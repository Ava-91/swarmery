// Package ctxhogs attributes a session's context growth to the individual tools
// that produced it. It parses one Claude Code JSONL transcript on demand (no
// store, no ingest) and returns a per-tool token estimate plus the per-turn
// cache-write growth curve — the "why is this session fat" companion to the
// ContextBadge / handoff wave.
//
// The token figure is an ESTIMATE: tool_result payloads are measured by the
// byte length of their raw content JSON and divided by ~4 (a rough
// bytes-per-token heuristic — surfaced verbatim in the UI caveat). It is a
// diagnostic ranking, not an accounting number: real tokenization is
// model-specific and not derivable from the transcript.
//
// The record shapes it reads are documented in docs/jsonl-format.md §4–§6 and
// mirror the lenient structs of internal/ingest/record.go (kept local here so
// this diagnostic path never depends on the ingest pipeline's internals).
package ctxhogs

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"strings"
)

// bytesPerToken is the crude estimate dividing raw tool-result JSON size into a
// token count. Stated in the UI caveat ("~4 bytes/token") so nobody mistakes it
// for a real token accounting.
const bytesPerToken = 4

const (
	// maxLineBytes bounds one scanned line. Transcript tool_results embed whole
	// file contents and multi-megabyte command output on a single line, so the
	// default 64 KiB bufio.Scanner limit would fail with "token too long";
	// mirrors internal/ingest.maxLineBytes.
	maxLineBytes = 64 << 20
	// initialBufBytes is the scanner's starting buffer (grows up to maxLineBytes).
	initialBufBytes = 1 << 20
	// topTools caps the aggregated tool list returned in the report.
	topTools = 20
	// unknownTool buckets tool_results whose tool_use_id had no matching name.
	unknownTool = "(unknown)"
)

// ToolAgg is one tool's contribution to context growth.
type ToolAgg struct {
	Name      string `json:"name"`
	Calls     int    `json:"calls"`
	EstTokens int64  `json:"estTokens"` // Σ len(raw tool_result content JSON)/4 for this tool
}

// TurnGrowth is one assistant API message's cache-write cost — the growth curve.
type TurnGrowth struct {
	Seq        int   `json:"seq"`
	CacheWrite int64 `json:"cacheWrite"` // usage.cache_creation_input_tokens of the assistant record
}

// Report is the full attribution result for one transcript.
type Report struct {
	Tools       []ToolAgg    `json:"tools"`       // sorted by EstTokens DESC, top 20
	Turns       []TurnGrowth `json:"turns"`       // growth curve, file/seq order
	TotalEst    int64        `json:"totalEst"`    // Σ all tool-result estimates (across every tool, not just top 20)
	Uninspected int          `json:"uninspected"` // tool_results whose tool_use id had no matching name
	Malformed   int          `json:"malformed"`   // lines that failed to parse and were skipped
}

// ── lenient record structs (docs/jsonl-format.md) ────────────────────────────

type record struct {
	Type    string          `json:"type"`
	Message json.RawMessage `json:"message"`
}

type apiMessage struct {
	ID      string          `json:"id"`
	Content json.RawMessage `json:"content"` // string (user prompt) or []contentBlock
	Usage   *usage          `json:"usage"`
}

type usage struct {
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`          // tool_use
	Name      string          `json:"name"`        // tool_use
	ToolUseID string          `json:"tool_use_id"` // tool_result
	Content   json.RawMessage `json:"content"`     // tool_result payload (string or []block)
}

// Analyze parses the transcript at path and returns its context-hog report.
// It returns an error only for I/O failures (open/read); malformed individual
// lines are skipped and counted in Report.Malformed, never fatal — mirroring
// internal/ingest.readRecords.
func Analyze(path string) (*Report, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	rep := &Report{}
	// id → tool name, learned from assistant tool_use blocks; resolves the
	// tool_result carrier that arrives on a later user line (§4b/§5).
	toolByID := map[string]string{}
	// name → running aggregate (pointers so map order doesn't matter).
	agg := map[string]*ToolAgg{}
	seq := 0

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, initialBufBytes), maxLineBytes)
	for sc.Scan() {
		trimmed := strings.TrimSpace(sc.Text())
		if trimmed == "" {
			continue
		}
		var r record
		if err := json.Unmarshal([]byte(trimmed), &r); err != nil {
			rep.Malformed++
			continue
		}

		switch r.Type {
		case "assistant":
			var m apiMessage
			if json.Unmarshal(r.Message, &m) != nil {
				rep.Malformed++
				continue
			}
			// Growth curve: one point per API message, cacheWrite from usage.
			var cacheWrite int64
			if m.Usage != nil {
				cacheWrite = m.Usage.CacheCreationInputTokens
			}
			rep.Turns = append(rep.Turns, TurnGrowth{Seq: seq, CacheWrite: cacheWrite})
			seq++
			// Learn id → name for every tool_use block on this line.
			for _, b := range decodeBlocks(m.Content) {
				if b.Type == "tool_use" && b.ID != "" {
					toolByID[b.ID] = b.Name
				}
			}

		case "user":
			var m apiMessage
			if json.Unmarshal(r.Message, &m) != nil {
				rep.Malformed++
				continue
			}
			for _, b := range decodeBlocks(m.Content) {
				if b.Type != "tool_result" {
					continue
				}
				name, ok := toolByID[b.ToolUseID]
				if !ok || name == "" {
					name = unknownTool
					rep.Uninspected++
				}
				// size = byte length of the raw content JSON (§4b). A missing
				// content field is len 0 → contributes nothing.
				est := int64(len(b.Content)) / bytesPerToken
				a := agg[name]
				if a == nil {
					a = &ToolAgg{Name: name}
					agg[name] = a
				}
				a.Calls++
				a.EstTokens += est
				rep.TotalEst += est
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	rep.Tools = topByTokens(agg)
	return rep, nil
}

// decodeBlocks returns the []contentBlock of a message content field, or nil
// when content is a plain string (user prompt) or otherwise not an array.
func decodeBlocks(content json.RawMessage) []contentBlock {
	if len(content) == 0 {
		return nil
	}
	var blocks []contentBlock
	if json.Unmarshal(content, &blocks) != nil {
		return nil // string content (§4a) or an unexpected shape — no blocks
	}
	return blocks
}

// topByTokens flattens the aggregate map into a slice sorted by EstTokens DESC
// (ties broken by Calls DESC, then Name ASC for a stable order), capped at
// topTools.
func topByTokens(agg map[string]*ToolAgg) []ToolAgg {
	tools := make([]ToolAgg, 0, len(agg))
	for _, a := range agg {
		tools = append(tools, *a)
	}
	sort.Slice(tools, func(i, j int) bool {
		if tools[i].EstTokens != tools[j].EstTokens {
			return tools[i].EstTokens > tools[j].EstTokens
		}
		if tools[i].Calls != tools[j].Calls {
			return tools[i].Calls > tools[j].Calls
		}
		return tools[i].Name < tools[j].Name
	})
	if len(tools) > topTools {
		tools = tools[:topTools]
	}
	return tools
}
