package main

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Real test-coverage ingestion (Cobertura XML or lcov), replacing the conservative
// name-reference heuristic with actual execution data where it is available.
//
// A definition is "tested" when a line inside its [start_line, end_line] range was
// executed by the test suite. This needs (a) a coverage report and (b) the
// definition's line range (a best-effort Orbit lookup). When either is missing for
// a given definition, we fall back to the name heuristic — so coverage strictly
// upgrades precision and never silently loses signal.
//
// Honest direction: coverage only *decides* a definition when its file appears in
// the report AND we have its line range. An ambiguous file-path match is treated as
// undecided (fall back), so we never mark a definition tested on a shaky match —
// errs toward flagging, never toward a false "safe".

// lineCoverage maps a normalized file path to the set of line numbers that were
// executed at least once (hits > 0).
type lineCoverage map[string]map[int]bool

// ---- Cobertura XML (verified against the cobertura coverage DTD) ----
// coverage > packages > package > classes > class[@filename] > lines > line[@number,@hits]
// (lines also appear under methods>method>lines; both are collected).

type cbCoverage struct {
	XMLName  xml.Name    `xml:"coverage"`
	Packages []cbPackage `xml:"packages>package"`
}
type cbPackage struct {
	Classes []cbClass `xml:"classes>class"`
}
type cbClass struct {
	Filename string     `xml:"filename,attr"`
	Lines    []cbLine   `xml:"lines>line"`
	Methods  []cbMethod `xml:"methods>method"`
}
type cbMethod struct {
	Lines []cbLine `xml:"lines>line"`
}
type cbLine struct {
	Number int `xml:"number,attr"`
	Hits   int `xml:"hits,attr"`
}

// normCovPath normalizes a coverage/definition path for matching: trims spaces,
// a leading "./", and converts backslashes so Windows-style report paths line up.
func normCovPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimPrefix(p, "./")
	return p
}

// parseCoverage reads a Cobertura XML or lcov report and returns executed lines per
// file. Format is sniffed from the content (XML marker vs lcov "SF:").
func parseCoverage(path string) (lineCoverage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	head := strings.TrimSpace(string(data))
	if strings.HasPrefix(head, "<?xml") || strings.HasPrefix(head, "<coverage") {
		return parseCobertura(data)
	}
	if strings.Contains(head, "SF:") {
		return parseLcov(data), nil
	}
	return nil, fmt.Errorf("unrecognized coverage format (expected Cobertura XML or lcov) in %s", path)
}

func parseCobertura(data []byte) (lineCoverage, error) {
	var cov cbCoverage
	if err := xml.Unmarshal(data, &cov); err != nil {
		return nil, fmt.Errorf("invalid Cobertura XML: %w", err)
	}
	out := lineCoverage{}
	add := func(file string, lines []cbLine) {
		if file == "" {
			return
		}
		key := normCovPath(file)
		m := out[key]
		if m == nil {
			m = map[int]bool{}
			out[key] = m
		}
		for _, ln := range lines {
			if ln.Hits > 0 {
				m[ln.Number] = true
			}
		}
	}
	for _, p := range cov.Packages {
		for _, c := range p.Classes {
			add(c.Filename, c.Lines)
			for _, mth := range c.Methods {
				add(c.Filename, mth.Lines)
			}
		}
	}
	return out, nil
}

func parseLcov(data []byte) lineCoverage {
	out := lineCoverage{}
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var cur string
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "SF:"):
			cur = normCovPath(line[3:])
			if out[cur] == nil {
				out[cur] = map[int]bool{}
			}
		case strings.HasPrefix(line, "DA:") && cur != "":
			// DA:<line>,<hits>[,<checksum>]
			rest := line[3:]
			parts := strings.Split(rest, ",")
			if len(parts) >= 2 {
				num, e1 := strconv.Atoi(strings.TrimSpace(parts[0]))
				hits, e2 := strconv.Atoi(strings.TrimSpace(parts[1]))
				if e1 == nil && e2 == nil && hits > 0 {
					out[cur][num] = true
				}
			}
		case line == "end_of_record":
			cur = ""
		}
	}
	return out
}

// coveredLinesFor returns the executed-line set for a definition's file, matching
// conservatively: an exact normalized path first, else a UNIQUE suffix match on a
// path-component boundary. Ambiguous (zero or multiple) matches return ok=false so
// the caller falls back to the name heuristic rather than guessing.
func coveredLinesFor(cov lineCoverage, filePath string) (map[int]bool, bool) {
	f := normCovPath(filePath)
	if f == "" {
		return nil, false
	}
	if m, ok := cov[f]; ok {
		return m, true
	}
	var match string
	n := 0
	for k := range cov {
		if strings.HasSuffix(k, "/"+f) || strings.HasSuffix(f, "/"+k) {
			match = k
			n++
		}
	}
	if n == 1 {
		return cov[match], true
	}
	return nil, false
}

// resolveTested decides which definitions are tested. With a coverage report and a
// definition's line range, a definition is tested iff a line in [start,end] was
// executed; otherwise it falls back to the name-reference heuristic (`corpus`).
// Deterministic: returns a sorted, deduped ID list.
func resolveTested(nodes []gNode, lineRange map[string][2]int, cov lineCoverage, corpus string) []string {
	nameTested := map[string]bool{}
	for _, id := range coveredDefIDs(nodes, corpus) {
		nameTested[id] = true
	}

	set := map[string]bool{}
	for _, n := range nodes {
		if cov != nil {
			if rng, ok := lineRange[n.ID]; ok && rng[0] > 0 && rng[1] >= rng[0] {
				if lines, matched := coveredLinesFor(cov, n.FilePath); matched {
					covered := false
					for l := rng[0]; l <= rng[1]; l++ {
						if lines[l] {
							covered = true
							break
						}
					}
					if covered {
						set[n.ID] = true
					}
					continue // coverage decided this definition (covered or not)
				}
			}
		}
		// Undecided by coverage → name-reference heuristic.
		if nameTested[n.ID] {
			set[n.ID] = true
		}
	}

	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
