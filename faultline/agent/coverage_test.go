package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTreatUnnameableAsTested(t *testing.T) {
	nodes := []gNode{
		{ID: "1", Name: "Named"},
		{ID: "2", Name: ""},   // unnameable -> becomes a free interceptor
		{ID: "3", Name: "  "}, // whitespace-only -> also unnameable
		{ID: "4", Name: "Already"},
	}
	tested := map[string]bool{"4": true}
	ids := treatUnnameableAsTested(nodes, []string{"4"}, tested)
	if !tested["2"] || !tested["3"] {
		t.Fatalf("unnameable nodes must be marked tested, got %v", tested)
	}
	if tested["1"] {
		t.Fatalf("a named node must NOT be auto-marked tested")
	}
	n4 := 0
	for _, id := range ids {
		if id == "4" {
			n4++
		}
	}
	if n4 != 1 {
		t.Fatalf("an already-tested id must not be duplicated, got %d", n4)
	}
}

const sampleCobertura = `<?xml version="1.0"?>
<coverage line-rate="0.5">
 <packages>
  <package name="calc">
   <classes>
    <class name="tax" filename="calc/tax.go">
     <methods>
      <method name="applyRate"><lines><line number="10" hits="3"/></lines></method>
     </methods>
     <lines>
      <line number="10" hits="3"/>
      <line number="11" hits="0"/>
      <line number="12" hits="5"/>
     </lines>
    </class>
   </classes>
  </package>
 </packages>
</coverage>`

const sampleLcov = `SF:./calc/order.go
DA:1,2
DA:2,0
DA:5,1
end_of_record
SF:calc/tax.go
DA:10,3
end_of_record
`

func TestParseCobertura(t *testing.T) {
	cov, err := parseCobertura([]byte(sampleCobertura))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := cov["calc/tax.go"]
	want := map[int]bool{10: true, 12: true} // line 11 has 0 hits → not covered
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("covered lines = %v, want %v", got, want)
	}
}

func TestParseLcov(t *testing.T) {
	cov := parseLcov([]byte(sampleLcov))
	if !reflect.DeepEqual(cov["calc/order.go"], map[int]bool{1: true, 5: true}) {
		t.Errorf("order.go covered = %v", cov["calc/order.go"])
	}
	if !reflect.DeepEqual(cov["calc/tax.go"], map[int]bool{10: true}) {
		t.Errorf("tax.go covered = %v", cov["calc/tax.go"])
	}
}

func TestParseCoverageDetectsFormat(t *testing.T) {
	dir := t.TempDir()
	xmlPath := filepath.Join(dir, "cov.xml")
	lcovPath := filepath.Join(dir, "lcov.info")
	badPath := filepath.Join(dir, "nope.txt")
	os.WriteFile(xmlPath, []byte(sampleCobertura), 0o644)
	os.WriteFile(lcovPath, []byte(sampleLcov), 0o644)
	os.WriteFile(badPath, []byte("this is not coverage"), 0o644)

	if c, err := parseCoverage(xmlPath); err != nil || c["calc/tax.go"][10] != true {
		t.Errorf("cobertura detection failed: %v", err)
	}
	if c, err := parseCoverage(lcovPath); err != nil || c["calc/tax.go"][10] != true {
		t.Errorf("lcov detection failed: %v", err)
	}
	if _, err := parseCoverage(badPath); err == nil {
		t.Errorf("unrecognized format should error, not silently pass")
	}
}

func TestCoveredLinesForMatching(t *testing.T) {
	cov := lineCoverage{
		"calc/tax.go": {10: true},
		"x/foo.go":    {1: true},
		"y/foo.go":    {2: true},
	}
	// exact normalized match (leading ./ stripped)
	if m, ok := coveredLinesFor(cov, "./calc/tax.go"); !ok || !m[10] {
		t.Errorf("exact match failed: %v %v", m, ok)
	}
	// ambiguous suffix (foo.go matches two) → undecided, not a guess
	if _, ok := coveredLinesFor(cov, "foo.go"); ok {
		t.Errorf("ambiguous suffix must NOT match (errs toward flagging)")
	}
	// unique suffix match
	cov2 := lineCoverage{"src/calc/tax.go": {10: true}}
	if m, ok := coveredLinesFor(cov2, "calc/tax.go"); !ok || !m[10] {
		t.Errorf("unique suffix match failed: %v %v", m, ok)
	}
}

// resolveTested: coverage decides a definition when its file is in the report AND a
// line range is known; otherwise it falls back to the name heuristic — never marking
// a definition tested on a coverage miss.
func TestResolveTestedCoverageThenNameFallback(t *testing.T) {
	nodes := []gNode{
		{ID: "a", Name: "applyRate", FilePath: "calc/tax.go"}, // covered by execution
		{ID: "b", Name: "helper", FilePath: "calc/tax.go"},    // line range NOT executed
		{ID: "c", Name: "other", FilePath: "calc/util.go"},    // file absent → name fallback (tested)
		{ID: "d", Name: "misc", FilePath: "calc/tax.go"},      // no line range → name fallback (untested)
	}
	cov := lineCoverage{"calc/tax.go": {10: true, 12: true}}
	lineRange := map[string][2]int{"a": {10, 12}, "b": {11, 11}}
	corpus := "package x\nfunc TestThings(t *testing.T){ other() }\n" // mentions only "other"

	got := resolveTested(nodes, lineRange, cov, corpus)
	want := []string{"a", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tested = %v, want %v", got, want)
	}
}

// With no coverage report, resolveTested must equal the pure name heuristic
// (coveredDefIDs) — a strict, behavior-preserving generalization.
func TestResolveTestedNoCoverageEqualsNameHeuristic(t *testing.T) {
	nodes := []gNode{
		{ID: "a", Name: "applyRate", FilePath: "calc/tax.go"},
		{ID: "b", Name: "helper", FilePath: "calc/tax.go"},
	}
	corpus := "func TestApply(t *testing.T){ applyRate() }"
	got := resolveTested(nodes, nil, nil, corpus)
	if !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("name-only tested = %v, want [a]", got)
	}
}
