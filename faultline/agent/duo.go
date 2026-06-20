package main

import (
	"os"
	"strings"
)

// ---- Closed loop with GitLab Duo --------------------------------------------
//
// Faultline's gate computes the *minimum test set* (a provably-minimal vertex
// cut) needed to close an untested blast radius. That set is the exact, concrete
// goal to hand to an agent — so the gate emits a hand-off using GitLab's
// documented flow trigger: mentioning a Duo flow's service account asks the flow
// to write the test in a draft MR. The live flow is configured per instance (AI
// -> Triggers, plus a .gitlab/duo/flows file); see CLOSED_LOOP.md. We do not
// auto-merge — Duo opens a *draft* MR and a human still approves.

// duoFlowHandle is the Duo flow service-account handle to mention, set per
// instance via FAULTLINE_DUO_FLOW once the flow is installed. Empty => the
// hand-off renders as guidance rather than a live mention (never a fake @-ping).
func duoFlowHandle() string {
	return strings.TrimSpace(os.Getenv("FAULTLINE_DUO_FLOW"))
}

// duoHandoff renders the closed-loop hand-off from the minimum test set. Returns
// "" when there is nothing untested to test. Pure given its inputs + the env.
func duoHandoff(minSet []cutNode) string {
	var names []string
	for _, c := range minSet {
		if c.Name != "" {
			names = append(names, "`"+c.Name+"`")
		}
	}
	if len(names) == 0 {
		return ""
	}
	goal := "add a test covering " + strings.Join(names, ", ") +
		" to close the untested blast radius Faultline flagged on this merge request"

	var b strings.Builder
	b.WriteString("\n🔄 **Close the loop with GitLab Duo** — the minimum test set above is the exact goal for an agent. ")
	if h := duoFlowHandle(); h != "" {
		b.WriteString("Mention the flow to open a draft MR with the test:\n\n")
		b.WriteString("> " + h + " " + goal + "\n")
	} else {
		b.WriteString("Install a Duo flow (see `CLOSED_LOOP.md`) and set `FAULTLINE_DUO_FLOW` to its service-account handle; the gate then posts:\n\n")
		b.WriteString("> @your-duo-flow " + goal + "\n")
	}
	b.WriteString("\n<sub>Uses GitLab's documented flow trigger (mention the flow's service account). Duo opens a **draft** MR; a human reviews and approves — the gate never auto-merges.</sub>\n")
	return b.String()
}
