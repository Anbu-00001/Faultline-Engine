package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobToRegexp(t *testing.T) {
	cases := []struct {
		pattern string
		match   []string
		noMatch []string
	}{
		{"*.rb", []string{"config/database.rb", "app/x/y.rb", "z.rb"}, []string{"z.rbx", "z.rb.txt", "z.go"}},
		{"**/*.rb", []string{"app/controllers/users/stars_controller.rb", "a.rb"}, []string{"a.go"}},
		{"/docs/", []string{"docs/intro.md", "docs/a/b.md"}, []string{"app/docs/x.md", "docs.md"}},
		{"docs/", []string{"docs/x.md", "app/docs/x.md"}, []string{"docs.md", "application/x"}},
		{"/config/*.yml", []string{"config/app.yml"}, []string{"config/sub/app.yml", "x/config/app.yml"}},
		{"/README.md", []string{"README.md"}, []string{"docs/README.md"}},
		{"lib/calc.go", []string{"lib/calc.go"}, []string{"src/lib/calc.go", "lib/calc.gox"}}, // mid-slash => root-anchored
		{"app/", []string{"app/models/user.rb", "src/app/x.go"}, []string{"application/x"}},   // slash-free dir => any depth
	}
	for _, c := range cases {
		re := globToRegexp(c.pattern)
		if re == nil {
			t.Fatalf("pattern %q produced nil regexp", c.pattern)
		}
		for _, p := range c.match {
			if !re.MatchString(p) {
				t.Errorf("pattern %q should match %q (re=%s)", c.pattern, p, re.String())
			}
		}
		for _, p := range c.noMatch {
			if re.MatchString(p) {
				t.Errorf("pattern %q should NOT match %q (re=%s)", c.pattern, p, re.String())
			}
		}
	}
}

func TestParseCodeownersSectionsAndDefaults(t *testing.T) {
	rules := parseCodeowners(`
# top comment
*.go @go-team
/docs/ @docs-team

[Backend] @backend
lib/*.rb
api/*.rb @api-special

^[Optional] @optional
scripts/*
`)
	want := map[string]struct {
		owners  string
		section string
	}{
		"*.go":      {"@go-team", ""},
		"/docs/":    {"@docs-team", ""},
		"lib/*.rb":  {"@backend", "Backend"},     // inherits section default
		"api/*.rb":  {"@api-special", "Backend"}, // explicit overrides default
		"scripts/*": {"@optional", "Optional"},
	}
	got := map[string]ownerRule{}
	for _, r := range rules {
		got[r.pattern] = r
	}
	for pat, w := range want {
		r, ok := got[pat]
		if !ok {
			t.Errorf("rule %q missing", pat)
			continue
		}
		if strings.Join(r.owners, ",") != w.owners {
			t.Errorf("%q owners = %v, want %s", pat, r.owners, w.owners)
		}
		if r.section != w.section {
			t.Errorf("%q section = %q, want %q", pat, r.section, w.section)
		}
	}
}

func TestOwnersForPrecedenceAndSectionUnion(t *testing.T) {
	rules := parseCodeowners(`
*.go @default
lib/*.go @lib-team
[Security] @sec
lib/secret.go @sec-override
`)
	owners := ownersFor("lib/secret.go", rules)
	set := strings.Join(owners, ",")
	if !strings.Contains(set, "@lib-team") || !strings.Contains(set, "@sec-override") {
		t.Fatalf("want last-match @lib-team (section \"\") + @sec-override (Security), got %v", owners)
	}
	if strings.Contains(set, "@default") {
		t.Fatalf("@default was superseded by lib/*.go in the same section; got %v", owners)
	}
}

func TestOwnershipReachEscapeBeyondDiff(t *testing.T) {
	root := t.TempDir()
	// Exact directory owners (no overlapping rules, so precedence is unambiguous).
	if err := os.WriteFile(filepath.Join(root, "CODEOWNERS"), []byte(
		"/shared/ @shared-team\n/billing/ @billing-team\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	blast := []impacted{
		{Name: "Config", FilePath: "shared/config.go"},    // in the diff
		{Name: "Util", FilePath: "shared/util.go"},        // impacted; @shared-team already required by the diff
		{Name: "Invoice", FilePath: "billing/invoice.go"}, // impacted; @billing-team escapes
	}
	changed := map[string]bool{"shared/config.go": true} // the MR's diff

	out := ownershipReach(root, blast, changed)
	if !strings.Contains(out, "@billing-team") || !strings.Contains(out, "billing/invoice.go") {
		t.Fatalf("billing owner beyond the diff should be surfaced:\n%s", out)
	}
	if strings.Contains(out, "@shared-team") {
		t.Fatalf("@shared-team owns a changed file (already required) — must not be flagged as escaped:\n%s", out)
	}
	if strings.Contains(out, "shared/util.go") {
		t.Fatalf("shared/util.go's owner is already in the diff — file must not appear:\n%s", out)
	}

	// No CODEOWNERS file => no section.
	if s := ownershipReach(t.TempDir(), blast, changed); s != "" {
		t.Fatalf("no CODEOWNERS should yield empty section, got:\n%s", s)
	}
}
