// Package claudebin resolves the Claude Code executable for daemon-launched
// subprocesses. launchd starts the daemon with a minimal PATH
// (/usr/bin:/bin:/usr/sbin:/sbin) that omits the npm/homebrew/local install
// dirs, so a bare exec.LookPath("claude") fails under the service even though
// `claude` is on the operator's interactive PATH.
//
// This is the single home of a resolver the repo previously carried twice
// verbatim (planning.ClaudeBin, api.claudeBin); both now delegate here, and
// mcpcfg's `claude mcp …` shell-out uses it. Resolution is driven entirely by
// the environment and the filesystem, so callers stay testable without a real
// binary installed.
package claudebin

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrNotFound reports that no claude executable could be located by any of the
// three strategies. Callers that surface it to a user should mention
// SWARMERY_CLAUDE_BIN — that is the escape hatch.
var ErrNotFound = errors.New("claude not found in PATH or common install locations")

// systemProbeDirs are the machine-wide install dirs probed after PATH, in
// order. A package var rather than an inline literal purely so tests can point
// it at a temp tree and stay hermetic on hosts that genuinely have a claude
// installed in one of them; production always uses these defaults.
var systemProbeDirs = []string{
	"/opt/homebrew/bin",
	"/usr/local/bin",
}

// Resolve returns a path to the claude executable.
// Order: SWARMERY_CLAUDE_BIN override → PATH lookup → probe the common install
// dirs (system dirs first, then the home-relative ones), accepting only a
// non-directory with an executable bit set. Returns ErrNotFound when every
// strategy misses.
func Resolve() (string, error) {
	if v := strings.TrimSpace(os.Getenv("SWARMERY_CLAUDE_BIN")); v != "" {
		return v, nil
	}
	if p, err := exec.LookPath("claude"); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	candidates := make([]string, 0, len(systemProbeDirs)+4)
	for _, dir := range systemProbeDirs {
		candidates = append(candidates, filepath.Join(dir, "claude"))
	}
	candidates = append(candidates,
		filepath.Join(home, ".claude", "local", "claude"),
		filepath.Join(home, ".local", "bin", "claude"),
		filepath.Join(home, ".npm-global", "bin", "claude"),
		filepath.Join(home, "bin", "claude"),
	)
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return c, nil
		}
	}
	return "", ErrNotFound
}
