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
