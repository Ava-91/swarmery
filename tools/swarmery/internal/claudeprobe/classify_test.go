package claudeprobe

import "testing"

// The shared classification table: one rule for the probe AND the real
// dispatch/verify runners. Exit-status first, wording second.
func TestClassifyExit(t *testing.T) {
	cases := []struct {
		name       string
		exitCode   int
		output     string
		wantStatus Status
		wantReason string
	}{
		{"clean exit is ready", 0, `{"loggedIn": true}`, StatusReady, ""},
		{"clean exit with empty output is ready", 0, "", StatusReady, ""},
		// Exit-status first: a zero exit is ready even if the output happens
		// to contain a marker-looking string (e.g. the model quoting it).
		{"clean exit outranks marker text", 0, "Not logged in", StatusReady, ""},
		{"auth status JSON no-login", 1, `{"loggedIn": false, "authMethod": "none"}`, StatusNoLogin, ReasonNoLogin},
		// The recorded plain-run line (docs/claude-cli-credential-behaviour.md §1).
		{"plain-run no-login line", 1, "Not logged in · Please run /login", StatusNoLogin, ReasonNoLogin},
		{"marker buried in surrounding output", 1, "some banner\nNot logged in · Please run /login\n", StatusNoLogin, ReasonNoLogin},
		{"exit 1 with unrelated output", 1, "Error: something else entirely", StatusUnknown, ReasonUnrecognised},
		{"exit 1 with empty output", 1, "", StatusUnknown, ReasonUnrecognised},
		{"other nonzero exit", 3, "explosion", StatusUnknown, ReasonUnrecognised},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyExit(tc.exitCode, tc.output)
			if got.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q", got.Status, tc.wantStatus)
			}
			if got.Reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", got.Reason, tc.wantReason)
			}
		})
	}
}
