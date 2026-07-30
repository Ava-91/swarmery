package phaserun

// Runner is the headless-claude boundary for a phase run (interactive planning
// v2 phase 5). ClaudeRunner is production; tests substitute a stub that returns
// without spawning a process (mirrors planning.Runner / dispatch.Runner). Start
// BLOCKS until the process exits — the service calls it inside its own
// goroutine (the async seam is the goroutine, not the Runner), which keeps exit
// handling and single-flight release in one place and makes the flow
// stub-testable.
//
// Model knob (the ONE env knob of this package): phase runs inherit the
// account-default model unless SWARMERY_PHASERUN_MODEL is set, in which case
// its value is passed as --model verbatim (pin full model IDs, not aliases —
// aliases re-resolve over time). Binary resolution reuses planning.ClaudeBin
// (SWARMERY_CLAUDE_BIN override → PATH → common install locations), so the
// spawn works under launchd's minimal PATH exactly like the planner's.

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/planning"
)

// Runner is the spawn seam the service depends on.
type Runner interface {
	Start(ctx context.Context, spec RunSpec) (*Run, error)
}

// RunSpec is one dispatched phase run.
type RunSpec struct {
	Prompt      string // full phase-run prompt (contract + embedded phase doc)
	SessionUUID string // daemon-generated; passed as --session-id (explicit link)
	Cwd         string // the acquired worktree path — the process runs here
	// SettingsFile is passed as --settings when non-empty: the project's
	// .claude/settings.json, lent to a run whose worktree cannot discover it
	// (see repopath.InheritedSettings). It carries the project's enabled plugins,
	// permissions and additionalDirectories.
	SettingsFile string
}

// Run is the outcome of a completed phase-run process.
type Run struct {
	SessionUUID string        // echoed back for the phase↔session link
	ExitCode    int           // process exit status (0 = clean; -1 = never started)
	TimedOut    bool          // true if the ctx deadline fired
	Stderr      string        // tail of stderr, surfaced in run_error on failure
	Duration    time.Duration // wall-clock spawn→exit
}

// phaseRunTimeout bounds one phase execution: a phase implements + tests +
// commits real work, so it gets triple the planner's window, but must not
// wedge a worktree forever.
const phaseRunTimeout = 60 * time.Minute

// stderrTailBytes caps captured stderr landing in run_error.
const stderrTailBytes = 4096

// modelEnv is the single model-override knob (see the file header).
const modelEnv = "SWARMERY_PHASERUN_MODEL"

// ClaudeRunner spawns `claude -p <prompt> --session-id <uuid> [--model <m>]`
// with cwd set to the worktree. The prompt is passed as an argument (not
// stdin) so --session-id positioning is unambiguous (same as dispatch).
type ClaudeRunner struct {
	// Timeout overrides phaseRunTimeout when > 0 (tests shrink it).
	Timeout time.Duration
}

func (r ClaudeRunner) Start(ctx context.Context, spec RunSpec) (*Run, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = phaseRunTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	bin, err := planning.ClaudeBin()
	if err != nil {
		return &Run{SessionUUID: spec.SessionUUID, ExitCode: -1}, err
	}

	args := []string{"-p", spec.Prompt, "--session-id", spec.SessionUUID}
	if m := strings.TrimSpace(os.Getenv(modelEnv)); m != "" {
		args = append(args, "--model", m)
	}
	if f := strings.TrimSpace(spec.SettingsFile); f != "" {
		args = append(args, "--settings", f)
	}

	start := time.Now()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = spec.Cwd
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	// stdout is the assistant text; we do NOT parse it — the run's transcript is
	// ingested independently and progress flows through the phase doc's checkbox
	// ticks (wsingest). Discard it.
	runErr := cmd.Run()

	run := &Run{
		SessionUUID: spec.SessionUUID,
		Stderr:      tail(stderr.String(), stderrTailBytes),
		Duration:    time.Since(start),
	}
	if ctx.Err() == context.DeadlineExceeded {
		run.TimedOut = true
		run.ExitCode = -1
		return run, nil // a timeout is an outcome, not a Start error
	}
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			run.ExitCode = ee.ExitCode()
			return run, nil // a nonzero exit is an outcome the service stamps
		}
		// The process could not be started/observed at all (fork failure).
		run.ExitCode = -1
		return run, runErr
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
