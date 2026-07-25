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
