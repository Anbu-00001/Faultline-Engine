package main

import (
	"strings"
	"testing"
)

func TestNormalizeDedupAndEdgeMapping(t *testing.T) {
	var defs, calls orbitResp
	defs.Result.Nodes = []orbitNode{
		{Type: "Definition", ID: "A", Name: "applyRate", FilePath: "calc/tax.go", DefinitionType: "Function"},
		{Type: "Definition", ID: "C", Name: "CalculateTax", FilePath: "calc/tax.go", DefinitionType: "Function"},
		{Type: "Definition", ID: "D", Name: "ApplyDiscount", FilePath: "calc/tax.go"}, // no definition_type
	}
	calls.Result.Nodes = []orbitNode{
		{ID: "A", Name: "applyRate", FilePath: "calc/tax.go"}, // duplicate of A
		{ID: "C", Name: "CalculateTax", FilePath: "calc/tax.go"},
	}
	calls.Result.Edges = []orbitEdge{
		{From: "Definition", FromID: "C", To: "Definition", ToID: "A", Type: "CALLS"},
		{FromID: "C", ToID: "A", Type: "IMPORTS"}, // non-CALLS must be ignored
	}

	g := normalize(defs, calls)

	if len(g.Nodes) != 3 {
		t.Fatalf("want 3 deduped nodes, got %d", len(g.Nodes))
	}
	if len(g.Edges) != 1 {
		t.Fatalf("want 1 CALLS edge (IMPORTS ignored), got %d", len(g.Edges))
	}
	if g.Edges[0].From != "C" || g.Edges[0].To != "A" {
		t.Fatalf("edge maps from_id/to_id wrong: %+v", g.Edges[0])
	}
	for _, n := range g.Nodes {
		if n.ID == "D" && n.DefinitionType != "Function" {
			t.Fatalf("missing definition_type should default to Function, got %q", n.DefinitionType)
		}
	}
}

func TestResolveChanged(t *testing.T) {
	g := graph{Nodes: []gNode{
		{ID: "A", FilePath: "calc/tax.go"},
		{ID: "C", FilePath: "calc/tax.go"},
		{ID: "T", FilePath: "calc/order.go"},
	}}

	if got := resolveChanged(g, nil, []string{"calc/tax.go"}); len(got) != 2 {
		t.Fatalf("by-file: want 2 defs in tax.go, got %d (%v)", len(got), got)
	}
	if got := resolveChanged(g, []string{"A", "A"}, nil); len(got) != 1 || got[0] != "A" {
		t.Fatalf("by-id dedup: want [A], got %v", got)
	}
	if got := resolveChanged(g, nil, []string{"missing.go"}); len(got) != 0 {
		t.Fatalf("unknown file: want 0, got %v", got)
	}
	// union of ids + files, deduped (A appears in both)
	if got := resolveChanged(g, []string{"A"}, []string{"calc/order.go"}); len(got) != 2 {
		t.Fatalf("union: want 2 (A + T), got %d (%v)", len(got), got)
	}
}

func TestRenderMarkdownEmptyBlastRadius(t *testing.T) {
	md := renderMarkdown(report{ImpactedCount: 0}, []string{"ApplyDiscount"})
	if !strings.Contains(md, "Empty blast radius") {
		t.Fatalf("empty radius should be called out, got:\n%s", md)
	}
	if strings.Contains(md, "| Impacted definition |") {
		t.Fatalf("empty radius must not render a table, got:\n%s", md)
	}
	if !strings.Contains(md, "`ApplyDiscount`") {
		t.Fatalf("changed name should be shown, got:\n%s", md)
	}
}

func TestRenderMarkdownDeepChainFlagsCap(t *testing.T) {
	r := report{
		ImpactedCount: 2,
		MaxDepth:      5,
		BlastRadius: []impacted{
			{Name: "CalculateTax", FilePath: "calc/tax.go", Distance: 1},
			{ID: "999", FilePath: "calc/order.go", Distance: 5}, // no name -> falls back to ID
		},
	}
	md := renderMarkdown(r, []string{"applyRate"})
	if !strings.Contains(md, "2 definition(s) transitively affected") {
		t.Fatalf("missing impact count, got:\n%s", md)
	}
	if !strings.Contains(md, "beyond Orbit's 3-hop query cap") {
		t.Fatalf("depth>3 should flag the 3-hop cap (the moat), got:\n%s", md)
	}
	if !strings.Contains(md, "| `CalculateTax` | calc/tax.go | 1 |") {
		t.Fatalf("table row malformed, got:\n%s", md)
	}
	if !strings.Contains(md, "| `999` |") {
		t.Fatalf("nameless impacted node should fall back to ID, got:\n%s", md)
	}
}

func TestRenderMarkdownShallowDoesNotClaimCap(t *testing.T) {
	r := report{ImpactedCount: 1, MaxDepth: 2, BlastRadius: []impacted{{Name: "X", Distance: 2}}}
	md := renderMarkdown(r, nil)
	if strings.Contains(md, "3-hop") {
		t.Fatalf("depth<=3 must NOT claim to beat the 3-hop cap, got:\n%s", md)
	}
}
