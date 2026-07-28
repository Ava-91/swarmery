package plugindrift

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// execTimeout bounds one CLI call. `claude plugin list --json` measures ~300 ms
// on a warm machine; 20 s is a stall guard, not a budget.
const execTimeout = 20 * time.Second

// ExecRunner runs the real claude binary.
type ExecRunner struct{ Bin string }

func (r ExecRunner) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.Bin, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("%s %v: %w: %s", r.Bin, args, err, truncate(string(ee.Stderr), 400))
		}
		return nil, fmt.Errorf("%s %v: %w", r.Bin, args, err)
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ResolveBin finds the claude binary. The daemon runs under launchd with a
// stripped PATH, so PATH lookup alone is not enough — see the risk table in
// the plan README. An explicit flag value always wins.
func ResolveBin(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("claude binary %q: %w", explicit, err)
		}
		return explicit, nil
	}
	if p, err := exec.LookPath("claude"); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	for _, cand := range []string{
		filepath.Join(home, ".local", "bin", "claude"),
		"/opt/homebrew/bin/claude",
		"/usr/local/bin/claude",
	} {
		if cand == "" {
			continue
		}
		if _, err := os.Stat(cand); err == nil {
			return cand, nil
		}
	}
	return "", fmt.Errorf("claude binary not found in PATH, ~/.local/bin, /opt/homebrew/bin or /usr/local/bin")
}
