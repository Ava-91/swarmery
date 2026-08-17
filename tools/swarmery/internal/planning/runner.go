package planning

import (
	"context"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/claudeacct"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/claudebin"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/runcore"
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

// Start maps this engine's RunSpec onto runcore.Spec and its Result back onto
// Run. Everything shared — the argv, the account env merge, the process group,
// the drain, the exit ladder — lives in internal/runcore; what stays here is the
// planner's own policy: its timeout, its model default, and resolving the account
// from cwd.
func (r ClaudeRunner) Start(ctx context.Context, spec RunSpec) (*Run, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = planTimeout
	}
	model := r.Model
	if model == "" {
		model = defaultModel
	}

	res, err := runcore.ClaudeRunner{Engine: "planning"}.Start(ctx, runcore.Spec{
		Prompt:      spec.Prompt,
		SessionUUID: spec.SessionUUID,
		Cwd:         spec.Cwd,
		Model:       model,
		// Resolving the account from cwd is correct HERE — and only here and in
		// provision. A planner run's Cwd is the PROJECT path (see RunSpec.Cwd), so it
		// carries the project's .claude/settings.local.json. dispatch and verify look
		// the same but are not: their Cwd is a worktree with no settings file, which
		// is why they take the key from the caller instead (plan A3).
		// An unbound project resolves to "" and produces no env delta, so cmd.Env is
		// then a byte-identical copy of os.Environ().
		Account: claudeacct.Binding(spec.Cwd),
		Timeout: timeout,
		// launchd starts the daemon with a minimal PATH that omits npm/homebrew, so a
		// bare PATH lookup can miss — ClaudeBin probes the install locations too.
		Bin: ClaudeBin,
	})
	return &Run{
		SessionUUID: res.SessionUUID,
		ExitCode:    res.ExitCode,
		TimedOut:    res.TimedOut,
		Stderr:      res.Stderr,
		Duration:    res.Duration,
	}, err
}

// ClaudeBin resolves the Claude Code executable so the planner spawn works
// under launchd's minimal PATH: explicit SWARMERY_CLAUDE_BIN override → PATH
// lookup → probe the common install locations. Exported so tests can assert the
// resolution and the service can surface a clear "binary missing" error before
// spawning. The logic lives in internal/claudebin, shared with the API layer's
// resume spawn and mcpcfg's `claude mcp …` shell-out.
func ClaudeBin() (string, error) { return claudebin.Resolve() }
