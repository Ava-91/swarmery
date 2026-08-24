package trajeval

import "testing"

func TestDetectSearchLoop(t *testing.T) {
	// 4 consecutive identical tool calls with no progress event => one finding.
	evs := []event{
		{turnID: 1, typ: "tool_call", tool: "Grep"},
		{turnID: 2, typ: "tool_call", tool: "Grep"},
		{turnID: 3, typ: "tool_call", tool: "Grep"},
		{turnID: 4, typ: "tool_call", tool: "Grep"},
	}
	got := detectSearchLoop(evs)
	if got == nil {
		t.Fatal("expected a search-loop finding, got nil")
	}
	if got.kind != "search-loop" {
		t.Errorf("kind = %q, want search-loop", got.kind)
	}

	// A progress event breaks the run => no finding.
	broken := []event{
		{turnID: 1, typ: "tool_call", tool: "Grep"},
		{turnID: 2, typ: "tool_call", tool: "Grep"},
		{turnID: 3, typ: "file_change", tool: ""},
		{turnID: 4, typ: "tool_call", tool: "Grep"},
		{turnID: 5, typ: "tool_call", tool: "Grep"},
	}
	if f := detectSearchLoop(broken); f != nil {
		t.Errorf("expected nil (run broken by file_change), got %+v", f)
	}

	// 4 same-tool calls then a different tool => finding fires on the switch.
	switchTriggered := []event{
		{turnID: 1, typ: "tool_call", tool: "Grep"},
		{turnID: 2, typ: "tool_call", tool: "Grep"},
		{turnID: 3, typ: "tool_call", tool: "Grep"},
		{turnID: 4, typ: "tool_call", tool: "Grep"},
		{turnID: 5, typ: "tool_call", tool: "Read"},
	}
	if f := detectSearchLoop(switchTriggered); f == nil {
		t.Error("expected search-loop finding on tool switch, got nil")
	}

	emptyTool := []event{
		{turnID: 1, typ: "tool_call", tool: ""},
		{turnID: 2, typ: "tool_call", tool: ""},
		{turnID: 3, typ: "tool_call", tool: ""},
		{turnID: 4, typ: "tool_call", tool: ""},
	}
	if f := detectSearchLoop(emptyTool); f != nil {
		t.Errorf("expected nil for empty-tool calls, got %+v", f)
	}
}

// The metric has to mean what its name says. On the live corpus the old
// detector accepted ANY tool name, and 83% of its 557 findings were runs of
// Bash while 3% were runs of Edit — building and editing, recorded as
// searching. These cases pin the tools a "search loop" may be made of.
func TestSearchLoopIsAboutSearching(t *testing.T) {
	run := func(tool string, n int) []event {
		evs := make([]event, 0, n)
		for i := 0; i < n; i++ {
			evs = append(evs, event{turnID: int64(i + 1), typ: "tool_call", tool: tool})
		}
		return evs
	}

	// Doing, not looking — never a search loop however long the run.
	for _, tool := range []string{"Bash", "Edit", "MultiEdit", "Write", "NotebookEdit", "TaskCreate", "WebSearchUnknownTool"} {
		if f := detectSearchLoop(run(tool, 8)); f != nil {
			t.Errorf("%s ×8 reported a search loop: %+v", tool, f)
		}
	}

	// Looking, repeatedly, with nothing to show for it — still a finding.
	for _, tool := range []string{"Grep", "Glob", "Read", "NotebookRead", "WebSearch", "WebFetch"} {
		if f := detectSearchLoop(run(tool, 4)); f == nil {
			t.Errorf("%s ×4 with no progress should be a search loop", tool)
		}
	}

	// The guard shipped alongside this asks agents for ONE operation per Bash
	// call, so consecutive Bash calls go UP by design. Interleaving them must
	// not manufacture a loop out of ordinary work.
	interleaved := []event{
		{turnID: 1, typ: "tool_call", tool: "Read"},
		{turnID: 2, typ: "tool_call", tool: "Bash"},
		{turnID: 3, typ: "tool_call", tool: "Read"},
		{turnID: 4, typ: "tool_call", tool: "Bash"},
		{turnID: 5, typ: "tool_call", tool: "Read"},
		{turnID: 6, typ: "tool_call", tool: "Bash"},
		{turnID: 7, typ: "tool_call", tool: "Read"},
	}
	if f := detectSearchLoop(interleaved); f != nil {
		t.Errorf("reading and running commands alternately is not a search loop: %+v", f)
	}

	// An Edit is progress even when ingest emitted no file_change event for it —
	// which is exactly why 15 live findings were runs of Edit.
	editBreaks := []event{
		{turnID: 1, typ: "tool_call", tool: "Read"},
		{turnID: 2, typ: "tool_call", tool: "Read"},
		{turnID: 3, typ: "tool_call", tool: "Edit"},
		{turnID: 4, typ: "tool_call", tool: "Read"},
		{turnID: 5, typ: "tool_call", tool: "Read"},
	}
	if f := detectSearchLoop(editBreaks); f != nil {
		t.Errorf("an Edit between reads is progress, not a loop: %+v", f)
	}

	// A non-search tool ends the run WITHOUT starting one, so a search run that
	// resumes after it starts from zero rather than carrying its history.
	resumed := []event{
		{turnID: 1, typ: "tool_call", tool: "Grep"},
		{turnID: 2, typ: "tool_call", tool: "Grep"},
		{turnID: 3, typ: "tool_call", tool: "Bash"},
		{turnID: 4, typ: "tool_call", tool: "Grep"},
		{turnID: 5, typ: "tool_call", tool: "Grep"},
	}
	if f := detectSearchLoop(resumed); f != nil {
		t.Errorf("2 greps, a build, 2 greps is not a 4-long loop: %+v", f)
	}
}

func TestDetectVerifySkip(t *testing.T) {
	edited := []event{
		{turnID: 1, typ: "file_change"},
		{turnID: 2, typ: "session_end"},
	}
	if f := detectVerifySkip(edited); f == nil || f.kind != "verify-skip" {
		t.Fatalf("expected verify-skip finding, got %+v", f)
	}

	verified := []event{
		{turnID: 1, typ: "file_change"},
		{turnID: 2, typ: "test_run"},
		{turnID: 3, typ: "session_end"},
	}
	if f := detectVerifySkip(verified); f != nil {
		t.Errorf("expected nil (test_run present), got %+v", f)
	}

	noEdits := []event{{turnID: 1, typ: "tool_call", tool: "Read"}}
	if f := detectVerifySkip(noEdits); f != nil {
		t.Errorf("expected nil (no file_change), got %+v", f)
	}
}

func TestFirstPass(t *testing.T) {
	clean := []event{{typ: "file_change"}, {typ: "test_run"}}
	if !firstPass(clean) {
		t.Error("firstPass = false, want true (no error events)")
	}
	errored := []event{{typ: "file_change"}, {typ: "error"}}
	if firstPass(errored) {
		t.Error("firstPass = true, want false (error event present)")
	}
}
