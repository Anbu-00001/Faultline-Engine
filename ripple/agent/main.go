// ripple-agent assembles a code subgraph from GitLab Orbit and runs the
// ripple-engine to compute the transitive change-impact ("blast radius") of an
// MR's changed definitions.
//
// Orbit's query DSL is capped at 3 hops, so the agent fetches the project's
// full one-hop CALLS edges (and all definitions) and hands them to the engine,
// which computes the unbounded transitive closure.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ---- Orbit raw (`--format raw`) response shapes ----

type orbitNode struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	FilePath string `json:"file_path"`
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

func glabPath() string {
	if p := os.Getenv("GLAB"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return home + "/.local/bin/glab"
}

// orbitQuery runs a query body through glab and parses the raw JSON response.
func orbitQuery(body string) (orbitResp, error) {
	var out orbitResp
	cmd := exec.Command(glabPath(), "orbit", "remote", "query", "-", "--format", "raw")
	cmd.Stdin = strings.NewReader(body)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return out, fmt.Errorf("glab query failed: %v: %s", err, stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return out, fmt.Errorf("parse orbit response: %v", err)
	}
	return out, nil
}

func defsQuery(pid int) string {
	return fmt.Sprintf(`{"query":{"query_type":"traversal","node":{"id":"d","entity":"Definition","columns":["name","file_path"],"filters":{"project_id":{"op":"eq","value":%d}}},"limit":1000},"response_format":"raw"}`, pid)
}

func callsQuery(pid int) string {
	return fmt.Sprintf(`{"query":{"query_type":"traversal","nodes":[{"id":"a","entity":"Definition","columns":["name","file_path"],"filters":{"project_id":{"op":"eq","value":%d}}},{"id":"b","entity":"Definition","columns":["name","file_path"]}],"relationships":[{"type":"CALLS","from":"a","to":"b","min_hops":1,"max_hops":1,"direction":"outgoing"}],"limit":1000},"response_format":"raw"}`, pid)
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
	changedDefs := flag.String("changed-defs", "", "comma-separated Definition IDs that changed")
	changedFiles := flag.String("changed-files", "", "comma-separated file paths that changed")
	enginePath := flag.String("engine", "", "path to ripple-engine binary")
	graphOut := flag.String("graph-out", "", "optional path to write the normalized graph JSON")
	flag.Parse()

	if *pid == 0 || *enginePath == "" {
		fmt.Fprintln(os.Stderr, "usage: ripple-agent --project-id N --engine PATH (--changed-defs IDs | --changed-files paths)")
		os.Exit(2)
	}

	defsResp, err := orbitQuery(defsQuery(*pid))
	fatal(err)
	callsResp, err := orbitQuery(callsQuery(*pid))
	fatal(err)

	nodes := map[string]gNode{}
	add := func(ns []orbitNode) {
		for _, n := range ns {
			if _, ok := nodes[n.ID]; !ok {
				nodes[n.ID] = gNode{ID: n.ID, Name: n.Name, FilePath: n.FilePath, DefinitionType: "Function"}
			}
		}
	}
	add(defsResp.Result.Nodes)
	add(callsResp.Result.Nodes)

	var g graph
	for _, n := range nodes {
		g.Nodes = append(g.Nodes, n)
	}
	for _, e := range callsResp.Result.Edges {
		if e.Type == "CALLS" {
			g.Edges = append(g.Edges, gEdge{Type: "CALLS", From: e.FromID, To: e.ToID})
		}
	}

	changed := splitNonEmpty(*changedDefs)
	if *changedFiles != "" {
		want := map[string]bool{}
		for _, f := range splitNonEmpty(*changedFiles) {
			want[f] = true
		}
		for _, n := range nodes {
			if want[n.FilePath] {
				changed = append(changed, n.ID)
			}
		}
	}
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

	cmd := exec.Command(*enginePath, "--graph", graphPath, "--changed", strings.Join(changed, ","))
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	fatal(cmd.Run())
}
