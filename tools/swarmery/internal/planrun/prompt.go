package planrun

import (
	"fmt"
	"strings"
	"text/template"
)

// Phase is one entry of the plan manifest handed to the executor: enough for it
// to see the shape and skip finished work, WITHOUT inlining the docs. Phase docs
// are long (plans routinely run tens of KB per phase); pasting all of them would
// spend the context window before the first line of work — and the run-plan
// skill deliberately passes phase content to its executors as file references,
// not pasted text.
type Phase struct {
	Seq       int
	Name      string
	DocPath   string // absolute path — the doc lives outside the repo worktree
	Done      int    // ticked acceptance checkboxes
	Total     int    // acceptance checkboxes in the doc
	DependsOn []int  // seqs this phase waits on
}

// complete reports whether the phase needs no further work.
func (p Phase) complete() bool { return p.Total > 0 && p.Done >= p.Total }

// Mode is how the controller executes the phases — the one execution call the
// run-plan skill cannot derive from the phase DAG, and the same fork an
// interactive session asks about before running a plan.
type Mode string

const (
	// ModeAuto leaves the skill's own route triage authoritative (default).
	ModeAuto Mode = "auto"
	// ModeSubagents forces per-phase executor+reviewer dispatch.
	ModeSubagents Mode = "subagents"
	// ModeInline forces the controller to do the work itself in this session.
	ModeInline Mode = "inline"
)

// ValidMode normalizes a caller-supplied mode; anything unknown degrades to
// ModeAuto rather than smuggling a typo into the prompt.
func ValidMode(m string) Mode {
	switch Mode(strings.TrimSpace(m)) {
	case ModeSubagents:
		return ModeSubagents
	case ModeInline:
		return ModeInline
	default:
		return ModeAuto
	}
}

// modeDirective is the extra instruction layered on the skill for a forced
// mode. ModeAuto adds nothing — the skill's triage table stands.
func modeDirective(m Mode) string {
	switch m {
	case ModeSubagents:
		return "\nEXECUTION MODE — subagent-driven (overrides the skill's route choice): dispatch every phase to an implementation subagent, then review each result with a FRESH reviewer subagent before moving on, exactly as the skill's route S/P describe. Do not implement phase work yourself in this session; your job is to control, dispatch, review and integrate.\n"
	case ModeInline:
		return "\nEXECUTION MODE — inline (overrides the skill's route choice): implement the phases YOURSELF in this session. Do not dispatch implementation subagents. Everything else from the skill still holds — the phase order, the per-phase verification commands, the acceptance-criteria ticks and the run ledger.\n"
	default:
		return ""
	}
}

// manifest renders the phase table: one line per phase, finished ones marked so
// the controller skips them instead of re-doing landed work.
func manifest(phases []Phase) string {
	var b strings.Builder
	for _, p := range phases {
		state := "TODO"
		if p.complete() {
			state = "DONE — skip"
		}
		fmt.Fprintf(&b, "- Phase %d [%s] %s (%d/%d criteria", p.Seq, state, p.Name, p.Done, p.Total)
		if len(p.DependsOn) > 0 {
			deps := make([]string, len(p.DependsOn))
			for i, d := range p.DependsOn {
				deps[i] = fmt.Sprintf("%d", d)
			}
			fmt.Fprintf(&b, ", depends on phase %s", strings.Join(deps, ", "))
		}
		fmt.Fprintf(&b, ")\n  doc: %s\n", p.DocPath)
	}
	return b.String()
}

// promptTemplate hands a whole plan to ONE controller session.
//
// It does NOT restate how to execute a plan: core already ships that contract as
// the `run-plan` skill (plugins/core/skills/run-plan/SKILL.md — phase-DAG
// triage, per-phase implement+review loops, worktree reconciliation, the
// run-ledger, checkbox ticking). Re-specifying it here would fork a maintained
// contract into a prompt string that silently rots. So the prompt's whole job is
// to (a) point the session at the plan, (b) invoke that skill, and (c) state the
// constraints the skill cannot know because they come from the RUN's context:
// this session is headless, so nothing may wait on a human.
//
// The headless caveat is the sharp edge. The skill ASK-gates base-branch pulls,
// pushes and commits, and routes `manual_legs` steps to the main session — all
// of which assume a human is watching. Under `-p` there is nobody to ask, so the
// prompt converts those into explicit deferrals plus a stop-and-report rule,
// rather than letting the run silently hang or improvise past a gate.
//
// The second run-context fact the skill cannot know: ENDING THE TURN ENDS THE
// PROCESS. Interactively, dispatching background executors and replying "waiting
// on them" is correct — a task notification re-invokes the controller when a child
// finishes. Under `-p` there is no re-invocation: the reply terminates `claude`,
// every child dies with it, and the exit code is 0, so the daemon records a clean
// `done` over work that never landed. That is not hypothetical — plan 70 burned
// 13m24s exactly this way on 2026-07-30 (both executor transcripts stop at the
// parent's final second), and the green chip claimed success. Hence the
// await-your-children rule below.
//
// text/template so paths and content interpolate without any prompt-side format
// bug (idiom of planning/prompt.go, phaserun/prompt.go).
var promptTemplate = template.Must(template.New("planrun").Parse(
	`You are the controller for an ENTIRE approved implementation plan, running HEADLESSLY (claude -p) in an isolated git worktree of the project repo (your cwd).

Execute this plan using the ` + "`run-plan`" + ` skill — invoke it with the Skill tool (skill: run-plan) and this plan directory as its argument. That skill IS your procedure: it parses the phase DAG, picks the execution route, dispatches per-phase implement+review loops, ticks acceptance criteria in the phase docs, and keeps the run ledger. Follow it; do not invent a different procedure.

PLAN DIRECTORY: {{.PlanDir}}
{{.ModeDirective}}
Constraints this run adds on top of the skill, because THERE IS NO HUMAN in this session — nothing can be ASK-gated:
- Work only in your cwd worktree. Do NOT touch the operator's main checkout.
- Commit per phase, in the worktree, with conventional commits. Do NOT push, do NOT open PRs, do NOT merge into the default branch, do NOT pull the base branch.
- Any step the skill routes to the main session as a manual leg (browser checks, live-environment probes, anything interactive) is DEFERRED: note it in the ledger as DEFERRED with the reason and carry on with the automated part.
- If you reach a decision that genuinely needs a human — an ASK gate you cannot defer, a destructive operation, or a phase whose premises contradict the code you find — STOP there and end your reply with: PLAN BLOCKED at phase <n>: <one-line reason>. Do not improvise a different design to get past it.
- ENDING YOUR TURN ENDS THIS PROCESS, and every subagent still running dies with it — while the exit code stays 0, so the run is recorded as a clean success that landed nothing. Never dispatch executors and then reply that you are waiting on them: that reply IS the kill. Await every subagent you dispatch inside the same turn (poll it to completion), and only finish your reply once no dispatched work is still in flight and its results are written to disk. If you cannot await them, do the phase work inline instead.
- Tick each acceptance criterion in its phase doc as you satisfy it, not in one batch at the end. Those ticks are the ONLY progress signal the operator can see while you run.
- When every phase is finished, end with: PLAN DONE.

PHASE MANIFEST (current state — phases marked DONE are already landed; skip them):
{{.Manifest}}
PLAN README:
----------------------------------------
{{.Readme}}
----------------------------------------`))

// BuildPrompt renders the plan-run prompt. Template execution on a fixed
// template with string data cannot fail, so the (unreachable) error is ignored
// (same posture as planning.BuildPrompt / phaserun.BuildPrompt).
func BuildPrompt(planDir, readme string, phases []Phase, mode Mode) string {
	var b strings.Builder
	_ = promptTemplate.Execute(&b, struct {
		PlanDir       string
		ModeDirective string
		Manifest      string
		Readme        string
	}{planDir, modeDirective(mode), manifest(phases), readme})
	return b.String()
}
