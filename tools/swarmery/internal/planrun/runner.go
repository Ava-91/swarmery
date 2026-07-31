package planrun

// Runner is the headless-claude boundary for a plan run. ClaudeRunner is
// production; tests substitute a stub that returns without spawning a process
// (mirrors planning.Runner / phaserun.Runner). Start BLOCKS until the process
// exits — the service calls it inside its own goroutine (the async seam is the
// goroutine, not the Runner), which keeps exit handling and single-flight
// release in one place and makes the flow stub-testable.
//
// Knobs (all optional):
//   - SWARMERY_PLANRUN_AGENT   default --agent when the caller names none
//     (falls back to "tech-lead"). The UI's agent picker overrides it per run.
//   - SWARMERY_PLANRUN_MODEL   passed as --model verbatim; unset ⇒ the account
//     default. Pin full model IDs, not aliases — aliases re-resolve over time.
//   - SWARMERY_PLANRUN_TIMEOUT Go duration bounding one plan run (default 8h).
//     A whole plan is many phases of real work, so it gets far more room than a
//     single phase (SWARMERY_PHASERUN_TIMEOUT, default 4h) — but it must not
//     wedge a worktree forever.
//
// Binary resolution reuses planning.ClaudeBin (SWARMERY_CLAUDE_BIN override →
// PATH → common install locations), so the spawn works under launchd's minimal
// PATH exactly like the planner's.

import (
	"bytes"
	"context"
	"log"
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

// RunSpec is one dispatched plan run.
type RunSpec struct {
	Prompt      string // full plan-run prompt (contract + README + phase manifest)
	SessionUUID string // daemon-generated; passed as --session-id (explicit link)
	Cwd         string // the acquired worktree path — the process runs here
	Agent       string // --agent; "" ⇒ omitted (the session's default agent)
	// SettingsFile is passed as --settings when non-empty: the project's
	// .claude/settings.json, lent to a run whose worktree cannot discover it
	// (see repopath.InheritedSettings). It carries the project's enabled plugins,
	// permissions and additionalDirectories — without it a multi-repo run has no
	// core@swarmery, and the default agent does not exist.
	SettingsFile string
}

// Run is the outcome of a completed plan-run process.
type Run struct {
	SessionUUID string        // echoed back for the plan↔session link
	ExitCode    int           // process exit status (0 = clean; -1 = never started)
	TimedOut    bool          // true if the ctx deadline fired
	Stderr      string        // tail of stderr, surfaced in run_error on failure
	Duration    time.Duration // wall-clock spawn→exit
}

// Env knobs — see the file header.
const (
	agentEnv   = "SWARMERY_PLANRUN_AGENT"
	modelEnv   = "SWARMERY_PLANRUN_MODEL"
	timeoutEnv = "SWARMERY_PLANRUN_TIMEOUT"
)

// fallbackAgent is the orchestrating agent a plan run is handed to when neither
// the caller nor the environment names one. tech-lead is core's routing agent —
// the one whose job description already is "take a plan and get it executed".
const fallbackAgent = "tech-lead"

// planRunTimeout bounds one plan execution when SWARMERY_PLANRUN_TIMEOUT is
// unset or unparseable.
const planRunTimeout = 8 * time.Hour

// stderrTailBytes caps captured stderr landing in run_error.
const stderrTailBytes = 4096

// DefaultAgent is the agent a plan run uses when the caller names none: the
// SWARMERY_PLANRUN_AGENT override, else fallbackAgent. Exported so the API can
// tell the UI which agent an un-picked "Run plan" will actually use.
func DefaultAgent() string {
	if a := strings.TrimSpace(os.Getenv(agentEnv)); a != "" {
		return a
	}
	return fallbackAgent
}

// runTimeout resolves the configured plan-run window.
func runTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv(timeoutEnv))
	if raw == "" {
		return planRunTimeout
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		log.Printf("warning: planrun: ignoring invalid %s=%q: %v", timeoutEnv, raw, err)
		return planRunTimeout
	}
	return d
}

// ClaudeRunner spawns `claude -p <prompt> --session-id <uuid> [--agent <a>]
// [--model <m>]` with cwd set to the worktree. The prompt is passed as an
// argument (not stdin) so --session-id positioning is unambiguous (same as
// dispatch/phaserun).
type ClaudeRunner struct {
	// Timeout overrides the env/default window when > 0 (tests shrink it).
	Timeout time.Duration
}

func (r ClaudeRunner) Start(ctx context.Context, spec RunSpec) (*Run, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = runTimeout()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	bin, err := planning.ClaudeBin()
	if err != nil {
		return &Run{SessionUUID: spec.SessionUUID, ExitCode: -1}, err
	}

	args := []string{"-p", spec.Prompt, "--session-id", spec.SessionUUID}
	if a := strings.TrimSpace(spec.Agent); a != "" {
		args = append(args, "--agent", a)
	}
	if m := strings.TrimSpace(os.Getenv(modelEnv)); m != "" {
		args = append(args, "--model", m)
	}
	// Before --agent is resolved: the settings file is what enables the plugin the
	// agent ships in.
	if f := strings.TrimSpace(spec.SettingsFile); f != "" {
		args = append(args, "--settings", f)
	}

	start := time.Now()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = spec.Cwd
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	// stdout is the assistant text; we do NOT parse it — the run's transcript is
	// ingested independently and progress flows through the phase docs' checkbox
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
