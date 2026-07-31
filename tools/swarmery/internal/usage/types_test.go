package usage

import (
	"encoding/json"
	"strings"
	"testing"
)

// The JSON tags on Provider/Window/Pace are the wire contract Phase 2 serializes
// verbatim and the Usage modal consumes. Pin them so a rename is a test failure
// rather than a silently empty UI row.

func TestWindowJSONContract(t *testing.T) {
	w := Window{
		Key:         "session-5h",
		Label:       "Session (5h)",
		PercentUsed: 42,
		PercentLeft: 58,
		ResetText:   "resets in 3h 30m",
		ResetMs:     12_600_000,
		ResetAt:     "2026-07-28T15:30:00Z",
		WindowMs:    18_000_000,
		Pace:        &Pace{Status: PaceAhead, PercentElapsed: 30, Message: "12% over pace"},
		Source:      SourceOAuth,
	}
	raw, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("marshal window: %v", err)
	}
	const want = `{"key":"session-5h","label":"Session (5h)","percentUsed":42,"percentLeft":58,` +
		`"resetText":"resets in 3h 30m","resetMs":12600000,"resetAt":"2026-07-28T15:30:00Z",` +
		`"windowDurationMs":18000000,"pace":{"status":"ahead","percentElapsed":30,"message":"12% over pace"},` +
		`"source":"oauth"}`
	if string(raw) != want {
		t.Errorf("window JSON =\n  %s\nwant\n  %s", raw, want)
	}
}

func TestWindowJSONOmitsOptionalFields(t *testing.T) {
	raw, err := json.Marshal(Window{Key: "weekly", Label: "Weekly", PercentLeft: 100, Source: SourceOAuth})
	if err != nil {
		t.Fatalf("marshal window: %v", err)
	}
	const want = `{"key":"weekly","label":"Weekly","percentUsed":0,"percentLeft":100,"source":"oauth"}`
	if string(raw) != want {
		t.Errorf("sparse window JSON =\n  %s\nwant\n  %s", raw, want)
	}
}

func TestEstimateOnlyFieldsAreOptional(t *testing.T) {
	raw, err := json.Marshal(Window{Key: "weekly", Label: "Weekly", Source: SourceEstimate, Used: 12, Limit: 100})
	if err != nil {
		t.Fatalf("marshal window: %v", err)
	}
	for _, want := range []string{`"source":"estimate"`, `"used":12`, `"limit":100`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("estimate window JSON %s missing %s", raw, want)
		}
	}
}

// TestProviderJSONContract pins the card contract INCLUDING `account`, which is
// not optional: with more than one account every card is named "Claude", so the
// UI's identity is `account:name` and a card that shipped without the field
// would collide with the default account's card.
func TestProviderJSONContract(t *testing.T) {
	raw, err := json.Marshal(Provider{
		Account: "nabu-org",
		Name:    providerName,
		Status:  StatusOK,
		Plan:    "Max",
		Source:  SourceOAuth,
		Windows: []Window{},
	})
	if err != nil {
		t.Fatalf("marshal provider: %v", err)
	}
	const want = `{"account":"nabu-org","name":"Claude","status":"ok","plan":"Max","source":"oauth","windows":[]}`
	if string(raw) != want {
		t.Errorf("provider JSON =\n  %s\nwant\n  %s", raw, want)
	}
}

func TestProviderErrorCardJSON(t *testing.T) {
	raw, err := json.Marshal(Provider{
		Name:    providerName,
		Status:  StatusNoAuth,
		Error:   "No Claude credentials — run `claude` to log in",
		Source:  SourceOAuth,
		Windows: []Window{},
	})
	if err != nil {
		t.Fatalf("marshal provider: %v", err)
	}
	// An empty window list must serialize as [] (not null) so the UI can map it.
	if !strings.Contains(string(raw), `"windows":[]`) {
		t.Errorf("provider JSON %s should carry an empty windows array", raw)
	}
	if !strings.Contains(string(raw), `"status":"no-auth"`) {
		t.Errorf("provider JSON %s should carry the no-auth status", raw)
	}
}
