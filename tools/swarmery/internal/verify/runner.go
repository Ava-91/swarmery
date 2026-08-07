package verify

import (
	"bytes"
	"context"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/claudeacct"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/procgroup"
)

// RunSpec is one bounded read-only verifier run.
type RunSpec struct {
	Prompt      string // the read-only verifier prompt (BuildPrompt output)
	SessionUUID string // daemon-generated; passed as --session-id (explicit link)
	Cwd         string // the task's worktree path — the process runs here
	Model       string // optional --model override ("" = account default at the spawn layer; the service fills defaultModel before building the spec)

	// Account is the Claude Code account key this run must execute under,
	// resolved by the CALLER from the task's PROJECT — never from Cwd. Cwd is a
	// worktree, which carries no project settings file, so resolving it here
	// would silently fall back to the default account (plan A3).
	// "" means the default account and produces no env delta.
	Account string
}

// Run is the outcome of a completed verifier process. Unlike the dispatcher,
// verification READS the model's stdout — the verdict lives in the transcript —
// so Output carries the captured stdout for the parser.
type Run struct {
	Output      string        // captured stdout (the verifier's reasoning + VERDICT line)
	ExitCode    int           // process exit status (0 = clean; -1 = never started / timeout)
	TimedOut    bool          // true if the hard timeout fired (ctx deadline)
	Stderr      string        // tail of stderr, for the detail on an error
	Duration    time.Duration // wall-clock spawn→exit
}

// Runner is the headless-claude boundary for verification. ClaudeRunner is
// production; tests substitute a stub that returns a canned Run without spawning
// a process (mirroring dispatch.Runner / improve.Runner / provision.Runner).
// Run BLOCKS until the process exits — the service calls it inside its own
// goroutine, keeping parse + stamp in one place and the whole flow
// stub-testable. A timeout is an OUTCOME (TimedOut=true), not an error — the
// service maps it to INCONCLUSIVE.
type Runner interface {
	Run(ctx context.Context, spec RunSpec) (*Run, error)
}

// claudeTimeout is the hard wall-clock bound for one verification run (phase-6
// spec: 15 minutes). Overridable via SWARMERY_VERIFY_TIMEOUT_MIN at the service
// layer; ClaudeRunner uses this constant when the spec carries no ctx deadline.
const claudeTimeout = 15 * time.Minute

// drainGrace bounds the post-exit wait for the run's process group to empty out.
const drainGrace = 5 * time.Second

// stderrTailBytes caps captured stderr landing in verify_detail on an error.
const stderrTailBytes = 4096

// defaultModel pins verifier runs whose task carries no model override: an
// unset --model inherits the account default (Fable-5 here — 2× the Opus
// price). Full ID, not an alias — aliases re-resolve over time.
const defaultModel = "claude-opus-5"

// ClaudeRunner spawns `claude -p <prompt> --session-id <uuid> [--model <m>]`
// with cwd set to the worktree. Binary resolution is a plain PATH lookup — the
// same pattern as dispatch.ClaudeRunner / internal/toolproc (the daemon's
// launchd/service PATH must contain the claude binary). The prompt is passed as
// an argument (not stdin) so --session-id positioning is unambiguous, matching
// the dispatcher. NOTE: read-only-ness is enforced by the PROMPT contract, not
// by a sandbox — the security review must confirm the run cannot mutate the
// worktree in a way that would corrupt the graded diff (it runs in the task's
// own throwaway worktree, so at worst it dirties that worktree, never main).
type ClaudeRunner struct {
	// Timeout overrides claudeTimeout when > 0 (tests shrink it; the service
	// sets it from SWARMERY_VERIFY_TIMEOUT_MIN).
	Timeout time.Duration
}

func (r ClaudeRunner) Run(ctx context.Context, spec RunSpec) (*Run, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = claudeTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// --setting-sources project,local: skip user-level settings (global plugin
	// stack) — headless runs don't need them; project plugins and OAuth are
	// unaffected.
	args := []string{"-p", spec.Prompt, "--session-id", spec.SessionUUID,
		"--setting-sources", "project,local"}
	if m := strings.TrimSpace(spec.Model); m != "" {
		args = append(args, "--model", m)
	}
	start := time.Now()
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = spec.Cwd
	// Account comes from the SPEC, not from cmd.Dir: cmd.Dir is the task's
	// worktree, which has no .claude/settings.local.json of its own, so
	// claudeacct.EnvFor(cmd.Dir) here would resolve nothing and silently verify
	// under the default account (plan A3). The service resolves the binding from
	// the project path and puts the key in spec.Account.
	// EnvForAccount("") returns nil, so an unbound project's cmd.Env is a
	// byte-identical copy of os.Environ() — behaviour unchanged from before.
	cmd.Env = append(os.Environ(), claudeacct.EnvForAccount(spec.Account)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Own process group: the hard timeout must take the verifier's whole tree, and
	// Wait must not block on a pipe a surviving descendant still holds — which for
	// this runner would also mean stdout never closing, i.e. no verdict at all.
	procgroup.Isolate(cmd, 0)
	err := cmd.Run()
	elapsed := time.Since(start) // the run's own wall clock — the drain is teardown

	if cmd.Process != nil && procgroup.Drain(cmd.Process.Pid, drainGrace) {
		log.Printf("warning: verify: uuid=%s left processes behind; killed the run's process group", spec.SessionUUID)
	}

	run := &Run{
		Output:   stdout.String(),
		Stderr:   tail(stderr.String(), stderrTailBytes),
		Duration: elapsed,
	}
	if ctx.Err() == context.DeadlineExceeded {
		run.TimedOut = true
		run.ExitCode = -1
		return run, nil // a timeout is an outcome (→ INCONCLUSIVE), not a Start error
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			run.ExitCode = ee.ExitCode()
			return run, nil // nonzero exit is an outcome the service routes (still parse stdout)
		}
		// The process could not be started/observed at all (PATH miss, fork
		// failure). That IS an error — the service maps it to INCONCLUSIVE.
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
