package planning

import "testing"

// ---------------------------------------------------------------------------
// BuildPrompt
// ---------------------------------------------------------------------------

func TestBuildPrompt(t *testing.T) {
	p := BuildPrompt("  add a dark mode toggle  ")

	// Idea is interpolated verbatim (trimmed).
	if want := "The user's idea:\nadd a dark mode toggle"; !contains(p, want) {
		t.Errorf("prompt missing trimmed idea; got tail:\n%s", tailStr(p, 200))
	}
	// Wizard-protocol invariants.
	for _, must := range []string{
		"never call the AskUserQuestion tool", // headless: hook does not fire
		"PHASE A",                             // interview phase marker
		"PHASE B",                             // planning phase marker
		"PROCEED",                             // terminal instruction
		"PRIVATE WORKSPACE",                   // plan lands in the workspace
		"PLAN SAVED:",                         // unambiguous completion signal
		"@task-planner",                       // small-scope agent
		"@implementation-planner",             // large-scope agent
		"fenced json block",                   // structured-output contract
		"runningPlan",                         // running plan required every turn
	} {
		if !contains(p, must) {
			t.Errorf("prompt missing required instruction %q", must)
		}
	}
}

func TestBuildPromptEmptyIdea(t *testing.T) {
	p := BuildPrompt("")
	if !contains(p, "The user's idea:") {
		t.Fatalf("empty idea dropped the frame:\n%s", p)
	}
}

// ---------------------------------------------------------------------------
// BuildAnswerMessage
// ---------------------------------------------------------------------------

func TestBuildAnswerMessageSingleSelect(t *testing.T) {
	q := PlanningQuestion{
		ID:   "approach",
		Type: "single_select",
		Options: []PlanningOption{
			{ID: "opt-a", Label: "Option A"},
			{ID: "opt-b", Label: "Option B"},
		},
	}
	msg := BuildAnswerMessage(q, []string{"opt-a"}, "")

	mustContain := []string{
		`"approach"`,                   // question id
		"opt-a — Option A",             // id — label
		"Rebuild the running plan",     // closing instruction
		"continue the interview",       // protocol reminder
		"fenced json block",            // format hint
	}
	for _, want := range mustContain {
		if !contains(msg, want) {
			t.Errorf("BuildAnswerMessage missing %q\nmessage:\n%s", want, msg)
		}
	}
	// The non-selected option must not appear.
	if contains(msg, "opt-b") {
		t.Errorf("BuildAnswerMessage should not include non-selected opt-b")
	}
}

func TestBuildAnswerMessageWithOther(t *testing.T) {
	q := PlanningQuestion{
		ID:   "scope",
		Type: "single_select",
		Options: []PlanningOption{
			{ID: "small", Label: "Small"},
			{ID: "other", Label: "Other", IsOther: true},
		},
	}
	msg := BuildAnswerMessage(q, []string{"other"}, "Use the existing API layer")

	if !contains(msg, "Other: Use the existing API layer") {
		t.Errorf("BuildAnswerMessage missing other text\nmessage:\n%s", msg)
	}
}

func TestBuildAnswerMessageMultiSelect(t *testing.T) {
	q := PlanningQuestion{
		ID:   "features",
		Type: "multi_select",
		Options: []PlanningOption{
			{ID: "feat-a", Label: "Feature A"},
			{ID: "feat-b", Label: "Feature B"},
			{ID: "feat-c", Label: "Feature C"},
		},
	}
	msg := BuildAnswerMessage(q, []string{"feat-a", "feat-c"}, "")

	if !contains(msg, "feat-a — Feature A") {
		t.Errorf("BuildAnswerMessage missing feat-a\nmessage:\n%s", msg)
	}
	if !contains(msg, "feat-c — Feature C") {
		t.Errorf("BuildAnswerMessage missing feat-c\nmessage:\n%s", msg)
	}
	if contains(msg, "feat-b") {
		t.Errorf("BuildAnswerMessage should not include non-selected feat-b")
	}
}

func TestBuildAnswerMessageUnknownID(t *testing.T) {
	// An unknown option id (defensive) should fall back to printing the id itself.
	q := PlanningQuestion{
		ID:      "q1",
		Type:    "single_select",
		Options: []PlanningOption{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}},
	}
	msg := BuildAnswerMessage(q, []string{"unknown-id"}, "")
	if !contains(msg, "unknown-id — unknown-id") {
		t.Errorf("BuildAnswerMessage should echo unknown id as fallback\nmessage:\n%s", msg)
	}
}

// ---------------------------------------------------------------------------
// BuildRefineMessage
// ---------------------------------------------------------------------------

func TestBuildRefineMessage(t *testing.T) {
	msg := BuildRefineMessage("Focus only on the backend layer")

	mustContain := []string{
		"Focus only on the backend layer", // verbatim quote
		"Rebuild affected plan fields",    // rebuild instruction
		"accumulated decisions",           // reference to history
		"exactly one next question",       // interview continuation
		"per the protocol",                // format reminder
	}
	for _, want := range mustContain {
		if !contains(msg, want) {
			t.Errorf("BuildRefineMessage missing %q\nmessage:\n%s", want, msg)
		}
	}
}

// ---------------------------------------------------------------------------
// BuildProceedMessage
// ---------------------------------------------------------------------------

func TestBuildProceedMessage(t *testing.T) {
	msg := BuildProceedMessage()

	mustContain := []string{
		"PROCEED — write the plan now.", // literal terminal marker
		"PHASE B",                       // phase reference
	}
	for _, want := range mustContain {
		if !contains(msg, want) {
			t.Errorf("BuildProceedMessage missing %q\nmessage:\n%s", want, msg)
		}
	}
}

// ---------------------------------------------------------------------------
// helpers (local, dependency-free)
// ---------------------------------------------------------------------------

func contains(s, sub string) bool {
	return len(sub) == 0 || indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func tailStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
