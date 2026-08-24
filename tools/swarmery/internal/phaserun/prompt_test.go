package phaserun

import (
	"strings"
	"testing"
)

func TestBuildPrompt_InterpolatesAllFields(t *testing.T) {
	// docPath is worktree-RELATIVE now (worktree.LendPlanDoc lends the doc in).
	p := BuildPrompt(
		".swarmery/plan/phase-2-api.md",
		"phase-2-api.md",
		"# Phase 2 — API\n\n- [ ] a criterion\n")

	for _, want := range []string{
		// The contract lines the executor must obey.
		"executing ONE phase of an approved implementation plan",
		"tick its checkbox (- [ ] → - [x])",
		// The doc is quoted by its WORKTREE-RELATIVE path: the contract's first
		// line makes the worktree the agent's one root, so an absolute path
		// outside it would contradict the fence and be refused by the sandbox.
		"lent into this worktree at: .swarmery/plan/phase-2-api.md",
		"This worktree is your ONE root",
		"copied back to the operator's workspace",
		"Do NOT push, do NOT open PRs, do NOT merge",
		// Headless run-context: the reply that says "waiting on my helpers" is the
		// reply that kills them, and the 0 exit code books the run as a success over
		// nothing (plan 70, 2026-07-30).
		"ENDING YOUR TURN ENDS THIS PROCESS",
		"PHASE BLOCKED:",
		"PHASE DONE.",
		// The summary contract: wsingest reads the doc's `## Completion Report`
		// section and the Plans UI shows that and nothing else, so a phase whose
		// account lives only in the final reply reads as "no summary of the work
		// written" over work that shipped.
		"## Completion Report",
		"the ONLY summary the operator's dashboard shows",
		// The embedded doc, framed by its rel path.
		"PHASE DOCUMENT (phase-2-api.md):",
		"# Phase 2 — API",
		"- [ ] a criterion",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q\n---\n%s", want, p)
		}
	}
}

func TestBuildPrompt_DocContentInsideFence(t *testing.T) {
	p := BuildPrompt("/x/doc.md", "doc.md", "BODY")
	// The doc body sits between the two dashed separators.
	first := strings.Index(p, "----------------------------------------")
	last := strings.LastIndex(p, "----------------------------------------")
	if first == -1 || first == last {
		t.Fatalf("prompt lacks the two dashed separators:\n%s", p)
	}
	if between := p[first:last]; !strings.Contains(between, "BODY") {
		t.Errorf("doc content not between separators:\n%s", p)
	}
	if !strings.HasSuffix(strings.TrimSpace(p), "----------------------------------------") {
		t.Errorf("prompt should end with the closing separator:\n%s", p)
	}
}
