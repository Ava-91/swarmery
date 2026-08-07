// Package provision owns the "enable pack → install + generate" pipeline: a
// mocked Runner (the only seam touching the real claude binary), a pack→action
// policy map, and a Service that enqueues single-flight jobs with durable status.
package provision

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/claudeacct"
)

// Runner executes the claude binary. It is the ONLY seam that touches a real
// process — every test injects a stub. Mirrors internal/improve.Runner.
type Runner interface {
	// Claude runs `claude <args...>` with cwd=dir (dir=="" inherits the daemon
	// cwd), feeding stdin (""=none), and returns trimmed stdout. A non-nil error
	// carries a stderr tail. The daemon's launchd PATH must contain `claude`
	// (already ensured for the serena/graphify tool dashboards).
	Claude(ctx context.Context, dir, stdin string, args ...string) (string, error)
}

// stderrTailBytes caps how much captured stderr lands in the error (and thus in
// provision_jobs.error).
const stderrTailBytes = 4096

// defaultModel pins headless generator runs: without --model the CLI inherits
// the account default (Fable-5 here — 2× the Opus price). Full ID, not an
// alias — aliases re-resolve over time.
const defaultModel = "claude-opus-5"

// ClaudeRunner is the production Runner: a plain PATH lookup of the claude
// binary, the same pattern internal/improve and internal/toolproc use.
type ClaudeRunner struct{}

func (ClaudeRunner) Claude(ctx context.Context, dir, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "claude", args...)
	// Resolving the account from `dir` is correct HERE — and only here and in
	// planning. A provision run's dir IS the project path, so it carries the
	// project's .claude/settings.local.json. dispatch and verify look the same but
	// are not: their cwd is a worktree with no settings file, which is why they
	// take the key from the caller instead (plan A3).
	//
	// dir=="" means "inherit the daemon cwd" and names no project at all —
	// claudeacct.EnvFor("") would probe a RELATIVE .claude/settings.local.json and
	// could bind the run to whatever project the daemon happens to sit in, so that
	// case resolves nothing.
	var acctEnv []string
	if dir != "" {
		cmd.Dir = dir
		acctEnv = claudeacct.EnvFor(dir)
	}
	// nil delta for an unbound project ⇒ a byte-identical copy of os.Environ().
	cmd.Env = append(os.Environ(), acctEnv...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	joined := "claude " + strings.Join(args, " ")
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("%s timed out; stderr: %s", joined, tail(errb.String(), stderrTailBytes))
		}
		return "", fmt.Errorf("%s: %w; stderr: %s", joined, err, tail(errb.String(), stderrTailBytes))
	}
	return strings.TrimSpace(out.String()), nil
}

// tail returns the last ≤ n bytes of s, trimmed.
func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = s[len(s)-n:]
	}
	return s
}
