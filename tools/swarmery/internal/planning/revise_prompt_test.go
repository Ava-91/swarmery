package planning

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/textdiff"
)

// ---------------------------------------------------------------------------
// Prompt split regression + BuildRevisePrompt
// ---------------------------------------------------------------------------

// goldenPlanPrompt is the recorded output of BuildPrompt. It exists so that a
// change to the plan-mode prompt — whether deliberate or a side effect of
// editing the shared phaseAProtocol constant — shows up as a reviewable diff
// in the PR that causes it.
const goldenPlanPrompt = "testdata/plan_prompt.golden"

// updateGolden rewrites the recorded prompt: `go test ./internal/planning
// -run TestBuildPromptMatchesGolden -update`. Commit the result together with
// the prompt change so reviewers see both halves.
var updateGolden = flag.Bool("update", false, "rewrite the recorded plan prompt")

// TestBuildPromptMatchesGolden guards the plan-mode prompt against unintended
// edits. It replaces a frozen sha256 baseline that pinned the bytes BuildPrompt
// produced before the PHASE A protocol was factored into phaseAProtocol: that
// hash was captured on a branch cut before the spec-driven planning change
// (315be57) landed, so it never matched main afterwards and reported an
// INTENTIONAL two-line addition as an opaque "drift" — main's CI was red on it
// from 2026-08-12 to 2026-08-14 while nothing was actually broken.
//
// A golden file keeps the tripwire and makes the failure readable: the diff
// below says exactly which prompt lines moved, so the reviewer can tell a
// deliberate change from an accident. The prompt's semantic contract (the
// instructions that MUST be present) is asserted separately and independently
// by TestBuildPrompt.
func TestBuildPromptMatchesGolden(t *testing.T) {
	got := BuildPrompt("fixed regression idea", "/ws-root")

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(goldenPlanPrompt), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPlanPrompt, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("rewrote %s (%d bytes)", goldenPlanPrompt, len(got))
		return
	}

	want, err := os.ReadFile(goldenPlanPrompt)
	if err != nil {
		t.Fatalf("read golden: %v — regenerate with `go test ./internal/planning -run TestBuildPromptMatchesGolden -update`", err)
	}
	if got != string(want) {
		t.Fatalf("plan-mode prompt changed (%d bytes, golden has %d):\n%s\n"+
			"If the change is deliberate, rerun with -update and commit the golden file alongside it.",
			len(got), len(want), textdiff.UnifiedDiff(goldenPlanPrompt, "BuildPrompt()", string(want), got))
	}
}

// TestPhaseAProtocolSharedByBothTemplates asserts the single shared constant
// really is in both rendered prompts, verbatim.
func TestPhaseAProtocolSharedByBothTemplates(t *testing.T) {
	plan := BuildPrompt("idea", "")
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
