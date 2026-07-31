package planrun

import (
	"strings"
	"testing"
)

func testPhases() []Phase {
	return []Phase{
		{Seq: 1, Name: "Schema", DocPath: "/ws/plan/phase-1-schema.md", Done: 2, Total: 2},
		{Seq: 2, Name: "UI", DocPath: "/ws/plan/phase-2-ui.md", Done: 0, Total: 3, DependsOn: []int{1}},
	}
}

func TestBuildPromptDelegatesToTheSkill(t *testing.T) {
	got := BuildPrompt("/ws/plan", "# Epic\n\nObjective: ship.\n", testPhases(), ModeAuto)

	// The prompt's job is to point at the skill, not to restate the procedure —
	// a re-specified contract would fork the maintained one.
	if !strings.Contains(got, "run-plan") {
		t.Error("prompt does not name the run-plan skill")
	}
	for _, want := range []string{
		"/ws/plan",              // plan dir
		"Objective: ship.",      // README inlined
		"PLAN BLOCKED at phase", // stop-and-report contract
		"PLAN DONE",             // success contract
		"Do NOT push",           // headless safety
		"DEFERRED",              // manual legs cannot run headlessly
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

// TestBuildPromptDemandsPerPhaseCompletionReport pins the one artifact the
// dashboard reads as a phase summary. The skill's report contract sends executors
// to `<task-dir>/reports/phase-<N>-report.md`, which wsingest never parses; only
// the phase doc's `## Completion Report` section reaches the Plans UI. Dropping
// this instruction fails silently — every phase ships and every Summary tab says
// "no summary of the work written" — so it gets its own test.
func TestBuildPromptDemandsPerPhaseCompletionReport(t *testing.T) {
	for _, mode := range []Mode{ModeAuto, ModeSubagents, ModeInline} {
		got := BuildPrompt("/ws/plan", "readme", testPhases(), mode)
		for _, want := range []string{
			"## Completion Report",
			"A phase left with no Completion Report is not finished",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("mode %s: prompt missing %q", mode, want)
			}
		}
	}
}

// TestBuildPromptForbidsReturningWithChildrenInFlight pins the run-context fact the
// skill cannot know: under `claude -p` the reply that says "waiting on the
// executors" is the reply that kills them, and the 0 exit code then books the run
// as `done` over nothing. Plan 70 lost 13m24s to exactly that on 2026-07-30. If
// this instruction is ever dropped from the prompt, the failure is silent — a green
// chip — so it gets its own test rather than riding along in the omnibus one.
func TestBuildPromptForbidsReturningWithChildrenInFlight(t *testing.T) {
	for _, mode := range []Mode{ModeAuto, ModeSubagents, ModeInline} {
		got := BuildPrompt("/ws/plan", "readme", testPhases(), mode)
		for _, want := range []string{
			"ENDING YOUR TURN ENDS THIS PROCESS", // the mechanism
			"Await every subagent you dispatch",  // the rule that follows from it
		} {
			if !strings.Contains(got, want) {
				t.Errorf("mode %q: prompt missing %q", mode, want)
			}
		}
	}
}

func TestBuildPromptManifestMarksDonePhases(t *testing.T) {
	got := BuildPrompt("/ws/plan", "readme", testPhases(), ModeAuto)

	if !strings.Contains(got, "Phase 1 [DONE — skip] Schema (2/2 criteria)") {
		t.Errorf("finished phase not marked skippable:\n%s", got)
	}
	if !strings.Contains(got, "Phase 2 [TODO] UI (0/3 criteria, depends on phase 1)") {
		t.Errorf("open phase or its dependency missing:\n%s", got)
	}
	// Doc paths travel as references; phase CONTENT must not be inlined (that is
	// what would blow the context window before the first line of work).
	for _, p := range testPhases() {
		if !strings.Contains(got, p.DocPath) {
			t.Errorf("manifest missing doc path %q", p.DocPath)
		}
	}
}

func TestBuildPromptModeDirectives(t *testing.T) {
	cases := []struct {
		mode        Mode
		want        string
		mustNotHave string
	}{
		{ModeSubagents, "subagent-driven", "implement the phases YOURSELF"},
		{ModeInline, "implement the phases YOURSELF", "Do not implement phase work yourself"},
	}
	for _, tc := range cases {
		t.Run(string(tc.mode), func(t *testing.T) {
			got := BuildPrompt("/ws/plan", "readme", testPhases(), tc.mode)
			if !strings.Contains(got, tc.want) {
				t.Errorf("prompt for mode %s missing %q", tc.mode, tc.want)
			}
			if strings.Contains(got, tc.mustNotHave) {
				t.Errorf("prompt for mode %s leaks the other mode's directive %q", tc.mode, tc.mustNotHave)
			}
		})
	}
	// auto adds nothing: the skill's own triage table stays authoritative.
	if got := BuildPrompt("/ws/plan", "readme", testPhases(), ModeAuto); strings.Contains(got, "EXECUTION MODE") {
		t.Error("auto mode must not override the skill's route choice")
	}
}

func TestValidModeDegradesToAuto(t *testing.T) {
	for _, in := range []string{"", "AUTO", "workflow", "nonsense", "  "} {
		if got := ValidMode(in); got != ModeAuto {
			t.Errorf("ValidMode(%q) = %q, want auto", in, got)
		}
	}
	if got := ValidMode(" subagents "); got != ModeSubagents {
		t.Errorf("ValidMode(padded) = %q, want subagents", got)
	}
	if got := ValidMode("inline"); got != ModeInline {
		t.Errorf("ValidMode(inline) = %q, want inline", got)
	}
}
