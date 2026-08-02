package extract

import (
	"encoding/json"
	"fmt"
	"strings"
)

// maxTasks caps how many cards ONE extraction may put on the board. The prompt
// asks for ≤10; this enforces it, because a model that ignores the instruction
// would otherwise bury the Triage column under a single click. Overflow is
// reported by the caller (Service.ExtractTasks logs it) — never dropped
// silently.
const maxTasks = 10

// titleLimit matches the board's own title budget (internal/ingest's
// captureTitleLimit / the New-Task modal's field). Enforced here rather than
// left to the model: the prompt asks for it, the parse guarantees it.
const titleLimit = 120

// Task is one extracted card: exactly the two fields the prompt contract asks
// for. Everything else about the row is fixed by taskcap.InsertCapturedTask.
type Task struct {
	Title  string `json:"title"`
	Prompt string `json:"prompt"`
}

// ErrBadOutput wraps every way the model's answer can fail to be the contract.
// The endpoint turns it into a 502 with the detail attached, so an operator
// sees WHY the run produced nothing instead of a silent zero.
type ErrBadOutput struct {
	Detail string
}

func (e *ErrBadOutput) Error() string { return "extract: unusable model output: " + e.Detail }

// parseTasks pulls the task array out of a raw model answer.
//
// Defensive by construction, because the far side is a language model and the
// near side writes to the operator's board:
//
//   - The first fenced block wins; a bare array is accepted too (models drop the
//     fence when the answer is short). Anything else is an error, never a guess.
//   - A JSON value that is not an array of objects is an error — a model that
//     answered in prose must not read as "no tasks found", because the two
//     outcomes call for opposite responses (502 vs. an honest zero).
//   - Items are individually validated and BAD ONES ARE DROPPED, not fatal: an
//     otherwise-good run of five tasks should not be lost to one malformed
//     sixth. The dropped count comes back so the caller can log it.
//
// Returns (tasks, dropped, error). An empty array is a valid answer: (nil, 0, nil).
func parseTasks(raw string) ([]Task, int, error) {
	body := strings.TrimSpace(fencedOrRaw(raw))
	if body == "" {
		return nil, 0, &ErrBadOutput{Detail: "model returned an empty answer"}
	}
	var items []Task
	if err := json.Unmarshal([]byte(body), &items); err != nil {
		return nil, 0, &ErrBadOutput{Detail: fmt.Sprintf("answer is not a JSON array of {title,prompt}: %v", err)}
	}
	var (
		out     []Task
		dropped int
	)
	for _, it := range items {
		title := strings.TrimSpace(strings.Join(strings.Fields(it.Title), " "))
		prompt := strings.TrimSpace(it.Prompt)
		if title == "" || prompt == "" {
			dropped++
			continue
		}
		if len(out) == maxTasks {
			dropped++
			continue
		}
		out = append(out, Task{Title: truncate(title, titleLimit), Prompt: prompt})
	}
	return out, dropped, nil
}

// fencedOrRaw returns the contents of the FIRST ``` fenced block in s, or s
// itself when there is no complete fence. The opening fence's language tag
// (```json) is dropped with the rest of its line.
//
// "First" rather than "last": the prompt asks for one block and nothing else,
// so a second block is chatter after the answer, not a correction of it.
func fencedOrRaw(s string) string {
	open := strings.Index(s, "```")
	if open < 0 {
		return s
	}
	rest := s[open+3:]
	// Drop the remainder of the opening fence's line (the language tag).
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[nl+1:]
	} else {
		// "```json" with no newline after it — there is no block body at all.
		return s
	}
	close := strings.Index(rest, "```")
	if close < 0 {
		// Unterminated fence: the answer was cut off mid-block. Take what there
		// is — json.Unmarshal will reject it with a real parse error, which is a
		// better 502 detail than "no fenced block found".
		return rest
	}
	return rest[:close]
}
