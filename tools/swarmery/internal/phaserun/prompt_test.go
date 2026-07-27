package phaserun

import (
	"strings"
	"testing"
)

func TestBuildPrompt_InterpolatesAllFields(t *testing.T) {
	p := BuildPrompt(
		"/ws/proj/workspace/working/2026/07/27/thing/plan/phase-2-api.md",
		"phase-2-api.md",
		"# Phase 2 — API\n\n- [ ] a criterion\n")

	for _, want := range []string{
		// The contract lines the executor must obey.
		"executing ONE phase of an approved implementation plan",
		"tick its checkbox (- [ ] → - [x])",
		"The document lives at: /ws/proj/workspace/working/2026/07/27/thing/plan/phase-2-api.md",
		"Do NOT push, do NOT open PRs, do NOT merge",
		"PHASE BLOCKED:",
		"PHASE DONE.",
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
