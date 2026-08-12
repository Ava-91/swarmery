package claudeprobe

import "strings"

// noLoginMarkers are the CLI's recorded no-login output shapes, one per
// invocation family: the `auth status` JSON field, and the plain-run line
// (`Not logged in · Please run /login`, printed by `claude -p` under a
// credential-less config dir — docs/claude-cli-credential-behaviour.md §1).
// Fixed matchers — classification never feeds output anywhere else.
var noLoginMarkers = []string{
	`"loggedIn": false`,
	"Not logged in",
}

// ClassifyExit maps a finished `claude` invocation to a Status. It is the SAME
// rule the probe applies to its own child process, exported so real dispatch
// and verification runs can be read as probes rather than duplicating the
// matcher — two copies of this rule are how the daemon starts disagreeing with
// itself about whether an account works.
//
// Exit-status first, wording second: zero exit → ready unconditionally; a
// non-zero exit is no-login only when output carries one of the CLI's recorded
// no-login shapes; everything else is unknown, never ready. output is the
// run's combined tail; it is matched, never stored and never echoed into a
// Reason — Reason is always one of the fixed constants above.
func ClassifyExit(exitCode int, output string) Result {
	if exitCode == 0 {
		return Result{Status: StatusReady}
	}
	for _, marker := range noLoginMarkers {
		if strings.Contains(output, marker) {
			return Result{Status: StatusNoLogin, Reason: ReasonNoLogin}
		}
	}
	return Result{Status: StatusUnknown, Reason: ReasonUnrecognised}
}
