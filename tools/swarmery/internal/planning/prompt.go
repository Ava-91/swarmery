package planning

import (
	"fmt"
	"strings"
	"text/template"
)

// promptTemplate is the Interactive Planning v2 wizard prompt.  It instructs
// the agent to research the repo first, then run PHASE A (structured interview
// — one JSON question per turn) until it receives the PROCEED instruction,
// and only then write the plan to the private workspace (PHASE B).
//
// The inner ```json fence is part of the prompt text and is preserved using
// backtick-concatenation so the outer Go raw-string literal is not broken.
//
// text/template so {{.Idea}} interpolates without any prompt-side format bug.
var promptTemplate = template.Must(template.New("planner").Parse(
	`You are the swarmery planning assistant, running headlessly to turn a rough idea into a structured, executable plan for THIS project (your current working directory is the project repo).

You are NOT in an interactive terminal: never call the AskUserQuestion tool. The dashboard drives this conversation instead; follow the protocol below EXACTLY.

PHASE A — INTERVIEW (every turn until you receive the PROCEED instruction):
1. Research first. Before your first question, inspect the repository (read-only: list files, read code/docs, git log) so every question is grounded in what actually exists. Cite concrete findings (paths, current behavior) in the question description.
2. Iterative narrowing. Ask EXACTLY ONE question per turn: analyze the idea and prior answers, rebuild the running plan around every decision already made, then ask the single highest-impact next question, one level deeper than the last. Never ask a generic question; never silently pick a direction yourself.
3. Options. Give 2-4 materially distinct options, each with a short label, a description grounded in the repo, and pros/cons where meaningful. ALWAYS include a final option {"id":"other","label":"Other","isOther":true} so the operator can write their own answer.
4. Response format. End EVERY interview turn with exactly one fenced json block and NOTHING after it:

` + "```json" + `
{"type":"question","data":{"id":"kebab-unique-id","type":"single_select","question":"…","description":"…","options":[{"id":"opt-a","label":"…","description":"…","pros":["…"],"cons":["…"]},{"id":"other","label":"Other","isOther":true}],"runningPlan":{"title":"…","description":"…","proposedChanges":["specific change"],"acceptanceCriteria":["observable outcome"],"suggestedSize":"S"}}}
` + "```" + `

   Use "multi_select" when choices are not mutually exclusive. Keep runningPlan present and current on every turn. Text before the block is your visible analysis — keep it brief and concrete. Match the operator's language (the idea below) in question/option prose.

PHASE B — PLAN (only after the operator sends the PROCEED instruction):
- Stop asking questions. Choose the planning agent that fits the scope: @task-planner approach for < ~1 week / <=3 phases, @implementation-planner for larger multi-phase work.
- Write the plan to the PRIVATE WORKSPACE, never into a code repo. The task dir MUST match this exact shape — the dashboard's epic scanner walks ONLY workspace/{working,archive}/<YYYY>/<MM>/<DD>/<slug>/, so a plan saved anywhere else silently never appears on the Plans page:
  <workspace root>/<project>/workspace/working/<YYYY>/<MM>/<DD>/<slug>/plan/README.md
  where <YYYY>/<MM>/<DD> is today's date as three separate directories and <slug> is a lowercase kebab slug WITHOUT a date prefix. NEVER save under workspace/plans/ — that tree is frozen history, even if the directory exists on disk.
- Plan contents per the workspace convention (CLAUDE.md section 11 / core pack): plan/README.md (objective, real file paths, phase sequencing table, risks, Definition of Done) plus phase-N docs, each with a self-contained copy-paste agent prompt, measurable acceptance criteria, and — as the doc's LAST section — an empty ` + "`## Completion Report`" + ` stub for the executor to fill at phase end (the dashboard renders exactly that section as the phase's summary, so a doc without the stub leaves the operator with "no summary of the work written"). Honor every decision and the final running plan from the interview.
- Do NOT implement anything and do NOT create git branches — planning only.
- Finish your FINAL message with this exact line on its own:
  PLAN SAVED: <absolute path to the plan dir>

The user's idea:
{{.Idea}}`))

// BuildPrompt renders the planner prompt for one idea.  The idea is trimmed
// and interpolated verbatim; template execution on a fixed template with
// string data cannot fail, so the (unreachable) error is ignored.
func BuildPrompt(idea string) string {
	var b strings.Builder
	_ = promptTemplate.Execute(&b, struct{ Idea string }{Idea: strings.TrimSpace(idea)})
	return b.String()
}

// BuildAnswerMessage renders the resume message sent to the agent when the
// operator selects an answer for question q.
//
// selected contains the chosen option IDs.  Labels are resolved from
// q.Options; unknown IDs are printed as-is.  If otherText is non-empty it is
// appended as an "Other: …" line.  The message closes with the standard
// instruction to rebuild the running plan and continue the interview.
func BuildAnswerMessage(q PlanningQuestion, selected []string, otherText string) string {
	// Build a label lookup from the question options.
	labelFor := make(map[string]string, len(q.Options))
	for _, o := range q.Options {
		labelFor[o.ID] = o.Label
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Answer to question %q:\n", q.ID)
	for _, id := range selected {
		label := labelFor[id]
		if label == "" {
			label = id
		}
		fmt.Fprintf(&b, "- %s — %s\n", id, label)
	}
	if otherText != "" {
		fmt.Fprintf(&b, "Other: %s\n", otherText)
	}
	b.WriteString("\nRebuild the running plan around this decision and continue the interview per the protocol (one question, fenced json block).")
	return b.String()
}

// BuildRefineMessage renders the resume message for operator refinement
// instructions.  The instructions are quoted verbatim inside a fenced block
// and the agent is told to rebuild affected plan fields and ask exactly one
// next question.
func BuildRefineMessage(instructions string) string {
	return "Refinement instructions:\n\n```\n" + instructions + "\n```\n\n" +
		"Rebuild affected plan fields around all accumulated decisions and these instructions, then ask exactly one next question in that direction, per the protocol."
}

// BuildProceedMessage renders the terminal PROCEED instruction that ends the
// interview and triggers PHASE B (plan writing).
func BuildProceedMessage() string {
	return "PROCEED — write the plan now.\nExecute PHASE B of your instructions."
}
