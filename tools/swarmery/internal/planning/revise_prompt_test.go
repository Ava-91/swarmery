package planning

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Prompt split regression + BuildRevisePrompt
// ---------------------------------------------------------------------------

// TestBuildPromptUnchangedByPromptSplit pins BuildPrompt's output to the exact
// bytes it produced BEFORE the PHASE A protocol was factored into the shared
// phaseAProtocol constant (hash captured on the pre-split tree). A drift here
// means the plan-mode prompt changed as a side effect of the revise work.
func TestBuildPromptUnchangedByPromptSplit(t *testing.T) {
	p := BuildPrompt("fixed regression idea")
	const wantSHA = "85930618f166f906f89d3c6fc20f4769c9e57cc904c9fb2442ad5587b95a2848"
	const wantLen = 3725
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(p))); got != wantSHA || len(p) != wantLen {
		t.Fatalf("BuildPrompt output drifted from the pre-split baseline:\n  sha=%s len=%d\n  want %s len=%d",
			got, len(p), wantSHA, wantLen)
	}
}

// TestPhaseAProtocolSharedByBothTemplates asserts the single shared constant
// really is in both rendered prompts, verbatim.
func TestPhaseAProtocolSharedByBothTemplates(t *testing.T) {
	plan := BuildPrompt("idea")
	revise := BuildRevisePrompt(ReviseInput{Reason: "r", PlanDir: "/p/plan", ScratchDir: "/s/uuid"})
	for name, p := range map[string]string{"plan": plan, "revise": revise} {
		if !strings.Contains(p, phaseAProtocol) {
			t.Errorf("%s prompt does not embed phaseAProtocol verbatim", name)
		}
	}
}

func TestBuildRevisePrompt(t *testing.T) {
	in := ReviseInput{
		Reason:     "phase 3 failed against the real API",
		PlanDir:    "/ws/2026/08/11/my-task/plan",
		ScratchDir: "/db/revisions/uuid-r1",
		PlanTitle:  "My Task Plan",
		Evidence:   "| 1 | `phase-1-a.md` | 3/3 | done | completed | — | — |",
		DoneDocs:   []string{"phase-1-a.md", "phase-2-b.md"},
		Readme:     "# My Task Plan\n\n| # | Phase | Doc | Depends on |",
		Docs:       []SeedDoc{{Name: "phase-3-c.md", Content: "# Phase 3\n\n## Completion Report\n"}},
	}
	p := BuildRevisePrompt(in)

	for _, must := range []string{
		"never call the AskUserQuestion tool",      // shared PHASE A protocol
		"PHASE B — STAGE THE REVISION",             // revise-specific PHASE B
		in.PlanDir,                                 // the plan being revised
		in.ScratchDir,                              // where to write proposals
		"MUST NOT be changed, renamed, or deleted", // done-doc immutability rule
		"- phase-1-a.md",                           // …with the concrete list
		"- phase-2-b.md",                           //
		"`## Completion Report` section as the LAST section", // stub requirement
		"revision.json",                                 // manifest contract
		`"action":"rename","renameFrom"`,                // rename shape
		"create | update | rename | delete",             // closed action set
		"REVISION STAGED: " + in.ScratchDir,             // completion sentinel
		"phase 3 failed against the real API",           // operator's reason
		"My Task Plan",                                  // plan title
		"--- phase-3-c.md ---",                          // seed doc header
		"## Completion Report",                          // seed doc content travels whole
		"| 1 | `phase-1-a.md` | 3/3 | done | completed", // evidence table
	} {
		if !strings.Contains(p, must) {
			t.Errorf("revise prompt missing %q", must)
		}
	}

	// The revise prompt must never INSTRUCT finishing with PLAN SAVED — the
	// only mention of that marker is the prohibition.
	if strings.Contains(p, "PLAN SAVED: <absolute path") {
		t.Error("revise prompt carries the plan-mode PLAN SAVED completion instruction")
	}
	if !strings.Contains(p, `never emit a "PLAN SAVED:" line`) {
		t.Error("revise prompt missing the explicit PLAN SAVED prohibition")
	}
	// And never the plan-mode PHASE B.
	if strings.Contains(p, "PHASE B — PLAN (") {
		t.Error("revise prompt embeds the plan-mode PHASE B")
	}
}

func TestBuildRevisePromptNoDoneDocs(t *testing.T) {
	p := BuildRevisePrompt(ReviseInput{Reason: "r", PlanDir: "/p", ScratchDir: "/s"})
	if !strings.Contains(p, "deleted: (none)") {
		t.Errorf("empty done-doc list should render as (none); got:\n%s", p[:600])
	}
}
