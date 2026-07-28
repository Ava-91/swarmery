package ctxhogs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const samplePath = "testdata/ctxhogs_sample.jsonl"

// findTool returns the aggregate for a tool name, or nil.
func findTool(r *Report, name string) *ToolAgg {
	for i := range r.Tools {
		if r.Tools[i].Name == name {
			return &r.Tools[i]
		}
	}
	return nil
}

// TestAnalyzeAggregatesByTool asserts the exact per-tool call counts and token
// estimates from the hand-crafted fixture. The estimate is len(raw content
// JSON)/4; the fixture's tool_result content strings were sized so the raw JSON
// (string incl. its two quote bytes) divides cleanly:
//
//	Bash: "x"*100 → raw 102 → 25 ; "y"*200 → raw 202 → 50   → 2 calls, 75
//	Read: "r"*400 → raw 402 → 100                            → 1 call, 100
//	(unknown): "u"*40 → raw 42 → 10                          → 1 call, 10
func TestAnalyzeAggregatesByTool(t *testing.T) {
	rep, err := Analyze(samplePath)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	// Tools sorted by estTokens DESC: Read(100), Bash(75), (unknown)(10).
	want := []ToolAgg{
		{Name: "Read", Calls: 1, EstTokens: 100},
		{Name: "Bash", Calls: 2, EstTokens: 75},
		{Name: "(unknown)", Calls: 1, EstTokens: 10},
	}
	if len(rep.Tools) != len(want) {
		t.Fatalf("Tools len = %d, want %d (%+v)", len(rep.Tools), len(want), rep.Tools)
	}
	for i, w := range want {
		got := rep.Tools[i]
		if got.Name != w.Name || got.Calls != w.Calls || got.EstTokens != w.EstTokens {
			t.Errorf("Tools[%d] = %+v, want %+v", i, got, w)
		}
	}

	if rep.TotalEst != 185 {
		t.Errorf("TotalEst = %d, want 185", rep.TotalEst)
	}
}

// TestAnalyzeGrowthCurve asserts the per-turn cache-write growth curve: one
// TurnGrowth per assistant API message, seq in file order, cacheWrite from
// usage.cache_creation_input_tokens.
func TestAnalyzeGrowthCurve(t *testing.T) {
	rep, err := Analyze(samplePath)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	want := []TurnGrowth{
		{Seq: 0, CacheWrite: 1000},
		{Seq: 1, CacheWrite: 2000},
	}
	if len(rep.Turns) != len(want) {
		t.Fatalf("Turns len = %d, want %d (%+v)", len(rep.Turns), len(want), rep.Turns)
	}
	for i, w := range want {
		if rep.Turns[i] != w {
			t.Errorf("Turns[%d] = %+v, want %+v", i, rep.Turns[i], w)
		}
	}
}

// TestAnalyzeSkipsMalformedAndCountsUnknown asserts the malformed line is
// skipped (not fatal) and the tool_result whose tool_use_id has no matching
// tool_use is counted under Uninspected + the "(unknown)" tool bucket.
func TestAnalyzeSkipsMalformedAndCountsUnknown(t *testing.T) {
	rep, err := Analyze(samplePath)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if rep.Uninspected != 1 {
		t.Errorf("Uninspected = %d, want 1", rep.Uninspected)
	}
	if rep.Malformed != 1 {
		t.Errorf("Malformed = %d, want 1", rep.Malformed)
	}
	unk := findTool(rep, "(unknown)")
	if unk == nil {
		t.Fatalf("no (unknown) tool bucket; tools = %+v", rep.Tools)
	}
	if unk.Calls != 1 || unk.EstTokens != 10 {
		t.Errorf("(unknown) = %+v, want {Calls:1, EstTokens:10}", *unk)
	}
}

// TestAnalyzeLargeLine proves the scanner buffer survives a multi-MB line — the
// exact failure mode a naive bufio.Scanner hits ("token too long") on real
// transcripts, which embed whole file contents in tool results.
func TestAnalyzeLargeLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.jsonl")

	// A >1 MB tool_result content, with a matching tool_use so it lands on a
	// named bucket (not (unknown)).
	const bigChars = 2 << 20 // ~2 MiB of content
	big := strings.Repeat("Z", bigChars)
	lines := []string{
		`{"type":"assistant","uuid":"a1","timestamp":"2026-07-27T10:00:00.000Z","sessionId":"big",` +
			`"message":{"id":"m1","role":"assistant","content":[{"type":"tool_use","id":"toolu_big","name":"Read"}],` +
			`"usage":{"cache_creation_input_tokens":123}}}`,
		fmt.Sprintf(`{"type":"user","uuid":"u1","timestamp":"2026-07-27T10:00:01.000Z","sessionId":"big",`+
			`"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_big","content":%q}]}}`, big),
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write big fixture: %v", err)
	}

	rep, err := Analyze(path)
	if err != nil {
		t.Fatalf("Analyze big line: %v", err)
	}
	read := findTool(rep, "Read")
	if read == nil {
		t.Fatalf("no Read bucket for the big line; tools = %+v", rep.Tools)
	}
	// raw content JSON = the string plus its two surrounding quotes.
	wantEst := int64(bigChars+2) / 4
	if read.Calls != 1 || read.EstTokens != wantEst {
		t.Errorf("Read big = %+v, want {Calls:1, EstTokens:%d}", *read, wantEst)
	}
	if len(rep.Turns) != 1 || rep.Turns[0].CacheWrite != 123 {
		t.Errorf("Turns = %+v, want one {Seq:0, CacheWrite:123}", rep.Turns)
	}
}

// TestAnalyzeMissingFile surfaces a real read error (not a nil report).
func TestAnalyzeMissingFile(t *testing.T) {
	if _, err := Analyze(filepath.Join(t.TempDir(), "does-not-exist.jsonl")); err == nil {
		t.Fatal("Analyze on a missing file: want error, got nil")
	}
}
