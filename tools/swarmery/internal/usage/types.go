// Package usage resolves the operator's local Claude OAuth credential and reads
// their live Anthropic subscription quotas — the 5-hour session window, the
// weekly window, and the per-model weekly windows — so the dashboard can show
// the real reading instead of a self-estimate.
//
// # Policy note — supersedes the 2026-06 OAuth spike
//
// internal/api/usage.go opens with a spike note declaring this OAuth path
// "credential exfiltration + a fragile private-API dependency, out of policy".
// That decision is deliberately reversed here, and the reasoning is recorded so
// the record stays honest:
//
//   - The daemon reads the operator's OWN credentials on the operator's OWN
//     machine, from the same store the `claude` CLI itself wrote
//     (~/.claude/.credentials.json, or the macOS Keychain item
//     "Claude Code-credentials"). Nothing is harvested from anyone else.
//   - The bearer token is sent ONLY to Anthropic's own API — the exact call
//     `claude /usage` makes. It is never persisted to SQLite, never logged,
//     never written back to disk or the keychain, and never included in any
//     HTTP response body served by the daemon. Creds.String redacts so an
//     accidental %v is safe, and upstream error bodies are scrubbed of bearer
//     material and truncated before they can reach Provider.Error.
//   - The endpoint is undocumented and may break. That is accepted and
//     contained: Client.Fetch never returns an error — every failure mode
//     degrades to a visible per-provider error card, never to a crash and never
//     to a fabricated number. Every parse is tolerant (multiple key fallbacks
//     plus a generic limits[] walk) so a field rename degrades one row, not the
//     endpoint.
//   - SWARMERY_USAGE_OAUTH=0 is an explicit opt-out: LoadCreds then returns
//     ErrDisabled without touching the filesystem or the keychain at all.
//
// Fusion's node-pty fallback (driving the `claude` TUI and scraping its /usage
// screen when the API path fails) is deliberately NOT ported: Go has no
// equivalent in this tree, and a 60-second PTY spawn per refresh is the wrong
// trade for a dashboard poll. Loss of the OAuth path degrades to an error card,
// not to a scrape.
package usage

// Provider status values. Fetch encodes every outcome in Provider.Status rather
// than returning an error, so one broken provider can never break the endpoint.
const (
	StatusOK     = "ok"
	StatusError  = "error"
	StatusNoAuth = "no-auth"
)

// Window/Provider source values. "oauth" is a real reading from Anthropic;
// "estimate" is the telemetry-derived secondary card owned by internal/api.
const (
	SourceOAuth    = "oauth"
	SourceEstimate = "estimate"
)

// Pace status values. Note the vocabulary, kept verbatim from Fusion so the
// operator-visible strings match the reference: "ahead" means burning FASTER
// than a linear burn of the window (rendered as a warning), "behind" means
// under pace (rendered positively). The words read backwards at first pass.
const (
	PaceAhead   = "ahead"
	PaceOnTrack = "on-track"
	PaceBehind  = "behind"
)

// Pace compares consumption against elapsed time inside a window, in
// percentage points (Fusion semantics: percentUsed - percentElapsed).
type Pace struct {
	Status         string  `json:"status"` // "ahead" | "on-track" | "behind"
	PercentElapsed float64 `json:"percentElapsed"`
	Message        string  `json:"message"`
}

// Window is one quota window (session, weekly, per-model weekly, or an
// operator-configured telemetry estimate).
type Window struct {
	Key         string  `json:"key"`   // stable id for React keys + hide prefs
	Label       string  `json:"label"` // "Session (5h)", "Weekly", "Weekly (Fable)"
	PercentUsed float64 `json:"percentUsed"`
	PercentLeft float64 `json:"percentLeft"`
	ResetText   string  `json:"resetText,omitempty"` // "resets in 3h 30m"
	ResetMs     int64   `json:"resetMs,omitempty"`   // ms until reset
	ResetAt     string  `json:"resetAt,omitempty"`   // RFC3339
	WindowMs    int64   `json:"windowDurationMs,omitempty"`
	Pace        *Pace   `json:"pace,omitempty"`
	Source      string  `json:"source"`          // "oauth" | "estimate"
	Used        int64   `json:"used,omitempty"`  // estimate provider only
	Limit       int64   `json:"limit,omitempty"` // estimate provider only
}

// Provider is one card in the Usage modal.
type Provider struct {
	Name    string   `json:"name"`   // "Claude"
	Status  string   `json:"status"` // "ok" | "error" | "no-auth"
	Error   string   `json:"error,omitempty"`
	Plan    string   `json:"plan,omitempty"` // "Max" | "Pro" | "Team"
	Source  string   `json:"source"`         // "oauth" | "estimate"
	Windows []Window `json:"windows"`
}
