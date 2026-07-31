package planning

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/procgroup"
)

// Runner is the headless-claude boundary for a planner run. ClaudeRunner is
// production; tests substitute a stub that returns without spawning a process
// (mirrors improve.Runner / dispatch.Runner / routines.Runner). Start BLOCKS
// until the process exits — the service calls it inside its own goroutine (the
// async seam is the goroutine, not the Runner), which keeps exit handling and
// single-flight release in one place and makes the flow stub-testable.
type Runner interface {
	Start(ctx context.Context, spec RunSpec) (*Run, error)
}

// RunSpec is one dispatched planner run.
type RunSpec struct {
	Prompt      string // full planner prompt (idea + instructions)
	SessionUUID string // daemon-generated; passed as --session-id (explicit link)
	Cwd         string // the project path — the process runs here (hooks active)
}

// Run is the outcome of a completed planner process.
type Run struct {
	SessionUUID string        // echoed back for the task↔session link
	ExitCode    int           // process exit status (0 = clean; -1 = never started)
	TimedOut    bool          // true if the ctx deadline fired
	Stderr      string        // tail of stderr, surfaced on failure
	Duration    time.Duration // wall-clock spawn→exit
}

// planTimeout bounds one planner run (a planner may think + ask + write a plan
// dir, which is longer than a mechanical run but must not wedge a slot forever).
const planTimeout = 20 * time.Minute

// drainGrace bounds the post-exit wait for the run's process group to empty out.
const drainGrace = 5 * time.Second

// stderrTailBytes caps captured stderr landing in the run error.
const stderrTailBytes = 4096

// defaultModel pins planner runs: without --model the CLI inherits the account
// default (Fable-5 here — 2× the Opus price). Full ID, not an alias — aliases
// re-resolve over time.
const defaultModel = "claude-opus-5"

// ClaudeRunner spawns `claude -p <prompt> --session-id <uuid>` with cwd set to
// the project path. Binary resolution mirrors session_message.go's claudeBin:
// launchd starts the daemon with a minimal PATH that omits npm/homebrew, so a
// bare LookPath can miss — an explicit SWARMERY_CLAUDE_BIN override, then PATH,
// then the common install locations. The prompt is passed as an argument (not
// stdin) so --session-id positioning is unambiguous (same as dispatch).
type ClaudeRunner struct {
	// Timeout overrides planTimeout when > 0 (tests shrink it).
	Timeout time.Duration
	// Model overrides defaultModel when non-empty.
	Model string
}

func (r ClaudeRunner) Start(ctx context.Context, spec RunSpec) (*Run, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = planTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	bin, err := ClaudeBin()
	if err != nil {
		return &Run{SessionUUID: spec.SessionUUID, ExitCode: -1}, err
	}

	start := time.Now()
	model := r.Model
	if model == "" {
		model = defaultModel
	}
	cmd := exec.CommandContext(ctx, bin, "-p", spec.Prompt, "--session-id", spec.SessionUUID, "--model", model)
	cmd.Dir = spec.Cwd
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	// Own process group — survive daemon restarts (same rationale as the session
	// resume spawn in api/session_message.go). procgroup adds the half a bare
	// Setpgid was missing: cancellation and the plan timeout now reach the whole
	// tree instead of the leader alone, and Wait cannot hang on a pipe an orphan
	// still holds.
	procgroup.Isolate(cmd, 0)
	// stdout is the assistant text; we do NOT parse it — the planner's transcript
	// is ingested independently and the page reads the linked session's turns from
	// the DB. Discard it.
	runErr := cmd.Run()
	elapsed := time.Since(start) // the run's own wall clock — the drain is teardown

	if cmd.Process != nil && procgroup.Drain(cmd.Process.Pid, drainGrace) {
		log.Printf("warning: planning: uuid=%s left processes behind; killed the run's process group", spec.SessionUUID)
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
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			run.ExitCode = ee.ExitCode()
			return run, nil // a nonzero exit is an outcome the service logs
		}
		// The process could not be started/observed at all (fork failure).
		run.ExitCode = -1
		return run, runErr
	}
	run.ExitCode = 0
	return run, nil
}

// ClaudeBin resolves the Claude Code executable, mirroring the
// session_message.go resolution order so the planner spawn works under launchd's
// minimal PATH: explicit SWARMERY_CLAUDE_BIN override → PATH lookup → probe the
// common install locations. Exported so tests can assert the resolution and the
// service can surface a clear "binary missing" error before spawning.
func ClaudeBin() (string, error) {
	if v := strings.TrimSpace(os.Getenv("SWARMERY_CLAUDE_BIN")); v != "" {
		return v, nil
	}
	if p, err := exec.LookPath("claude"); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	candidates := []string{
		"/opt/homebrew/bin/claude",
		"/usr/local/bin/claude",
		filepath.Join(home, ".claude", "local", "claude"),
		filepath.Join(home, ".local", "bin", "claude"),
		filepath.Join(home, ".npm-global", "bin", "claude"),
		filepath.Join(home, "bin", "claude"),
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return c, nil
		}
	}
	return "", fmt.Errorf("claude not found in PATH or common install locations")
}

// tail returns the last <= n bytes of s, trimmed.
func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = s[len(s)-n:]
	}
	return s
}
