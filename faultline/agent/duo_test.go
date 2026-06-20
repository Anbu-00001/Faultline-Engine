package main

import (
	"strings"
	"testing"
)

func TestDuoHandoff(t *testing.T) {
	// Nothing untested => no hand-off.
	if s := duoHandoff(nil); s != "" {
		t.Fatalf("empty min-set should yield no hand-off, got:\n%s", s)
	}

	min := []cutNode{{ID: "p", Name: "parse_tokens"}, {ID: "r", Name: "_read_message"}}

	// No flow configured: guidance form, names present, honest draft/human caveat.
	t.Setenv("FAULTLINE_DUO_FLOW", "")
	g := duoHandoff(min)
	for _, want := range []string{"parse_tokens", "_read_message", "@your-duo-flow", "draft", "approves"} {
		if !strings.Contains(g, want) {
			t.Fatalf("guidance hand-off missing %q:\n%s", want, g)
		}
	}

	// Flow configured: a live mention with the configured handle.
	t.Setenv("FAULTLINE_DUO_FLOW", "@faultline-fix")
	live := duoHandoff(min)
	if !strings.Contains(live, "@faultline-fix add a test covering") {
		t.Fatalf("configured hand-off should mention the flow handle:\n%s", live)
	}
	if strings.Contains(live, "@your-duo-flow") {
		t.Fatalf("configured hand-off must not show the placeholder handle:\n%s", live)
	}
}
