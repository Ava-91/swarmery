package dispatch

import (
	"bytes"
	"context"
	"log"
	"os/exec"
	"strings"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/procgroup"
)

// RunSpec is one dispatched headless run.
type RunSpec struct {
	Prompt      string // full prompt (task body + execution contract)
	SessionUUID string // daemon-generated; passed as --session-id (explicit link)
	Cwd         string // the task's worktree path — the process runs here
	Model       string // optional --model override ("" = account default)
	Agent       string // optional registry agent name ("" = plain run, no mention)
}

// Run is the outcome of a completed dispatched process.
type Run struct {
	SessionUUID string        // echoed back for the task↔session link
	ExitCode    int           // process exit status (0 = clean; -1 = never started)
	TimedOut    bool          // true if RunTimeout fired (ctx deadline)
	Stderr      string        // tail of stderr, surfaced in dispatch_error on failure
	Duration    time.Duration // wall-clock spawn→exit
}

// Runner is the headless-claude boundary. ClaudeRunner is production; tests
// substitute a stub that returns a canned Run without spawning a process
// (mirroring improve.Runner / provision.Runner). Start BLOCKS until the process
// exits — the dispatcher calls it inside its own goroutine (the async seam is
// the goroutine, not the Runner), which keeps exit + sentinel handling in one
// place and makes the whole flow stub-testable.
type Runner interface {
	Start(ctx context.Context, spec RunSpec) (*Run, error)
}

// stderrTailBytes caps captured stderr landing in dispatch_error.
const stderrTailBytes = 4096

// drainGrace bounds the post-exit wait for the run's process group to empty out.
const drainGrace = 5 * time.Second

// defaultModel pins dispatched implementation runs whose task carries no model
// override: an unset --model inherits the account default (Fable-5 here — 5×
// the Sonnet price). Executor tier — full ID, not an alias, because aliases
// re-resolve over time.
const defaultModel = "claude-sonnet-5"

// ClaudeRunner spawns `claude -p <prompt> --session-id <uuid> [--model <m>]`
// with cwd set to the worktree. Binary resolution is a plain PATH lookup — the
// same pattern as improve.ClaudeRunner and internal/toolproc (the daemon's
// launchd/service PATH must contain the claude binary). The prompt is passed as
// an argument (not stdin) so --session-id positioning is unambiguous.
type ClaudeRunner struct{}

// agentPrompt is the ONE place a dispatched run's agent mention is applied. A
// board task carrying tasks.agent runs AS that registry agent, and the mention
// is the convention the rest of the product already speaks: Agent Hub's "Run
// now" composes the same "@<name>: " prefix for the board's ?compose= deep-link
// (web/src/pages/agent-hub/RunNow.tsx), so a dispatched run and a hand-composed
// one reach the same agent by the same route.
//
// The prefix lives HERE and not in the service on purpose: the service stays a
// pure field-mapper (it copies tasks.agent into the spec and nothing else), so
// there is exactly one code path that can prefix and double-prefixing is not
// expressible. TrimSpace matches the Model handling below — a whitespace-only
// value is no value, never a "@ : " mention.
//
// No stripping of an existing leading mention, deliberately: a prompt seeded by
// Agent Hub's ?compose= deep-link already carries "@<name>: " in its text, and a
// card that ALSO sets tasks.agent to that same name would spawn
// "@tech-lead: @tech-lead: ...". That is redundant, not wrong — both mentions
// name the same agent, so routing is unaffected — and today no UI sets both on
// one task. A New-Task modal that prefills the agent picker FROM a compose link
// would make it reachable; if that lands, dedupe at the point the modal fills
// the picker (drop the mention from the prompt text once it owns the field),
// not here, so this stays the one unconditional prefix site.
//
// No registry validation here, also on purpose: names are checked against the
// registry when the card is written (POST/PATCH), and the registry is re-scanned
// from disk, so an agent deleted between write and dispatch would fail a
// dispatcher-side check for no gain. An unknown mention simply does not route in
// Claude Code and the run proceeds on the plain prompt — a degraded run beats a
// refused one.
func agentPrompt(spec RunSpec) string {
	agent := strings.TrimSpace(spec.Agent)
	if agent == "" {
		return spec.Prompt
	}
	return "@" + agent + ": " + spec.Prompt
}

func (ClaudeRunner) Start(ctx context.Context, spec RunSpec) (*Run, error) {
	// --setting-sources project,local: skip user-level settings (global plugin
	// stack) — headless runs don't need them; project plugins and OAuth are
	// unaffected.
	args := []string{"-p", agentPrompt(spec), "--session-id", spec.SessionUUID,
		"--setting-sources", "project,local"}
	if m := strings.TrimSpace(spec.Model); m != "" {
		args = append(args, "--model", m)
	}
	start := time.Now()
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = spec.Cwd
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	// Own process group: a timeout or cancel must reach the executor's whole tree
	// (shells, tools, sub-agents), and Wait must not block on a pipe an orphan
	// still holds.
	procgroup.Isolate(cmd, 0)
	// stdout is the assistant text; we do NOT parse it here — the run's transcript
	// is ingested independently and the dispatcher reads the linked session's
	// last assistant turn from the DB for sentinels. Discard it.
	err := cmd.Run()
	elapsed := time.Since(start) // the run's own wall clock — the drain is teardown

	// Wait only guarantees the leader is reaped; a survivor would keep writing into
	// a worktree the dispatcher may hand to the next stage or reclaim.
	if cmd.Process != nil && procgroup.Drain(cmd.Process.Pid, drainGrace) {
		log.Printf("warning: dispatch: uuid=%s left processes behind; killed the run's process group", spec.SessionUUID)
	}

	run := &Run{
		SessionUUID: spec.SessionUUID,
		Stderr:      tail(stderr.String(), stderrTailBytes),
		Duration:    elapsed,
	}
	if ctx.Err() == context.DeadlineExceeded {
		run.TimedOut = true
		run.ExitCode = -1
		return run, nil // a timeout is an outcome, not a Start error
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			run.ExitCode = ee.ExitCode()
			return run, nil // nonzero exit is an outcome the dispatcher routes
		}
		// The process could not be started/observed at all (PATH miss, fork
		// failure). That IS a Start error — the dispatcher marks the row and
		// releases the slot.
		run.ExitCode = -1
		return run, err
	}
	run.ExitCode = 0
	return run, nil
}

// tail returns the last <= n bytes of s, trimmed.
func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = s[len(s)-n:]
	}
	return s
}
