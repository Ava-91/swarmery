package api

// Tests for GET /api/usage — the provider-array contract (live Claude card +
// optional telemetry-estimate card).
//
// # Isolation contract
//
// usageClient is a package-level *usage.Client whose ZERO VALUE reads the
// operator's real credential store (~/.claude/.credentials.json, the macOS
// keychain) and dials api.anthropic.com. No test in this package may do either,
// so two guards are in place:
//
//   - init() below replaces the client for the whole test binary with one that
//     resolves no credentials over a transport that refuses every dial. A test
//     that reaches /api/usage without opting in therefore CANNOT touch the
//     network or the credential store — it just gets the no-auth card.
//   - installStubUsageClient installs a per-test client wired to a local
//     httptest stub, and restores the previous client AND clears the shared
//     response cache on cleanup, so no test leaks state into the next one
//     (both usageClient and usageCache are process-wide).
//
// # Pace semantics changed in this phase
//
// The old local pace() helper returned a RATIO (usedPct/elapsedPct - 1, e.g.
// 0.1666 rendered as "17% over pace"). The estimate card now calls
// usage.CalculatePace, which is Fusion's PERCENTAGE-POINT delta
// (percentUsed - percentElapsed, with a ±5-point on-track dead band). Identical
// state therefore yields different numbers: 10% used at 50% elapsed was
// -0.8 ("80% under pace") and is now -40 points ("40% under pace"). TestPace is
// gone with the helper; pace is covered by internal/usage/pace_test.go.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/usage"
)

// ── stub credential + endpoint plumbing ────────────────────────────────────

// usageFixtureToken deliberately carries the `sk-ant` shape so
// TestUsageResponseCarriesNoSecrets fails loudly if a bearer ever reaches a
// response body. It is not, and never was, a real credential.
const (
	usageFixtureToken   = "sk-ant-oat01-FAKE-TOKEN-FOR-TESTS-ONLY"
	usageFixtureRefresh = "sk-ant-ort01-FAKE-REFRESH-FOR-TESTS-ONLY"
)

// usageStubNow pins the live client's clock so every reset in
// usageStubPayload resolves to a fixed countdown, elapsed fraction, and pace.
var usageStubNow = time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

// usageStubPayload is an Anthropic /api/oauth/usage body in the shape the live
// endpoint ships. Against usageStubNow it yields three windows:
//
//	Session (5h)    28% used, resets in 3h 30m of a 5h window → 30% elapsed → on-track
//	Weekly          19% used, resets in 5d    of a 7d window → 29% elapsed → behind
//	Weekly (Fable)  28% used, same 7d window                  → 29% elapsed → on-track
const usageStubPayload = `{
  "five_hour": {"utilization": 28, "resets_at": "2026-07-28T15:30:00Z"},
  "seven_day": {"utilization": 19, "resets_at": "2026-08-02T12:00:00Z"},
  "seven_day_opus": null,
  "seven_day_sonnet": null,
  "limits": [
    {"kind": "weekly_scoped", "group": "weekly", "percent": 28,
     "resets_at": "2026-08-02T12:00:00Z",
     "scope": {"model": {"display_name": "Fable"}}}
  ]
}`

// usageRefusingTransport makes an accidental real dial a loud test failure
// rather than a silent network call.
type usageRefusingTransport struct{}

func (usageRefusingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("outbound network is disabled in api tests")
}

// init hardens the whole api test binary — see the isolation contract above.
func init() {
	usageClient = &usage.Client{
		HTTP:      &http.Client{Transport: usageRefusingTransport{}},
		Now:       func() time.Time { return usageStubNow },
		LoadCreds: usageNoCreds,
	}
}

func usageNoCreds(context.Context) (*usage.Creds, error) { return nil, usage.ErrNoCreds }

// usageLoggedInCreds mimics a healthy `claude` login: the required user:profile
// scope and a far-future expiry, so no refresh round-trip is attempted.
func usageLoggedInCreds(context.Context) (*usage.Creds, error) {
	return &usage.Creds{
		AccessToken:      usageFixtureToken,
		RefreshToken:     usageFixtureRefresh,
		ExpiresAt:        usageStubNow.Add(24 * time.Hour).UnixMilli(),
		Scopes:           []string{"user:inference", "user:profile"},
		SubscriptionType: "max",
	}, nil
}

// usageStub stands in for api.anthropic.com. It answers every path with the
// same body and records the call count + the bearers it was sent, so cache
// behaviour and token handling are both assertable.
type usageStub struct {
	srv *httptest.Server

	mu      sync.Mutex
	calls   int
	bearers []string
}

func newUsageStub(t *testing.T, body string) *usageStub {
	t.Helper()
	s := &usageStub{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.calls++
		s.bearers = append(s.bearers, r.Header.Get("authorization"))
		s.mu.Unlock()
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *usageStub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *usageStub) sentBearers() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.bearers...)
}

// installStubUsageClient points the package-level client at stub (nil = no
// endpoint at all, over a refusing transport) with the given credential loader.
// It clears the shared response cache before and after, so tests are
// order-independent.
func installStubUsageClient(t *testing.T, stub *usageStub, load func(context.Context) (*usage.Creds, error)) {
	t.Helper()
	prev := usageClient
	c := &usage.Client{
		Now:       func() time.Time { return usageStubNow },
		LoadCreds: load,
	}
	if stub != nil {
		c.HTTP, c.APIBase, c.AuthBase = stub.srv.Client(), stub.srv.URL, stub.srv.URL
	} else {
		c.HTTP = &http.Client{Transport: usageRefusingTransport{}}
	}
	usageClient = c
	resetUsageCache()
	t.Cleanup(func() {
		usageClient = prev
		resetUsageCache()
	})
}

// providerNamed returns the provider with the given name, failing the test when
// it is absent.
func providerNamed(t *testing.T, resp usageResp, name string) usage.Provider {
	t.Helper()
	for _, p := range resp.Providers {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("provider %q missing; got %v", name, providerNames(resp))
	return usage.Provider{}
}

func providerNames(resp usageResp) []string {
	names := make([]string, 0, len(resp.Providers))
	for _, p := range resp.Providers {
		names = append(names, p.Name)
	}
	return names
}

func windowLabeled(t *testing.T, p usage.Provider, label string) usage.Window {
	t.Helper()
	for _, w := range p.Windows {
		if w.Label == label {
			return w
		}
	}
	t.Fatalf("provider %q has no window labelled %q; got %+v", p.Name, label, p.Windows)
	return usage.Window{}
}

// ── pure-function tests (fixed clock, no DB) ───────────────────────────────

func TestParseUsageLimits(t *testing.T) {
	// Blank → not configured, not an error.
	if cfg, err := parseUsageLimits("   "); err != nil || cfg != nil {
		t.Errorf("blank: got cfg=%v err=%v, want nil,nil", cfg, err)
	}
	// Invalid JSON → error.
	if _, err := parseUsageLimits(`{not json`); err == nil {
		t.Errorf("invalid JSON: want error, got nil")
	}
	// Valid: a positive window is kept; a non-positive quota/window is dropped
	// (defensive — a misconfigured window must not divide-by-zero downstream).
	raw := `{
		"session5h":{"label":"5-hour session","tokens":50000000,"windowHours":5},
		"weekly":{"label":"Weekly","tokens":300000000,"windowHours":168},
		"bogusZeroTokens":{"tokens":0,"windowHours":5},
		"bogusZeroWindow":{"tokens":10,"windowHours":0}
	}`
	cfg, err := parseUsageLimits(raw)
	if err != nil {
		t.Fatalf("valid parse: %v", err)
	}
	if len(cfg) != 2 {
		t.Fatalf("kept %d windows, want 2 (two bogus dropped)", len(cfg))
	}
	if cfg["session5h"].Tokens != 50000000 || cfg["session5h"].WindowHours != 5 {
		t.Errorf("session5h = %+v", cfg["session5h"])
	}
	if _, ok := cfg["bogusZeroTokens"]; ok {
		t.Errorf("bogusZeroTokens should have been dropped")
	}
	if _, ok := cfg["bogusZeroWindow"]; ok {
		t.Errorf("bogusZeroWindow should have been dropped")
	}
}

func TestUsageWindowElapsed(t *testing.T) {
	// Fixed clock. The window is anchored to a deterministic grid (whole windows
	// since the Unix epoch) so "resets in" is stable between polls.
	loc := time.UTC
	now := time.Date(2026, 7, 24, 13, 30, 0, 0, loc) // 13:30:00 UTC

	// 5-hour window. epoch = now.Unix(); winSec = 5h. windowStart = floor to the
	// 5h grid; elapsed = now - windowStart ∈ [0, 5h); resetsAt = windowStart+5h.
	elapsed, window, resetsAt := usageWindowElapsed(now, 5)
	if window != 5*time.Hour {
		t.Errorf("window = %v, want 5h", window)
	}
	if elapsed < 0 || elapsed >= 5*time.Hour {
		t.Errorf("elapsed = %v, want within [0,5h)", elapsed)
	}
	// resetsAt is exactly windowStart + window, and windowStart = now - elapsed.
	wantReset := now.Add(-elapsed).Add(window)
	if !resetsAt.Equal(wantReset) {
		t.Errorf("resetsAt = %v, want %v", resetsAt, wantReset)
	}
	// The grid is aligned: (windowStart since epoch) is a whole multiple of 5h.
	windowStart := now.Add(-elapsed)
	if windowStart.Unix()%int64((5*time.Hour).Seconds()) != 0 {
		t.Errorf("windowStart %v not aligned to the 5h grid", windowStart)
	}

	// Determinism: two calls at the same instant agree.
	e2, _, r2 := usageWindowElapsed(now, 5)
	if e2 != elapsed || !r2.Equal(resetsAt) {
		t.Errorf("non-deterministic: (%v,%v) vs (%v,%v)", e2, r2, elapsed, resetsAt)
	}
}

// ── HTTP integration tests ─────────────────────────────────────────────────

// usageTestDB opens a store with one project/session and the given turns, and
// returns a server over it.
func usageTestDB(t *testing.T, name string, turnAt string, tokens ...[2]int64) (*sql.DB, *httptest.Server) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("exec: %v\n%s", err, q)
		}
	}
	mustExec(`INSERT INTO projects (id, path, slug, name, first_seen) VALUES (1, '/w/a', '-w-a', 'A', ?)`, turnAt)
	mustExec(`INSERT INTO sessions (id, project_id, session_uuid, model, status, started_at) VALUES (1, 1, 'u1', 'm', 'completed', ?)`, turnAt)
	for i, tk := range tokens {
		mustExec(`INSERT INTO turns (session_id, seq, role, started_at, tokens_in, tokens_out) VALUES (1, ?, 'assistant', ?, ?, ?)`,
			i+1, turnAt, tk[0], tk[1])
	}

	h, err := NewServer(db, false)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return db, srv
}

// TestUsageHTTP is the whole-contract test: {generatedAt, providers[]} with the
// live Claude card FIRST and the telemetry estimate SECOND.
func TestUsageHTTP(t *testing.T) {
	now := time.Now()
	// The 5h window is anchored to a grid counted from the Unix epoch, which does
	// not divide a day evenly — so a fixed "now - 10min" offset lands outside the
	// window for 10 minutes out of every 5 hours (a real flake, seen in CI).
	// Derive the timestamp from the same helper the endpoint uses instead.
	elapsed5h, _, _ := usageWindowElapsed(now, 5)
	inWindow := now.Add(-elapsed5h).Add(time.Minute)
	recent := inWindow.UTC().Format("2006-01-02T15:04:05")
	old := now.Add(-240 * time.Hour).UTC().Format("2006-01-02T15:04:05")

	db, err := store.Open(filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("exec: %v\n%s", err, q)
		}
	}
	mustExec(`INSERT INTO projects (id, path, slug, name, first_seen) VALUES (1, '/w/a', '-w-a', 'A', ?)`, old)
	mustExec(`INSERT INTO sessions (id, project_id, session_uuid, model, status, started_at) VALUES (1, 1, 'u1', 'm', 'completed', ?)`, recent)
	mustExec(`INSERT INTO turns (session_id, seq, role, started_at, tokens_in, tokens_out) VALUES
		(1, 1, 'assistant', ?, 1000, 500),
		(1, 2, 'assistant', ?, 200, 100),
		(1, 3, 'assistant', ?, 999999, 999999)`, recent, recent, old)

	h, err := NewServer(db, false)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	stub := newUsageStub(t, usageStubPayload)
	installStubUsageClient(t, stub, usageLoggedInCreds)

	// Configured: a 5-hour window with a generous quota. used = 1500+300 = 1800
	// (the 10-day-old turn is excluded).
	t.Setenv("SWARMERY_USAGE_LIMITS", `{"session5h":{"label":"5-hour session","tokens":1000000,"windowHours":5}}`)
	resetUsageCache()

	var got usageResp
	getJSON(t, srv.URL+"/api/usage?fresh=1", &got)

	if _, perr := time.Parse(time.RFC3339, got.GeneratedAt); perr != nil {
		t.Errorf("generatedAt %q not RFC3339: %v", got.GeneratedAt, perr)
	}
	if len(got.Providers) != 2 {
		t.Fatalf("providers = %v, want [Claude, %s]", providerNames(got), estimateProviderName)
	}
	if got.Providers[0].Name != "Claude" {
		t.Errorf("providers[0] = %q, want Claude first", got.Providers[0].Name)
	}

	// ── live Claude card ───────────────────────────────────────────────────
	claude := got.Providers[0]
	if claude.Status != usage.StatusOK {
		t.Errorf("claude status = %q (err %q), want ok", claude.Status, claude.Error)
	}
	if claude.Plan != "Max" {
		t.Errorf("claude plan = %q, want Max (title-cased subscriptionType)", claude.Plan)
	}
	if claude.Source != usage.SourceOAuth {
		t.Errorf("claude source = %q, want oauth", claude.Source)
	}
	if claude.Error != "" {
		t.Errorf("claude error = %q, want empty on the ok path", claude.Error)
	}

	// Values pinned to the plan doc's sample body: 28% used, 3h30m left of a 5h
	// window → 30% elapsed → on-track.
	session := windowLabeled(t, claude, "Session (5h)")
	if session.Key != "session-5h" {
		t.Errorf("session key = %q, want session-5h", session.Key)
	}
	if session.PercentUsed != 28 || session.PercentLeft != 72 {
		t.Errorf("session used/left = %v/%v, want 28/72", session.PercentUsed, session.PercentLeft)
	}
	if session.ResetMs != 12_600_000 {
		t.Errorf("session resetMs = %d, want 12600000 (3h30m)", session.ResetMs)
	}
	if session.WindowMs != (5 * time.Hour).Milliseconds() {
		t.Errorf("session windowDurationMs = %d, want %d", session.WindowMs, (5 * time.Hour).Milliseconds())
	}
	if session.ResetText != "resets in 3h 30m" {
		t.Errorf("session resetText = %q, want %q", session.ResetText, "resets in 3h 30m")
	}
	if session.Source != usage.SourceOAuth {
		t.Errorf("session source = %q, want oauth", session.Source)
	}
	if session.Pace == nil {
		t.Fatalf("session pace missing")
	}
	if session.Pace.Status != usage.PaceOnTrack || session.Pace.PercentElapsed != 30 {
		t.Errorf("session pace = %+v, want on-track at 30%% elapsed", *session.Pace)
	}

	// Weekly: 19% used at ~29% elapsed → 10 points under → "behind".
	weekly := windowLabeled(t, claude, "Weekly")
	if weekly.PercentUsed != 19 {
		t.Errorf("weekly used = %v, want 19", weekly.PercentUsed)
	}
	if weekly.Pace == nil {
		t.Fatalf("weekly pace missing")
	}
	if weekly.Pace.Status != usage.PaceBehind || weekly.Pace.Message != "10% under pace" {
		t.Errorf("weekly pace = %+v, want behind / 10%% under pace", *weekly.Pace)
	}
	// The generic limits[] walk contributes the per-model weekly row.
	if fable := windowLabeled(t, claude, "Weekly (Fable)"); fable.PercentUsed != 28 {
		t.Errorf("Weekly (Fable) used = %v, want 28", fable.PercentUsed)
	}

	// ── telemetry-estimate card ────────────────────────────────────────────
	est := providerNamed(t, got, estimateProviderName)
	if est.Status != usage.StatusOK || est.Source != usage.SourceEstimate {
		t.Errorf("estimate card = status %q source %q, want ok/estimate", est.Status, est.Source)
	}
	if len(est.Windows) != 1 {
		t.Fatalf("estimate windows = %d, want 1", len(est.Windows))
	}
	win := est.Windows[0]
	if win.Key != "session5h" || win.Label != "5-hour session" {
		t.Errorf("estimate window = %q/%q, want session5h/5-hour session", win.Key, win.Label)
	}
	if win.Used != 1800 {
		t.Errorf("used = %d, want 1800 (10-day-old turn excluded)", win.Used)
	}
	if win.Limit != 1000000 {
		t.Errorf("limit = %d, want 1000000", win.Limit)
	}
	if win.Source != usage.SourceEstimate {
		t.Errorf("window source = %q, want estimate", win.Source)
	}
	// percentUsed = used/limit*100, clamped; percentLeft is its complement.
	if wantPct := 1800.0 / 1000000.0 * 100; math.Abs(win.PercentUsed-wantPct) > 1e-9 {
		t.Errorf("percentUsed = %v, want %v", win.PercentUsed, wantPct)
	}
	if math.Abs(win.PercentUsed+win.PercentLeft-100) > 1e-9 {
		t.Errorf("percentUsed+percentLeft = %v, want 100", win.PercentUsed+win.PercentLeft)
	}
	if win.WindowMs != (5 * time.Hour).Milliseconds() {
		t.Errorf("estimate windowDurationMs = %d, want %d", win.WindowMs, (5 * time.Hour).Milliseconds())
	}
	// The estimate card shares ONE pace definition with the live card. Which of
	// the three statuses fires depends on where wall-clock "now" sits in the 5h
	// grid, so assert the shape, not a fixed status.
	if win.Pace == nil {
		t.Fatalf("estimate pace missing — usage.CalculatePace should fire for a live window")
	}
	switch win.Pace.Status {
	case usage.PaceAhead, usage.PaceOnTrack, usage.PaceBehind:
	default:
		t.Errorf("estimate pace status = %q, not one of ahead/on-track/behind", win.Pace.Status)
	}
	if !strings.HasPrefix(win.ResetText, "resets in ") {
		t.Errorf("estimate resetText = %q, want a %q countdown", win.ResetText, "resets in …")
	}
	// resetAt must be valid RFC3339 in the future.
	rt, perr := time.Parse(time.RFC3339, win.ResetAt)
	if perr != nil {
		t.Errorf("resetAt %q not RFC3339: %v", win.ResetAt, perr)
	}
	if !rt.After(now.Add(-time.Second)) {
		t.Errorf("resetAt %v should be ~now or later", rt)
	}
}

var usageGeneratedAtRe = regexp.MustCompile(`"generatedAt":"[^"]*"`)

// TestUsageNoCredsNoLimitsSingleProvider pins the body served on the common
// clean machine: no Claude login, no SWARMERY_USAGE_LIMITS. The estimate card
// must be ABSENT (not an empty card), the Claude card must be a no-auth card
// with actionable copy, and the status must still be 200 — a quota reading we
// cannot take is not a server error.
func TestUsageNoCredsNoLimitsSingleProvider(t *testing.T) {
	_, srv := usageTestDB(t, "usage-noauth.db", time.Now().UTC().Format("2006-01-02T15:04:05"))
	installStubUsageClient(t, nil, usageNoCreds)
	t.Setenv("SWARMERY_USAGE_LIMITS", "")
	resetUsageCache()

	status, body := getBody(t, srv.URL+"/api/usage?fresh=1")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a missing credential is not a server error)", status)
	}

	// generatedAt is the only volatile field; everything else is the contract.
	got := usageGeneratedAtRe.ReplaceAllString(strings.TrimSpace(body), `"generatedAt":"<ts>"`)
	want := `{"generatedAt":"<ts>","providers":[` +
		"{\"name\":\"Claude\",\"status\":\"no-auth\"," +
		"\"error\":\"No Claude credentials — run `claude` to log in\"," +
		`"source":"oauth","windows":[]}]}`
	if got != want {
		t.Errorf("body =\n%s\nwant\n%s", got, want)
	}

	var decoded usageResp
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Providers) != 1 {
		t.Fatalf("providers = %v, want exactly one (no estimate card without limits)", providerNames(decoded))
	}
	if decoded.Providers[0].Status != usage.StatusNoAuth {
		t.Errorf("status = %q, want no-auth", decoded.Providers[0].Status)
	}
	if !strings.Contains(decoded.Providers[0].Error, "claude") {
		t.Errorf("error %q is not actionable — it should name the `claude` CLI", decoded.Providers[0].Error)
	}
}

// TestUsageTwoWindowsOrderAndLabelFallback covers the shortest-window-first sort
// comparator (needs ≥2 windows to fire) and the label fallback (a window with no
// label reports its key). It also pins the cache contract: two calls inside the
// 30s TTL cost exactly ONE Anthropic round-trip, and ?fresh=1 always costs one.
func TestUsageTwoWindowsOrderAndLabelFallback(t *testing.T) {
	recent := time.Now().Add(-5 * time.Minute).UTC().Format("2006-01-02T15:04:05")
	_, srv := usageTestDB(t, "usage2.db", recent, [2]int64{10, 5})

	stub := newUsageStub(t, usageStubPayload)
	installStubUsageClient(t, stub, usageLoggedInCreds)

	// weekly (168h) declared before session5h (5h); the "weekly" window has NO
	// label → must fall back to its key. Sort must put the 5h window first.
	t.Setenv("SWARMERY_USAGE_LIMITS",
		`{"weekly":{"tokens":9000000,"windowHours":168},"session5h":{"label":"5h","tokens":900000,"windowHours":5}}`)
	resetUsageCache()

	var got usageResp
	getJSON(t, srv.URL+"/api/usage?fresh=1", &got)
	est := providerNamed(t, got, estimateProviderName)
	if len(est.Windows) != 2 {
		t.Fatalf("estimate windows = %d, want 2", len(est.Windows))
	}
	if est.Windows[0].Key != "session5h" {
		t.Errorf("first window = %q, want session5h (shortest first)", est.Windows[0].Key)
	}
	if est.Windows[1].Key != "weekly" || est.Windows[1].Label != "weekly" {
		t.Errorf("second window label = %q, want key fallback %q", est.Windows[1].Label, "weekly")
	}
	if n := stub.count(); n != 1 {
		t.Fatalf("anthropic calls after one ?fresh=1 = %d, want 1", n)
	}

	// Cache-hit fast path: a NON-fresh call inside the 30s TTL returns the cached
	// body and costs NO additional upstream call.
	var cached usageResp
	getJSON(t, srv.URL+"/api/usage", &cached)
	cachedEst := providerNamed(t, cached, estimateProviderName)
	if len(cachedEst.Windows) != 2 || cachedEst.Windows[0].Key != "session5h" {
		t.Errorf("cache-hit body = %+v, want the cached 2-window response", cachedEst.Windows)
	}
	if cached.GeneratedAt != got.GeneratedAt {
		t.Errorf("cache-hit generatedAt = %q, want the cached %q", cached.GeneratedAt, got.GeneratedAt)
	}
	if n := stub.count(); n != 1 {
		t.Errorf("anthropic calls after a cached call = %d, want still 1", n)
	}

	// ?fresh=1 bypasses the cache and does cost a call.
	var fresh usageResp
	getJSON(t, srv.URL+"/api/usage?fresh=1", &fresh)
	if n := stub.count(); n != 2 {
		t.Errorf("anthropic calls after a second ?fresh=1 = %d, want 2", n)
	}
}

// TestUsageDBErrorPath proves the handler surfaces a 500 (not a panic or a
// half-written body) when the token query fails — here by closing the DB before
// the request so usedTokensSince errors.
func TestUsageDBErrorPath(t *testing.T) {
	db, srv := usageTestDB(t, "usage-err.db", time.Now().UTC().Format("2006-01-02T15:04:05"))
	installStubUsageClient(t, nil, usageNoCreds)

	t.Setenv("SWARMERY_USAGE_LIMITS", `{"session5h":{"label":"5h","tokens":900000,"windowHours":5}}`)
	resetUsageCache()
	db.Close() // force every subsequent query to fail

	status, _ := getBody(t, srv.URL+"/api/usage?fresh=1")
	if status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 on DB error", status)
	}
}

// TestUsageInvalidLimitsReturns500 pins pre-existing behaviour that survived the
// provider-array rewrite: a malformed SWARMERY_USAGE_LIMITS is an operator
// misconfiguration the endpoint reports loudly rather than silently dropping the
// estimate card.
func TestUsageInvalidLimitsReturns500(t *testing.T) {
	_, srv := usageTestDB(t, "usage-badcfg.db", time.Now().UTC().Format("2006-01-02T15:04:05"))
	installStubUsageClient(t, nil, usageNoCreds)

	t.Setenv("SWARMERY_USAGE_LIMITS", `{not json`)
	resetUsageCache()

	status, body := getBody(t, srv.URL+"/api/usage?fresh=1")
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 on invalid SWARMERY_USAGE_LIMITS", status)
	}
	if !strings.Contains(body, "invalid SWARMERY_USAGE_LIMITS JSON") {
		t.Errorf("body = %q, want the invalid-limits message", strings.TrimSpace(body))
	}
	// A rejected request must not poison the cache for the next caller.
	if _, ok := cachedUsage(); ok {
		t.Errorf("a 500 response was cached; the next caller would be served an error state")
	}
}

// TestUsageResponseCarriesNoSecrets is the token-leak guard. The bearer is
// genuinely used upstream (asserted via the stub's recorded authorization
// header) and must appear NOWHERE in the bytes the daemon serves.
func TestUsageResponseCarriesNoSecrets(t *testing.T) {
	recent := time.Now().UTC().Format("2006-01-02T15:04:05")
	_, srv := usageTestDB(t, "usage-secrets.db", recent, [2]int64{10, 5})

	stub := newUsageStub(t, usageStubPayload)
	installStubUsageClient(t, stub, usageLoggedInCreds)
	t.Setenv("SWARMERY_USAGE_LIMITS", `{"session5h":{"label":"5h","tokens":900000,"windowHours":5}}`)
	resetUsageCache()

	status, body := getBody(t, srv.URL+"/api/usage?fresh=1")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}

	// The token really was sent upstream — otherwise this test would pass
	// vacuously against a code path that never touches a credential.
	bearers := stub.sentBearers()
	if len(bearers) == 0 || bearers[0] != "Bearer "+usageFixtureToken {
		t.Fatalf("stub saw bearers %v, want the fixture token sent exactly once upstream", bearers)
	}

	// …and none of it reached the operator-facing body.
	for _, banned := range []string{
		"Bearer", "bearer", "sk-ant", "accessToken", "refreshToken",
		usageFixtureToken, usageFixtureRefresh,
	} {
		if strings.Contains(body, banned) {
			t.Errorf("response body contains %q — credential material must never be serialized:\n%s", banned, body)
		}
	}

	// Re-marshalling the decoded struct must be equally clean, so the guard also
	// covers the cached copy the next caller would be served.
	var decoded usageResp
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	remarshalled, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, banned := range []string{"Bearer", "sk-ant", "accessToken", "refreshToken", usageFixtureToken} {
		if strings.Contains(string(remarshalled), banned) {
			t.Errorf("re-marshalled body contains %q", banned)
		}
	}
}
