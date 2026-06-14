// ripple-agent assembles a code subgraph from GitLab Orbit and runs the
// ripple-engine to compute the transitive change-impact ("blast radius") of an
// MR's changed definitions.
//
// Orbit's query DSL is capped at 3 hops, so the agent fetches the project's
// full one-hop CALLS edges (plus all definitions) and hands them to the engine,
// which computes the unbounded transitive closure.
//
// Two access modes:
//   - glab (default): shells out to `glab orbit remote query` (local dev).
//   - rest: calls POST /api/v4/orbit/query directly with a bearer token
//     (for the hosted/container run, using $AI_FLOW_GITLAB_TOKEN).
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

// ---- Orbit raw (`--format raw`) response shapes ----

type orbitNode struct {
	Type           string `json:"type"`
	ID             string `json:"id"`
	Name           string `json:"name"`
	FilePath       string `json:"file_path"`
	DefinitionType string `json:"definition_type"`
}

type orbitEdge struct {
	From   string `json:"from"`
	FromID string `json:"from_id"`
	To     string `json:"to"`
	ToID   string `json:"to_id"`
	Type   string `json:"type"`
}

type orbitResp struct {
	Result struct {
		Nodes []orbitNode `json:"nodes"`
		Edges []orbitEdge `json:"edges"`
	} `json:"result"`
}

// ---- normalized graph consumed by ripple-engine ----

type gNode struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	FilePath       string `json:"file_path"`
	DefinitionType string `json:"definition_type"`
}

type gEdge struct {
	Type string `json:"type"`
	From string `json:"from"`
	To   string `json:"to"`
}

type graph struct {
	Nodes []gNode `json:"nodes"`
	Edges []gEdge `json:"edges"`
}

// ---- engine report (mirror of ripple-engine's Report) ----

type impacted struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	FilePath       string `json:"file_path"`
	DefinitionType string `json:"definition_type"`
	Distance       int    `json:"distance"`
}

type report struct {
	Changed       []string   `json:"changed"`
	ImpactedCount int        `json:"impacted_count"`
	MaxDepth      int        `json:"max_depth"`
	BlastRadius   []impacted `json:"blast_radius"`
}

// normalize merges the definition + CALLS query results into one deduped graph.
// Pure function (no I/O) so it can be unit-tested.
func normalize(defs, calls orbitResp) graph {
	nm := map[string]gNode{}
	addNodes := func(ns []orbitNode) {
		for _, n := range ns {
			if _, ok := nm[n.ID]; ok {
				continue
			}
			dt := n.DefinitionType
			if dt == "" {
				dt = "Function"
			}
			nm[n.ID] = gNode{ID: n.ID, Name: n.Name, FilePath: n.FilePath, DefinitionType: dt}
		}
	}
	addNodes(defs.Result.Nodes)
	addNodes(calls.Result.Nodes)

	var g graph
	for _, n := range nm {
		g.Nodes = append(g.Nodes, n)
	}
	for _, e := range calls.Result.Edges {
		if e.Type == "CALLS" {
			g.Edges = append(g.Edges, gEdge{Type: "CALLS", From: e.FromID, To: e.ToID})
		}
	}
	return g
}

// resolveChanged turns explicit Definition IDs and/or changed file paths into a
// deduped set of changed Definition IDs. Pure function.
func resolveChanged(g graph, changedDefs, changedFiles []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, id := range changedDefs {
		add(id)
	}
	if len(changedFiles) > 0 {
		want := map[string]bool{}
		for _, f := range changedFiles {
			want[f] = true
		}
		for _, n := range g.Nodes {
			if want[n.FilePath] {
				add(n.ID)
			}
		}
	}
	return out
}

// renderMarkdown turns an engine report into a Markdown MR verdict. Pure (no I/O)
// so it can be unit-tested. changedNames are the human-readable names of the
// changed definitions (falls back to IDs when a name is unknown).
func renderMarkdown(r report, changedNames []string) string {
	var b strings.Builder
	b.WriteString("## 🌊 Ripple — change-impact analysis\n\n")
	if len(changedNames) > 0 {
		b.WriteString("**Changed:** ")
		for i, n := range changedNames {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString("`" + n + "`")
		}
		b.WriteString("\n\n")
	}
	if r.ImpactedCount == 0 {
		b.WriteString("✅ **Empty blast radius.** No definition transitively calls the changed code in the indexed graph.\n")
		return b.String()
	}
	b.WriteString(fmt.Sprintf("⚠️ **%d definition(s) transitively affected** — max depth **%d**", r.ImpactedCount, r.MaxDepth))
	if r.MaxDepth > 3 {
		b.WriteString(", beyond Orbit's 3-hop query cap")
	}
	b.WriteString(".\n\n| Impacted definition | File | Caller distance |\n|---|---|---|\n")
	for _, it := range r.BlastRadius {
		name := it.Name
		if name == "" {
			name = it.ID
		}
		b.WriteString(fmt.Sprintf("| `%s` | %s | %d |\n", name, it.FilePath, it.Distance))
	}
	b.WriteString("\n<sub>Transitive reverse-`CALLS` closure computed by the Ripple engine over GitLab Orbit's knowledge graph.</sub>\n")
	return b.String()
}

// postMRNote posts body as a note on a merge request via the GitLab REST API.
func postMRNote(host, token string, projectID, mrIID int, body string) error {
	endpoint := fmt.Sprintf("https://%s/api/v4/projects/%d/merge_requests/%d/notes", host, projectID, mrIID)
	form := url.Values{}
	form.Set("body", body)
	req, err := http.NewRequest("POST", endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("MR note POST HTTP %d: %s", resp.StatusCode, truncate(string(data), 200))
	}
	return nil
}

// orbitToken resolves the GitLab token from the container/CI env.
func orbitToken() string {
	if t := os.Getenv("AI_FLOW_GITLAB_TOKEN"); t != "" {
		return t
	}
	return os.Getenv("GITLAB_TOKEN")
}

func glabPath() string {
	if p := os.Getenv("GLAB"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return home + "/.local/bin/glab"
}

func queryGlab(body string) (orbitResp, error) {
	var out orbitResp
	cmd := exec.Command(glabPath(), "orbit", "remote", "query", "-", "--format", "raw")
	cmd.Stdin = strings.NewReader(body)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		return out, fmt.Errorf("glab query failed: %v: %s", err, se.String())
	}
	return out, json.Unmarshal(so.Bytes(), &out)
}

func queryREST(body, host, token string) (orbitResp, error) {
	var out orbitResp
	req, err := http.NewRequest("POST", "https://"+host+"/api/v4/orbit/query", strings.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("orbit REST HTTP %d: %s", resp.StatusCode, truncate(string(data), 200))
	}
	return out, json.Unmarshal(data, &out)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func defsQuery(pid int) string {
	return fmt.Sprintf(`{"query":{"query_type":"traversal","node":{"id":"d","entity":"Definition","columns":["name","file_path","definition_type"],"filters":{"project_id":{"op":"eq","value":%d}}},"limit":1000},"response_format":"raw"}`, pid)
}

func callsQuery(pid int) string {
	return fmt.Sprintf(`{"query":{"query_type":"traversal","nodes":[{"id":"a","entity":"Definition","columns":["name","file_path","definition_type"],"filters":{"project_id":{"op":"eq","value":%d}}},{"id":"b","entity":"Definition","columns":["name","file_path","definition_type"]}],"relationships":[{"type":"CALLS","from":"a","to":"b","min_hops":1,"max_hops":1,"direction":"outgoing"}],"limit":1000},"response_format":"raw"}`, pid)
}

func splitNonEmpty(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func fatal(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "ripple-agent:", err)
		os.Exit(1)
	}
}

func main() {
	pid := flag.Int("project-id", 0, "GitLab project ID")
	changedDefs := flag.String("changed-defs", "", "comma-separated Definition IDs changed")
	changedFiles := flag.String("changed-files", "", "comma-separated changed file paths")
	enginePath := flag.String("engine", "", "path to ripple-engine binary")
	mode := flag.String("mode", "glab", "orbit access mode: glab | rest")
	host := flag.String("host", "gitlab.com", "GitLab host (rest mode)")
	graphOut := flag.String("graph-out", "", "optional path to write the normalized graph JSON")
	format := flag.String("format", "json", "verdict output when not posting: json | md")
	postMR := flag.Int("post-mr", 0, "if >0, POST the verdict as a note to this merge request IID")
	mrProject := flag.Int("mr-project-id", 0, "project ID for --post-mr (defaults to --project-id)")
	flag.Parse()

	if *pid == 0 || *enginePath == "" {
		fmt.Fprintln(os.Stderr, "usage: ripple-agent --project-id N --engine PATH [--mode glab|rest] [--format md] [--post-mr IID] (--changed-defs IDs | --changed-files paths)")
		os.Exit(2)
	}

	query := queryGlab
	if *mode == "rest" {
		token := orbitToken()
		if token == "" {
			fatal(fmt.Errorf("rest mode requires AI_FLOW_GITLAB_TOKEN or GITLAB_TOKEN env var"))
		}
		query = func(body string) (orbitResp, error) { return queryREST(body, *host, token) }
	}

	defs, err := query(defsQuery(*pid))
	fatal(err)
	calls, err := query(callsQuery(*pid))
	fatal(err)

	g := normalize(defs, calls)
	changed := resolveChanged(g, splitNonEmpty(*changedDefs), splitNonEmpty(*changedFiles))
	if len(changed) == 0 {
		fatal(fmt.Errorf("no changed definitions resolved (project may be unindexed, or files have no definitions)"))
	}

	graphPath := *graphOut
	if graphPath == "" {
		f, err := os.CreateTemp("", "ripple-graph-*.json")
		fatal(err)
		graphPath = f.Name()
		f.Close()
	}
	data, _ := json.MarshalIndent(g, "", "  ")
	fatal(os.WriteFile(graphPath, data, 0o644))

	engineOut, err := exec.Command(*enginePath, "--graph", graphPath, "--changed", strings.Join(changed, ",")).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			fatal(fmt.Errorf("engine failed: %v: %s", err, strings.TrimSpace(string(ee.Stderr))))
		}
		fatal(fmt.Errorf("engine failed: %v", err))
	}

	var rep report
	fatal(json.Unmarshal(engineOut, &rep))

	// Map changed IDs back to human-readable names for the verdict.
	nameByID := map[string]string{}
	for _, n := range g.Nodes {
		nameByID[n.ID] = n.Name
	}
	var changedNames []string
	for _, id := range changed {
		if nm := nameByID[id]; nm != "" {
			changedNames = append(changedNames, nm)
		} else {
			changedNames = append(changedNames, id)
		}
	}

	if *postMR > 0 {
		token := orbitToken()
		if token == "" {
			fatal(fmt.Errorf("--post-mr requires AI_FLOW_GITLAB_TOKEN or GITLAB_TOKEN env var"))
		}
		proj := *mrProject
		if proj == 0 {
			proj = *pid
		}
		fatal(postMRNote(*host, token, proj, *postMR, renderMarkdown(rep, changedNames)))
		fmt.Printf("posted Ripple verdict to MR !%d (project %d): %d impacted, max depth %d\n",
			*postMR, proj, rep.ImpactedCount, rep.MaxDepth)
		return
	}

	if *format == "md" {
		fmt.Println(renderMarkdown(rep, changedNames))
	} else {
		fmt.Print(string(engineOut))
	}
}
