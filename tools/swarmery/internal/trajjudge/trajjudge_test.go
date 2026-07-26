package trajjudge

import (
	"strings"
	"testing"
)

func TestParseJudgment(t *testing.T) {
	valid := `{"end_result":4,"instruction_compliance":5,"pitfalls":3,"tool_calls":4,"review":"solid; one wasted search [t12]"}`
	j, err := parseJudgment(valid)
	if err != nil {
		t.Fatalf("valid: %v", err)
	}
	if j.EndResult != 4 || j.InstructionCompliance != 5 || j.Pitfalls != 3 || j.ToolCalls != 4 {
		t.Errorf("dims = %+v", j)
	}
	if j.Overall < 3.99 || j.Overall > 4.01 { // (4+5+3+4)/4 = 4.0
		t.Errorf("overall = %v, want 4.0", j.Overall)
	}

	// Model often wraps JSON in prose / fences — parse must find the object.
	fenced := "Here is my verdict:\n```json\n" + valid + "\n```\n"
	if _, err := parseJudgment(fenced); err != nil {
		t.Errorf("fenced JSON should parse: %v", err)
	}

	for name, bad := range map[string]string{
		"not json":     "the agent did fine",
		"missing dim":  `{"end_result":4,"instruction_compliance":5,"pitfalls":3}`,
		"out of range": `{"end_result":7,"instruction_compliance":5,"pitfalls":3,"tool_calls":4,"review":"x"}`,
		"empty review": `{"end_result":4,"instruction_compliance":5,"pitfalls":3,"tool_calls":4,"review":""}`,
	} {
		if _, err := parseJudgment(bad); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

func TestSummarizeAndPrompt(t *testing.T) {
	evs := []event{
		{seq: 1, typ: "tool_call", tool: "Grep"},
		{seq: 2, typ: "file_change", tool: ""},
		{seq: 3, typ: "test_run", tool: ""},
		{seq: 4, typ: "error", tool: ""},
	}
	sum := summarizeTrajectory(evs)
	// Ordered, one line per event, references the seq as a turn ref.
	for _, want := range []string{"t1", "tool_call", "Grep", "t3", "test_run", "t4", "error"} {
		if !strings.Contains(sum, want) {
			t.Errorf("summary missing %q:\n%s", want, sum)
		}
	}

	p := buildRubricPrompt(sum)
	for _, want := range []string{
		"end_result", "instruction_compliance", "pitfalls", "tool_calls",
		"JSON", "1", "5", sum,
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}
