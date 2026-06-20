package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ---- Code Owners for the blast radius, not the diff -------------------------
//
// GitLab's Orbit graph has no code-ownership edge (its OWNER edge is User->Group
// only), so Faultline does NOT fake one. Instead it reads the project's real
// CODEOWNERS file — the same mechanism GitLab itself uses — and maps it onto the
// transitive blast radius. GitLab Code Owners require approval only from owners
// of the *changed* files; Faultline surfaces owners of *transitively-impacted*
// files that the diff would not pull in. This is a property-level join on the
// repo's own CODEOWNERS, clearly labeled — not a synthesized cross-domain edge.

// ownerRule is one CODEOWNERS line: a path pattern and the owners it assigns,
// scoped to a section (default section is "").
type ownerRule struct {
	pattern string
	re      *regexp.Regexp
	owners  []string
	section string
}

// codeownersLocations are the paths GitLab checks, in priority order; the first
// that exists wins (GitLab does not merge them).
var codeownersLocations = []string{"CODEOWNERS", ".gitlab/CODEOWNERS", "docs/CODEOWNERS"}

// readCodeowners returns the CODEOWNERS file content from the repo root, or "".
func readCodeowners(root string) string {
	for _, loc := range codeownersLocations {
		if data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(loc))); err == nil {
			return string(data)
		}
	}
	return ""
}

// sectionHeaderRe matches a CODEOWNERS section header: optional ^ (optional
// section), [Name], optional [N] approval count, then optional whitespace +
// default owners. The trailing group must be empty or start with whitespace, so
// a glob char-class pattern like "[abc]/x.go" is NOT mistaken for a section.
var sectionHeaderRe = regexp.MustCompile(`^\^?\[([^\]]+)\](?:\[[0-9]+\])?(\s+.*)?$`)

// ownerToken keeps @-mentions (users/groups/roles) and email owners, dropping
// anything else (e.g. stray tokens) so only real owners are recorded.
func ownerTokens(toks []string) []string {
	var out []string
	for _, t := range toks {
		if strings.HasPrefix(t, "@") || strings.Contains(t, "@") {
			out = append(out, t)
		}
	}
	return out
}

// parseCodeowners parses CODEOWNERS content into ordered rules, handling comments
// (#), section headers (with optional default owners), and "pattern owner..."
// lines. A rule with no explicit owners inherits its section's default owners.
func parseCodeowners(content string) []ownerRule {
	var rules []ownerRule
	section := ""
	var sectionDefaults []string
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if m := sectionHeaderRe.FindStringSubmatch(line); m != nil {
			section = strings.TrimSpace(m[1])
			sectionDefaults = ownerTokens(strings.Fields(m[2]))
			continue
		}
		fields := strings.Fields(line)
		owners := ownerTokens(fields[1:])
		if len(owners) == 0 {
			owners = sectionDefaults
		}
		if re := globToRegexp(fields[0]); re != nil {
			rules = append(rules, ownerRule{pattern: fields[0], re: re, owners: owners, section: section})
		}
	}
	return rules
}

// globToRegexp converts the common CODEOWNERS/gitignore pattern subset to an
// anchored regexp over a repo-relative, forward-slash path. Supports: leading /
// (root-anchored, else match at any depth), trailing / (directory → everything
// beneath), ** (any number of segments), * (within a segment), and ?.
func globToRegexp(pattern string) *regexp.Regexp {
	p := strings.TrimSpace(pattern)
	hadLeadingSlash := strings.HasPrefix(p, "/")
	p = strings.TrimPrefix(p, "/")
	dirOnly := strings.HasSuffix(p, "/")
	p = strings.TrimSuffix(p, "/")
	if p == "" {
		return nil
	}
	// gitignore/CODEOWNERS: a pattern with a slash anywhere (not just leading) is
	// anchored to the repo root; a slash-free pattern matches at any depth.
	anchored := hadLeadingSlash || strings.Contains(p, "/")

	var b strings.Builder
	b.WriteString("^")
	if !anchored {
		b.WriteString("(?:.*/)?")
	}
	for i := 0; i < len(p); i++ {
		switch c := p[i]; c {
		case '*':
			if i+1 < len(p) && p[i+1] == '*' {
				i++ // consume the second '*'
				if i+1 < len(p) && p[i+1] == '/' {
					i++ // "**/" => zero or more path segments
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '.', '+', '(', ')', '|', '{', '}', '^', '$', '\\', '[', ']':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	if dirOnly {
		b.WriteString("/.*")
	} else {
		// Match the path exactly, or as a directory prefix (a name owns everything beneath it).
		b.WriteString("(?:/.*)?")
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil
	}
	return re
}

// ownersFor returns the owners responsible for a file. GitLab precedence: within
// each section the LAST matching rule wins; owners are unioned across sections.
func ownersFor(path string, rules []ownerRule) []string {
	bySection := map[string][]string{}
	var order []string
	for _, r := range rules {
		if r.re.MatchString(path) {
			if _, seen := bySection[r.section]; !seen {
				order = append(order, r.section)
			}
			bySection[r.section] = r.owners // last match in this section wins
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, sec := range order {
		for _, o := range bySection[sec] {
			if !seen[o] {
				seen[o] = true
				out = append(out, o)
			}
		}
	}
	return out
}

// ownershipReach renders the "Code owners beyond the diff" section: owners of
// transitively-impacted files that are NOT in the diff and whose owners are not
// already required by the diff's own changed files. Returns "" when there is no
// CODEOWNERS file or nothing escapes. Pure given its inputs.
func ownershipReach(root string, blast []impacted, changedFiles map[string]bool) string {
	rules := parseCodeowners(readCodeowners(root))
	if len(rules) == 0 {
		return ""
	}
	// Owners already pulled in by the diff (owners of any changed file).
	diffOwners := map[string]bool{}
	for f := range changedFiles {
		for _, o := range ownersFor(f, rules) {
			diffOwners[o] = true
		}
	}

	type row struct {
		file   string
		owners []string
	}
	var rows []row
	escaped := map[string]bool{}
	seenFile := map[string]bool{}
	for _, it := range blast {
		f := it.FilePath
		if f == "" || changedFiles[f] || seenFile[f] {
			continue
		}
		seenFile[f] = true
		var newOwners []string
		for _, o := range ownersFor(f, rules) {
			if !diffOwners[o] {
				newOwners = append(newOwners, o)
				escaped[o] = true
			}
		}
		if len(newOwners) > 0 {
			rows = append(rows, row{f, newOwners})
		}
	}
	if len(rows) == 0 {
		return ""
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].file < rows[j].file })
	owners := make([]string, 0, len(escaped))
	for o := range escaped {
		owners = append(owners, o)
	}
	sort.Strings(owners)

	var b strings.Builder
	b.WriteString("\n👤 **Code owners beyond the diff** — the blast radius reaches code owned by ")
	b.WriteString(strings.Join(owners, ", "))
	b.WriteString(", who own *impacted* files but none of the *changed* files, so GitLab's Code Owners approval would not require them. Consider looping them in:\n\n")
	b.WriteString("| Impacted file (not in diff) | Owner(s) |\n|---|---|\n")
	for _, r := range rows {
		b.WriteString("| " + mdCell(r.file) + " | " + mdCell(strings.Join(r.owners, ", ")) + " |\n")
	}
	b.WriteString("\n<sub>Read from the project's CODEOWNERS file (not an Orbit graph edge). GitLab Code Owners require approval only from owners of *changed* files; Faultline maps ownership onto the full transitive blast radius — Code Owners for the blast radius, not the diff.</sub>\n")
	return b.String()
}
