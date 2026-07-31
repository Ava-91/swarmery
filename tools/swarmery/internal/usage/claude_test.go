package usage

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// testNow pins every fixture's reset arithmetic. 2026-07-28T12:00:00Z is
// unix 1785240000 — the numeric timestamps in the testdata fixtures are offsets
// from this instant.
var testNow = time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return raw
}

// stub is an httptest server standing in for api.anthropic.com and
// platform.claude.com at once, recording what the client actually sent.
type stub struct {
	srv *httptest.Server

	mu           sync.Mutex
	usageCalls   int
	refreshCalls int
	bearers      []string
	betas        []string
	agents       []string
	refreshBody  map[string]string
}

// newStub wires a usage handler (given the 1-based attempt number) and an
// optional refresh handler; a nil refresh handler mints "refreshed-token".
func newStub(t *testing.T,
	usage func(attempt int, w http.ResponseWriter),
	refresh func(attempt int, w http.ResponseWriter),
) *stub {
	t.Helper()
	s := &stub{}
	mux := http.NewServeMux()
	mux.HandleFunc(oauthUsagePath, func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.usageCalls++
		attempt := s.usageCalls
		s.bearers = append(s.bearers, r.Header.Get("authorization"))
		s.betas = append(s.betas, r.Header.Get("anthropic-beta"))
		s.agents = append(s.agents, r.Header.Get("user-agent"))
		s.mu.Unlock()
		usage(attempt, w)
	})
	mux.HandleFunc(tokenPath, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]string
		_ = json.Unmarshal(body, &parsed)
		s.mu.Lock()
		s.refreshCalls++
		attempt := s.refreshCalls
		s.refreshBody = parsed
		s.mu.Unlock()
		if refresh != nil {
			refresh(attempt, w)
			return
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"refreshed-token"}`))
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

// wantSetupCard asserts the shape a credential-shaped failure must have: the
// provider is "not connected" rather than "error", it still carries the one-line
// message, and it carries a hint complete enough for the card to explain what is
// missing, how to supply it, why it is needed and how it is handled.
func wantSetupCard(t *testing.T, p Provider, kind, wantMsg string) {
	t.Helper()
	if p.Status != StatusNoAuth {
		t.Errorf("status = %q, want %q — a credential problem is setup, not a provider error", p.Status, StatusNoAuth)
	}
	if !strings.Contains(p.Error, wantMsg) {
		t.Errorf("error = %q, want it to mention %q", p.Error, wantMsg)
	}
	if p.Hint == nil {
		t.Fatalf("hint = nil, want setup guidance alongside %q", p.Error)
	}
	if p.Hint.Kind != kind {
		t.Errorf("hint kind = %q, want %q", p.Hint.Kind, kind)
	}
	if p.Hint.Title == "" || p.Hint.Detail == "" || p.Hint.Command == "" ||
		p.Hint.Why == "" || p.Hint.Handling == "" {
		t.Errorf("hint = %+v, want every operator-facing field populated", *p.Hint)
	}
}

// serveJSON is the common "200 with this body" usage handler.
func serveJSON(body []byte) func(int, http.ResponseWriter) {
	return func(_ int, w http.ResponseWriter) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write(body)
	}
}

func testCreds() *Creds {
	return &Creds{
		AccessToken:      fakeAccess,
		RefreshToken:     fakeRefresh,
		ExpiresAt:        testNow.Add(24 * time.Hour).UnixMilli(),
		Scopes:           []string{"user:inference", requiredScope},
		SubscriptionType: "max",
	}
}

// newClient returns a client wired to the stub with a fixed clock and a
// recorded (never real) sleep.
func newClient(s *stub, creds *Creds) (*Client, *[]time.Duration) {
	var slept []time.Duration
	c := &Client{
		HTTP:     s.srv.Client(),
		Now:      func() time.Time { return testNow },
		APIBase:  s.srv.URL,
		AuthBase: s.srv.URL,
		LoadCreds: func(context.Context) (*Creds, error) {
			if creds == nil {
				return nil, ErrNoCreds
			}
			return creds, nil
		},
	}
	c.sleep = func(d time.Duration) { slept = append(slept, d) }
	return c, &slept
}

func labelsOf(p Provider) []string {
	out := make([]string, 0, len(p.Windows))
	for _, w := range p.Windows {
		out = append(out, w.Label)
	}
	return out
}

func windowByLabel(t *testing.T, p Provider, label string) Window {
	t.Helper()
	for _, w := range p.Windows {
		if w.Label == label {
			return w
		}
	}
	t.Fatalf("no window labelled %q in %v", label, labelsOf(p))
	return Window{}
}

func TestFetchHappyPath(t *testing.T) {
	s := newStub(t, serveJSON(fixture(t, "usage-full.json")), nil)
	c, _ := newClient(s, testCreds())

	p := c.Fetch(context.Background())

	if p.Status != StatusOK {
		t.Fatalf("status = %q (%s), want ok", p.Status, p.Error)
	}
	if p.Name != "Claude" || p.Source != SourceOAuth {
		t.Errorf("provider = %s/%s, want Claude/oauth", p.Name, p.Source)
	}
	if p.Plan != "Max" {
		t.Errorf("plan = %q, want Max", p.Plan)
	}
	if p.Error != "" {
		t.Errorf("error = %q, want empty", p.Error)
	}

	wantLabels := []string{"Session (5h)", "Weekly", "Weekly (Fable)"}
	got := labelsOf(p)
	if len(got) != len(wantLabels) {
		t.Fatalf("labels = %v, want %v", got, wantLabels)
	}
	for i := range wantLabels {
		if got[i] != wantLabels[i] {
			t.Fatalf("labels = %v, want %v", got, wantLabels)
		}
	}

	for _, w := range p.Windows {
		if w.Pace == nil {
			t.Errorf("%s: pace = nil, want a pace", w.Label)
		}
		if w.ResetAt == "" {
			t.Errorf("%s: resetAt is empty", w.Label)
		}
		if w.Source != SourceOAuth {
			t.Errorf("%s: source = %q, want oauth", w.Label, w.Source)
		}
		if w.PercentUsed+w.PercentLeft != 100 {
			t.Errorf("%s: used %v + left %v != 100", w.Label, w.PercentUsed, w.PercentLeft)
		}
	}

	session := windowByLabel(t, p, "Session (5h)")
	if session.Key != "session-5h" {
		t.Errorf("session key = %q, want session-5h", session.Key)
	}
	if session.PercentUsed != 42 || session.PercentLeft != 58 {
		t.Errorf("session = %v used / %v left, want 42/58", session.PercentUsed, session.PercentLeft)
	}
	if session.ResetText != "resets in 3h 30m" {
		t.Errorf("session resetText = %q", session.ResetText)
	}
	if session.ResetAt != "2026-07-28T15:30:00Z" {
		t.Errorf("session resetAt = %q", session.ResetAt)
	}
	if session.WindowMs != fiveHours.Milliseconds() {
		t.Errorf("session windowMs = %d, want %d", session.WindowMs, fiveHours.Milliseconds())
	}
	if session.Pace.Status != PaceAhead || session.Pace.Message != "12% over pace" {
		t.Errorf("session pace = %+v, want ahead/12%% over pace", session.Pace)
	}

	weekly := windowByLabel(t, p, "Weekly")
	if weekly.Key != "weekly" || weekly.PercentUsed != 19 {
		t.Errorf("weekly = %q at %v%%, want weekly at 19%%", weekly.Key, weekly.PercentUsed)
	}
	if weekly.ResetText != "resets in 5d" || weekly.ResetAt != "2026-08-02T12:00:00Z" {
		t.Errorf("weekly reset = %q / %q", weekly.ResetText, weekly.ResetAt)
	}
	if weekly.Pace.Status != PaceBehind || weekly.Pace.Message != "10% under pace" {
		t.Errorf("weekly pace = %+v, want behind/10%% under pace", weekly.Pace)
	}

	fable := windowByLabel(t, p, "Weekly (Fable)")
	if fable.Key != "weekly-fable" || fable.PercentUsed != 28 {
		t.Errorf("fable = %q at %v%%, want weekly-fable at 28%%", fable.Key, fable.PercentUsed)
	}
	if fable.WindowMs != sevenDays.Milliseconds() {
		t.Errorf("fable windowMs = %d, want %d", fable.WindowMs, sevenDays.Milliseconds())
	}
	if fable.Pace.Status != PaceOnTrack {
		t.Errorf("fable pace = %+v, want on-track", fable.Pace)
	}

	// The request must carry exactly what `claude /usage` sends.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.usageCalls != 1 || s.refreshCalls != 0 {
		t.Errorf("calls: usage=%d refresh=%d, want 1/0", s.usageCalls, s.refreshCalls)
	}
	if s.bearers[0] != "Bearer "+fakeAccess {
		t.Error("authorization header did not carry the stored access token")
	}
	if s.betas[0] != anthropicBeta {
		t.Errorf("anthropic-beta = %q, want %q", s.betas[0], anthropicBeta)
	}
	if s.agents[0] != userAgent {
		t.Errorf("user-agent = %q, want %q", s.agents[0], userAgent)
	}
}

func TestFetchLegacyPayloadDedupesLimits(t *testing.T) {
	s := newStub(t, serveJSON(fixture(t, "usage-legacy.json")), nil)
	c, _ := newClient(s, testCreds())

	p := c.Fetch(context.Background())
	if p.Status != StatusOK {
		t.Fatalf("status = %q (%s), want ok", p.Status, p.Error)
	}

	want := []string{"Session (5h)", "Weekly", "Weekly (Sonnet)", "Weekly (Opus)"}
	got := labelsOf(p)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("labels = %v, want %v", got, want)
	}

	// The limits[] entry duplicating "Weekly (Sonnet)" (at 99%) must be
	// skipped — the named key's 33% wins and there is no second row.
	sonnet := windowByLabel(t, p, "Weekly (Sonnet)")
	if sonnet.PercentUsed != 33 {
		t.Errorf("sonnet = %v%%, want the named key's 33%% (limits[] duplicate leaked)", sonnet.PercentUsed)
	}

	// Every percentage and reset-timestamp spelling in the fixture must parse.
	session := windowByLabel(t, p, "Session (5h)")
	if session.PercentUsed != 61 || session.ResetText != "resets in 2h" {
		t.Errorf("session = %v%% / %q, want 61%% / resets in 2h", session.PercentUsed, session.ResetText)
	}
	if session.ResetAt != "2026-07-28T14:00:00Z" {
		t.Errorf("session resetAt = %q, want the unix-seconds value decoded", session.ResetAt)
	}
	if w := windowByLabel(t, p, "Weekly"); w.PercentUsed != 12 || w.ResetAt != "2026-08-02T12:00:00Z" {
		t.Errorf("weekly = %v%% / %q", w.PercentUsed, w.ResetAt)
	}
	if sonnet.ResetAt != "2026-08-02T12:00:00Z" {
		t.Errorf("sonnet resetAt = %q, want the unix-millis value decoded", sonnet.ResetAt)
	}
	if o := windowByLabel(t, p, "Weekly (Opus)"); o.PercentUsed != 7 || o.ResetText != "resets in 3d 12h" {
		t.Errorf("opus = %v%% / %q, want 7%% / resets in 3d 12h", o.PercentUsed, o.ResetText)
	}
}

func TestFetchSessionResetFallback(t *testing.T) {
	s := newStub(t, serveJSON(fixture(t, "usage-session-only.json")), nil)
	c, _ := newClient(s, testCreds())

	p := c.Fetch(context.Background())
	if p.Status != StatusOK {
		t.Fatalf("status = %q (%s), want ok", p.Status, p.Error)
	}

	session := windowByLabel(t, p, "Session (5h)")
	if session.ResetText != "resets in 5h" {
		t.Errorf("session resetText = %q, want the 5h fallback", session.ResetText)
	}
	if session.ResetMs != fiveHours.Milliseconds() {
		t.Errorf("session resetMs = %d, want %d", session.ResetMs, fiveHours.Milliseconds())
	}
	if session.ResetAt != "2026-07-28T17:00:00Z" {
		t.Errorf("session resetAt = %q, want now+5h", session.ResetAt)
	}
	if session.Pace == nil {
		t.Fatal("session pace = nil; the fallback exists so the row still renders a pace marker")
	}

	// The fallback is session-only: a weekly window with no reset instant keeps
	// an honest blank rather than a fabricated countdown.
	weekly := windowByLabel(t, p, "Weekly")
	if weekly.ResetText != "" || weekly.ResetMs != 0 || weekly.ResetAt != "" {
		t.Errorf("weekly got a fabricated reset: %q / %d / %q", weekly.ResetText, weekly.ResetMs, weekly.ResetAt)
	}
	if weekly.Pace != nil {
		t.Errorf("weekly pace = %+v, want nil without timing data", weekly.Pace)
	}
}

func TestFetchRetriesOn429(t *testing.T) {
	body := fixture(t, "usage-full.json")
	s := newStub(t, func(attempt int, w http.ResponseWriter) {
		if attempt == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		serveJSON(body)(attempt, w)
	}, nil)
	c, slept := newClient(s, testCreds())

	p := c.Fetch(context.Background())
	if p.Status != StatusOK {
		t.Fatalf("status = %q (%s), want ok after one 429", p.Status, p.Error)
	}
	s.mu.Lock()
	calls := s.usageCalls
	s.mu.Unlock()
	if calls != 2 {
		t.Errorf("usage requests = %d, want 2 (429 then 200)", calls)
	}
	if len(*slept) != 1 || (*slept)[0] != 2*time.Second {
		t.Errorf("backoff = %v, want one 2s wait from Retry-After", *slept)
	}
}

func TestFetch429ExhaustsRetries(t *testing.T) {
	s := newStub(t, func(_ int, w http.ResponseWriter) {
		w.WriteHeader(http.StatusTooManyRequests)
	}, nil)
	c, slept := newClient(s, testCreds())

	p := c.Fetch(context.Background())
	if p.Status != StatusError || !strings.Contains(p.Error, "429") {
		t.Fatalf("provider = %q / %q, want an error mentioning 429", p.Status, p.Error)
	}
	s.mu.Lock()
	calls := s.usageCalls
	s.mu.Unlock()
	if calls != maxRetries {
		t.Errorf("usage requests = %d, want %d", calls, maxRetries)
	}
	// No Retry-After header → exponential backoff from 1s.
	if len(*slept) != 2 || (*slept)[0] != time.Second || (*slept)[1] != 2*time.Second {
		t.Errorf("backoff = %v, want [1s 2s]", *slept)
	}
}

func TestFetchRefreshesOn401AndReusesToken(t *testing.T) {
	body := fixture(t, "usage-full.json")
	s := newStub(t, func(attempt int, w http.ResponseWriter) {
		if attempt == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		serveJSON(body)(attempt, w)
	}, nil)
	c, _ := newClient(s, testCreds())

	p := c.Fetch(context.Background())
	if p.Status != StatusOK {
		t.Fatalf("status = %q (%s), want ok after a refresh", p.Status, p.Error)
	}

	s.mu.Lock()
	if s.refreshCalls != 1 {
		t.Errorf("refresh calls = %d, want 1", s.refreshCalls)
	}
	if s.bearers[1] != "Bearer refreshed-token" {
		t.Errorf("retry bearer = %q, want the refreshed token", s.bearers[1])
	}
	rb := s.refreshBody
	s.mu.Unlock()

	if rb["grant_type"] != "refresh_token" || rb["refresh_token"] != fakeRefresh ||
		rb["client_id"] != oauthClientID || rb["scope"] != "user:inference "+requiredScope {
		t.Errorf("refresh body = %v, want the CLI's grant shape", rb)
	}

	// A second Fetch on the same client reuses the in-memory token: no second
	// refresh, and the first request already carries the refreshed bearer.
	p2 := c.Fetch(context.Background())
	if p2.Status != StatusOK {
		t.Fatalf("second fetch status = %q (%s)", p2.Status, p2.Error)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refreshCalls != 1 {
		t.Errorf("refresh calls after second fetch = %d, want 1", s.refreshCalls)
	}
	if s.usageCalls != 3 || s.bearers[2] != "Bearer refreshed-token" {
		t.Errorf("second fetch sent %d usage calls, last bearer %q", s.usageCalls, s.bearers[2])
	}
}

func TestFetchAuthRejected(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		creds      *Creds
		refreshErr bool
		wantCalls  int
	}{
		{"401 with no refresh token", http.StatusUnauthorized, func() *Creds {
			c := testCreds()
			c.RefreshToken = ""
			return c
		}(), false, 1},
		{"403 with no refresh token", http.StatusForbidden, func() *Creds {
			c := testCreds()
			c.RefreshToken = ""
			return c
		}(), false, 1},
		{"401 with a failing refresh", http.StatusUnauthorized, testCreds(), true, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var refresh func(int, http.ResponseWriter)
			if tc.refreshErr {
				refresh = func(_ int, w http.ResponseWriter) { w.WriteHeader(http.StatusInternalServerError) }
			}
			s := newStub(t, func(_ int, w http.ResponseWriter) { w.WriteHeader(tc.status) }, refresh)
			c, _ := newClient(s, tc.creds)

			p := c.Fetch(context.Background())
			wantSetupCard(t, p, HintLogin, "auth rejected")
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.usageCalls != tc.wantCalls {
				t.Errorf("usage requests = %d, want %d", s.usageCalls, tc.wantCalls)
			}
		})
	}
}

func TestFetchRefreshWithoutAccessTokenInResponse(t *testing.T) {
	s := newStub(t,
		func(_ int, w http.ResponseWriter) { w.WriteHeader(http.StatusUnauthorized) },
		func(_ int, w http.ResponseWriter) { _, _ = w.Write([]byte(`{"token_type":"bearer"}`)) })
	c, _ := newClient(s, testCreds())

	p := c.Fetch(context.Background())
	wantSetupCard(t, p, HintLogin, "auth rejected")
}

// TestFetchAuthRejectionDropsCachedToken pins the recovery path behind the
// re-login hint: once a refreshed token starts being rejected, it must not be
// replayed on every later poll (resolveToken prefers the cache and marks it
// already-refreshed, which suppresses the refresh retry). Dropping it means the
// next poll starts from the on-disk credential again, so a fresh `claude` login
// takes effect without restarting the daemon.
func TestFetchAuthRejectionDropsCachedToken(t *testing.T) {
	s := newStub(t, func(attempt int, w http.ResponseWriter) {
		if attempt == 1 {
			w.WriteHeader(http.StatusUnauthorized) // forces one refresh
			return
		}
		w.WriteHeader(http.StatusUnauthorized) // the refreshed token is rejected too
	}, nil)
	c, _ := newClient(s, testCreds())

	if p := c.Fetch(context.Background()); p.Status != StatusNoAuth {
		t.Fatalf("first fetch = %q (%s), want the setup card", p.Status, p.Error)
	}
	if tok := c.cachedToken(); tok != "" {
		t.Errorf("cached token = %q, want it dropped after a rejection", tok)
	}
}

func TestFetchRefreshAcceptsCamelCaseToken(t *testing.T) {
	body := fixture(t, "usage-full.json")
	s := newStub(t, func(attempt int, w http.ResponseWriter) {
		if attempt == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		serveJSON(body)(attempt, w)
	}, func(_ int, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"accessToken":"camel-token"}`))
	})
	c, _ := newClient(s, testCreds())

	if p := c.Fetch(context.Background()); p.Status != StatusOK {
		t.Fatalf("status = %q (%s), want ok", p.Status, p.Error)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bearers[1] != "Bearer camel-token" {
		t.Errorf("retry bearer = %q, want the camelCase token", s.bearers[1])
	}
}

func TestFetchExpiredTokenRefreshesUpFront(t *testing.T) {
	body := fixture(t, "usage-full.json")
	s := newStub(t, serveJSON(body), nil)
	creds := testCreds()
	creds.ExpiresAt = testNow.Add(-time.Hour).UnixMilli()
	c, _ := newClient(s, creds)

	p := c.Fetch(context.Background())
	if p.Status != StatusOK {
		t.Fatalf("status = %q (%s), want ok", p.Status, p.Error)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refreshCalls != 1 {
		t.Errorf("refresh calls = %d, want 1 before the usage request", s.refreshCalls)
	}
	if s.usageCalls != 1 || s.bearers[0] != "Bearer refreshed-token" {
		t.Errorf("first usage call used %q, want the refreshed token", s.bearers[0])
	}
}

// TestFetchRefreshesWithoutScopes pins the refresh path that the permissive
// scopes gate (claude.go) newly made reachable: a credential with a nil
// Scopes list and an already-expired ExpiresAt now proceeds past the gate
// into the up-front refresh (see TestFetchExpiredTokenRefreshesUpFront).
// It asserts both the outcome and the payload shape actually sent — the
// refresh request must legitimately omit the "scope" field for a scope-less
// credential (see the len(creds.Scopes) > 0 guard in refresh()).
func TestFetchRefreshesWithoutScopes(t *testing.T) {
	body := fixture(t, "usage-full.json")
	s := newStub(t, serveJSON(body), nil)
	creds := testCreds()
	creds.Scopes = nil
	creds.ExpiresAt = testNow.Add(-time.Hour).UnixMilli()
	c, _ := newClient(s, creds)

	p := c.Fetch(context.Background())
	if p.Status != StatusOK {
		t.Fatalf("status = %q (%s), want ok — a scope-less, expired credential must still refresh", p.Status, p.Error)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refreshCalls != 1 {
		t.Errorf("refresh calls = %d, want 1 before the usage request", s.refreshCalls)
	}
	if _, sent := s.refreshBody["scope"]; sent {
		t.Errorf("refresh body = %v, want no \"scope\" field for a scope-less credential", s.refreshBody)
	}
}

func TestFetchExpiredTokenFailures(t *testing.T) {
	t.Run("no refresh token", func(t *testing.T) {
		s := newStub(t, serveJSON([]byte(`{}`)), nil)
		creds := testCreds()
		creds.ExpiresAt = testNow.Add(-time.Hour).UnixMilli()
		creds.RefreshToken = ""
		c, _ := newClient(s, creds)

		p := c.Fetch(context.Background())
		wantSetupCard(t, p, HintLogin, "expired")
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.usageCalls != 0 {
			t.Errorf("usage requests = %d, want 0 — an expired token is not sent", s.usageCalls)
		}
	})

	t.Run("refresh rejected", func(t *testing.T) {
		s := newStub(t, serveJSON([]byte(`{}`)),
			func(_ int, w http.ResponseWriter) { w.WriteHeader(http.StatusBadRequest) })
		creds := testCreds()
		creds.ExpiresAt = testNow.Add(-time.Hour).UnixMilli()
		c, _ := newClient(s, creds)

		p := c.Fetch(context.Background())
		wantSetupCard(t, p, HintLogin, "refresh failed")
	})

	t.Run("refresh endpoint unreachable", func(t *testing.T) {
		s := newStub(t, serveJSON([]byte(`{}`)), nil)
		creds := testCreds()
		creds.ExpiresAt = testNow.Add(-time.Hour).UnixMilli()
		c, _ := newClient(s, creds)
		c.AuthBase = "http://exa\x7fmple.invalid" // unparseable as a URL

		p := c.Fetch(context.Background())
		wantSetupCard(t, p, HintLogin, "refresh failed")
	})
}

func TestFetchCredentialOutcomes(t *testing.T) {
	cases := []struct {
		name      string
		creds     *Creds
		err       error
		wantError string
	}{
		{"no credentials", nil, ErrNoCreds, "No Claude credentials"},
		{"unexpected load failure", nil, io.ErrUnexpectedEOF, "No Claude credentials"},
		{"nil credential without an error", nil, nil, "No Claude credentials"},
		{"credential without an access token", &Creds{Scopes: []string{requiredScope}}, nil, "No Claude credentials"},
		{"missing user:profile scope", &Creds{AccessToken: fakeAccess, Scopes: []string{"user:inference"}}, nil, "user:profile"},
		{"opted out", nil, ErrDisabled, "SWARMERY_USAGE_OAUTH=0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStub(t, serveJSON(fixture(t, "usage-full.json")), nil)
			c, _ := newClient(s, nil)
			c.LoadCreds = func(context.Context) (*Creds, error) { return tc.creds, tc.err }

			p := c.Fetch(context.Background())
			if p.Status != StatusNoAuth {
				t.Fatalf("status = %q, want no-auth", p.Status)
			}
			if !strings.Contains(p.Error, tc.wantError) {
				t.Errorf("error = %q, want it to mention %q", p.Error, tc.wantError)
			}
			if p.Windows == nil {
				t.Error("windows = nil, want an empty slice so the UI can map it")
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.usageCalls != 0 {
				t.Errorf("usage requests = %d, want 0 without a usable credential", s.usageCalls)
			}
		})
	}
}

// TestFetchAcceptsCredsWithoutScopes pins the permissive side of the scopes
// gate: the CLI does not always persist a scopes list to
// ~/.claude/.credentials.json, so an absent (nil) or empty Scopes slice must
// not be rejected up front — the request must reach the HTTP call, unlike the
// populated-but-wrong-scope case pinned in TestFetchCredentialOutcomes.
func TestFetchAcceptsCredsWithoutScopes(t *testing.T) {
	cases := []struct {
		name   string
		scopes []string
	}{
		{"nil scopes", nil},
		{"empty scopes slice", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStub(t, serveJSON(fixture(t, "usage-full.json")), nil)
			creds := testCreds()
			creds.Scopes = tc.scopes
			c, _ := newClient(s, creds)

			p := c.Fetch(context.Background())
			if p.Status != StatusOK {
				t.Fatalf("status = %q (%s), want ok — an absent/empty Scopes list must be permissive", p.Status, p.Error)
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.usageCalls != 1 {
				t.Errorf("usage requests = %d, want 1 — the request must reach the HTTP call", s.usageCalls)
			}
		})
	}
}

// TestFetchHonoursOptOutEndToEnd exercises the real LoadCreds (not the seam):
// with SWARMERY_USAGE_OAUTH=0 and a perfectly good credential file on disk,
// Fetch must still return no-auth.
func TestFetchHonoursOptOutEndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(configDirEnv, "")
	writeCredFile(t, filepath.Join(home, ".claude"))
	stubKeychain(t, func(context.Context) *Creds {
		t.Error("keychain consulted while SWARMERY_USAGE_OAUTH=0")
		return nil
	})

	s := newStub(t, serveJSON(fixture(t, "usage-full.json")), nil)
	c := &Client{
		HTTP:     s.srv.Client(),
		Now:      func() time.Time { return testNow },
		APIBase:  s.srv.URL,
		AuthBase: s.srv.URL,
	}

	// Control: the same client without the opt-out reaches the endpoint.
	if p := c.Fetch(context.Background()); p.Status != StatusOK {
		t.Fatalf("control fetch = %q (%s), want ok", p.Status, p.Error)
	}

	t.Setenv(oauthOptOutEnv, "0")
	p := c.Fetch(context.Background())
	if p.Status != StatusNoAuth || !strings.Contains(p.Error, "SWARMERY_USAGE_OAUTH=0") {
		t.Errorf("provider = %q / %q, want the opt-out card", p.Status, p.Error)
	}
}

// TestFetchScopedAccountSpeaksForItsOwnAccount exercises the real per-account
// resolution (no LoadCreds seam). A client pointed at an account's config dir
// must, when that account has no credential, render a CONNECT card for THAT
// account: its own key on the card, its own dir as the only place looked, and a
// command that actually logs THAT account in — a bare `claude` would re-login
// the default account and leave the card exactly as it was. Both decoys (the
// default account's file and CLAUDE_CONFIG_DIR) are populated, so any leak
// would show up here as a healthy card.
func TestFetchScopedAccountSpeaksForItsOwnAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCredFile(t, filepath.Join(home, ".claude"))
	t.Setenv(configDirEnv, filepath.Join(home, ".claude"))
	stubKeychain(t, func(context.Context) *Creds {
		t.Error("keychain consulted for a scoped account — that item is the default account's login")
		return nil
	})

	dir := filepath.Join(home, ".claude-nabu-org")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir account config dir: %v", err)
	}
	s := newStub(t, serveJSON(fixture(t, "usage-full.json")), nil)
	c := &Client{
		HTTP:     s.srv.Client(),
		Now:      func() time.Time { return testNow },
		APIBase:  s.srv.URL,
		AuthBase: s.srv.URL,
		Src:      Source{Account: "nabu-org", ConfigDir: dir},
	}

	p := c.Fetch(context.Background())
	wantSetupCard(t, p, HintLogin, "No Claude credentials")
	if p.Account != "nabu-org" {
		t.Errorf("card account = %q, want nabu-org", p.Account)
	}
	if want := configDirEnv + "=" + dir + " claude"; p.Hint.Command != want {
		t.Errorf("hint command = %q, want %q", p.Hint.Command, want)
	}
	if want := filepath.Join(dir, credentialsFile); len(p.Hint.Sources) != 1 || p.Hint.Sources[0] != want {
		t.Errorf("hint sources = %v, want exactly [%s]", p.Hint.Sources, want)
	}

	// …and once that account HAS logged in, the same client reads its quota.
	writeCredFile(t, dir)
	if got := c.Fetch(context.Background()); got.Status != StatusOK || got.Account != "nabu-org" {
		t.Errorf("after login: status %q account %q (%s), want ok/nabu-org", got.Status, got.Account, got.Error)
	}
}

func TestFetchNon200(t *testing.T) {
	longBody := "authorization: Bearer sk-ant-oat01-supersecret-value " + strings.Repeat("x", 300)
	cases := []struct {
		name       string
		status     int
		body       string
		wantPrefix string
	}{
		{"server error with a body", http.StatusInternalServerError, longBody, "HTTP 500: "},
		{"empty body", http.StatusBadGateway, "", "HTTP 502"},
		{"teapot", http.StatusTeapot, "no coffee here", "HTTP 418: no coffee here"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStub(t, func(_ int, w http.ResponseWriter) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}, nil)
			c, _ := newClient(s, testCreds())

			p := c.Fetch(context.Background())
			if p.Status != StatusError {
				t.Fatalf("status = %q, want error", p.Status)
			}
			if !strings.HasPrefix(p.Error, tc.wantPrefix) {
				t.Errorf("error = %q, want prefix %q", p.Error, tc.wantPrefix)
			}
			if strings.Contains(p.Error, "sk-ant-oat01-supersecret-value") {
				t.Fatalf("error leaked bearer material: %q", p.Error)
			}
			if n := len([]rune(p.Error)); n > snippetRunes+len("HTTP 500: ") {
				t.Errorf("error is %d runes, want the body snippet truncated", n)
			}
		})
	}
}

func TestFetchTransportAndPayloadFailures(t *testing.T) {
	t.Run("unparseable base URL", func(t *testing.T) {
		s := newStub(t, serveJSON([]byte(`{}`)), nil)
		c, _ := newClient(s, testCreds())
		c.APIBase = "http://exa\x7fmple.invalid"

		p := c.Fetch(context.Background())
		if p.Status != StatusError || !strings.Contains(p.Error, "request failed") {
			t.Errorf("provider = %q / %q, want a transport error", p.Status, p.Error)
		}
	})

	t.Run("server unreachable", func(t *testing.T) {
		s := newStub(t, serveJSON([]byte(`{}`)), nil)
		c, _ := newClient(s, testCreds())
		s.srv.Close()

		p := c.Fetch(context.Background())
		if p.Status != StatusError || !strings.Contains(p.Error, "request failed") {
			t.Errorf("provider = %q / %q, want a transport error", p.Status, p.Error)
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		s := newStub(t, serveJSON([]byte(`{"five_hour":`)), nil)
		c, _ := newClient(s, testCreds())

		p := c.Fetch(context.Background())
		if p.Status != StatusError || !strings.Contains(p.Error, "not valid JSON") {
			t.Errorf("provider = %q / %q, want a parse error", p.Status, p.Error)
		}
	})

	t.Run("empty but valid payload", func(t *testing.T) {
		s := newStub(t, serveJSON([]byte(`{}`)), nil)
		c, _ := newClient(s, testCreds())

		p := c.Fetch(context.Background())
		if p.Status != StatusOK {
			t.Fatalf("status = %q (%s), want ok", p.Status, p.Error)
		}
		if len(p.Windows) != 0 {
			t.Errorf("windows = %v, want none", labelsOf(p))
		}
	})
}

func TestClientDefaults(t *testing.T) {
	var c Client
	if got := c.apiBase(); got != defaultAPIBase {
		t.Errorf("apiBase() = %q, want %q", got, defaultAPIBase)
	}
	if got := c.authBase(); got != defaultAuthBase {
		t.Errorf("authBase() = %q, want %q", got, defaultAuthBase)
	}
	if got := c.httpClient(); got == nil || got.Timeout != defaultTimeout {
		t.Errorf("httpClient() timeout = %v, want %v", got.Timeout, defaultTimeout)
	}
	if c.now().IsZero() {
		t.Error("now() returned the zero time")
	}
	c.wait(0) // the real time.Sleep branch, with nothing to wait for

	withBase := Client{APIBase: "https://example.test/", AuthBase: "https://auth.test/"}
	if got := withBase.apiBase(); got != "https://example.test" {
		t.Errorf("apiBase() = %q, want the trailing slash trimmed", got)
	}
	if got := withBase.authBase(); got != "https://auth.test" {
		t.Errorf("authBase() = %q, want the trailing slash trimmed", got)
	}

	// The default LoadCreds path is the package function; with HOME pointed at
	// an empty dir it must report ErrNoCreds, not panic.
	t.Setenv("HOME", t.TempDir())
	t.Setenv(configDirEnv, "")
	stubKeychain(t, func(context.Context) *Creds { return nil })
	if _, err := c.loadCreds(context.Background()); err == nil {
		t.Error("loadCreds() = nil error, want ErrNoCreds from the real loader")
	}

	// refresh is a no-op without a refresh token.
	if _, ok := c.refresh(context.Background(), &Creds{}); ok {
		t.Error("refresh() succeeded without a refresh token")
	}
}

func TestTokenExpired(t *testing.T) {
	c := &Client{Now: func() time.Time { return testNow }}
	cases := []struct {
		name      string
		expiresAt int64
		want      bool
	}{
		{"unknown expiry is assumed valid", 0, false},
		{"negative expiry is assumed valid", -1, false},
		{"far future", testNow.Add(time.Hour).UnixMilli(), false},
		{"just outside the grace window", testNow.Add(61 * time.Second).UnixMilli(), false},
		{"inside the grace window", testNow.Add(30 * time.Second).UnixMilli(), true},
		{"already expired", testNow.Add(-time.Second).UnixMilli(), true},
	}
	for _, tc := range cases {
		if got := c.tokenExpired(tc.expiresAt); got != tc.want {
			t.Errorf("%s: tokenExpired(%d) = %v, want %v", tc.name, tc.expiresAt, got, tc.want)
		}
	}
}

func TestInferPlan(t *testing.T) {
	cases := []struct {
		name string
		in   Creds
		want string
	}{
		{"subscriptionType is title-cased", Creds{SubscriptionType: "max"}, "Max"},
		{"pro subscription", Creds{SubscriptionType: "pro"}, "Pro"},
		{"subscriptionType wins over tier", Creds{SubscriptionType: "team", RateLimitTier: "max_20x"}, "Team"},
		{"tier keyword max", Creds{RateLimitTier: "default_max_20x"}, "Max"},
		{"tier keyword pro", Creds{RateLimitTier: "PRO"}, "Pro"},
		{"tier keyword team", Creds{RateLimitTier: "team_premium_seat"}, "Team"},
		{"unrecognised tier is passed through", Creds{RateLimitTier: "enterprise"}, "enterprise"},
		{"nothing to infer from", Creds{}, ""},
		{"whitespace only", Creds{SubscriptionType: "  ", RateLimitTier: " "}, ""},
		{"non-ascii subscription type", Creds{SubscriptionType: "ünlimited"}, "Ünlimited"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			creds := tc.in
			if got := inferPlan(&creds); got != tc.want {
				t.Errorf("inferPlan(%v) = %q, want %q", creds, got, tc.want)
			}
		})
	}
}

func TestRetryDelay(t *testing.T) {
	cases := []struct {
		retryAfter string
		attempt    int
		want       time.Duration
	}{
		{"", 0, time.Second},
		{"", 1, 2 * time.Second},
		{"", 2, 4 * time.Second},
		{"3", 0, 3 * time.Second},
		{" 0 ", 0, 0},
		{"1.5", 0, 1500 * time.Millisecond},
		{"soon", 1, 2 * time.Second},                      // unparseable → backoff
		{"-5", 1, 2 * time.Second},                        // negative → backoff
		{"Wed, 21 Oct 2026 07:28:00 GMT", 0, time.Second}, // HTTP-date → backoff
	}
	for _, tc := range cases {
		if got := retryDelay(tc.retryAfter, tc.attempt); got != tc.want {
			t.Errorf("retryDelay(%q, %d) = %v, want %v", tc.retryAfter, tc.attempt, got, tc.want)
		}
	}
}

func TestParseResetTimestamp(t *testing.T) {
	cases := []struct {
		name       string
		value      any
		wantOK     bool
		wantMsLeft int64
		wantAt     string
	}{
		{"RFC3339", "2026-07-28T15:30:00Z", true, int64(3.5 * float64(time.Hour/time.Millisecond)), "2026-07-28T15:30:00Z"},
		{"RFC3339 with an offset", "2026-07-28T16:30:00+01:00", true, int64(3.5 * float64(time.Hour/time.Millisecond)), "2026-07-28T15:30:00Z"},
		{"unix seconds", float64(testNow.Add(time.Hour).Unix()), true, 3600 * 1000, "2026-07-28T13:00:00Z"},
		{"unix millis", float64(testNow.Add(time.Hour).UnixMilli()), true, 3600 * 1000, "2026-07-28T13:00:00Z"},
		{"numeric string in seconds", "1785247200", true, 2 * 3600 * 1000, "2026-07-28T14:00:00Z"},
		{"numeric string in millis", "1785247200000", true, 2 * 3600 * 1000, "2026-07-28T14:00:00Z"},
		{"nil", nil, false, 0, ""},
		{"empty string", "", false, 0, ""},
		{"whitespace", "   ", false, 0, ""},
		{"garbage", "next tuesday", false, 0, ""},
		{"already past", "2026-07-28T11:59:59Z", false, 0, ""},
		{"exactly now", float64(testNow.Unix()), false, 0, ""},
		{"NaN string", "NaN", false, 0, ""},
		{"wrong type", true, false, 0, ""},
		{"nested object", map[string]any{"at": 1}, false, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ms, at, ok := parseResetTimestamp(tc.value, testNow)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if ms != tc.wantMsLeft {
				t.Errorf("msLeft = %d, want %d", ms, tc.wantMsLeft)
			}
			if at != tc.wantAt {
				t.Errorf("resetAt = %q, want %q", at, tc.wantAt)
			}
		})
	}
}

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Session (5h)":           "session-5h",
		"Weekly":                 "weekly",
		"Weekly (Fable)":         "weekly-fable",
		"Weekly (Sonnet)":        "weekly-sonnet",
		"Weekly (Claude 5 Opus)": "weekly-claude-5-opus",
		"  spaced  ":             "spaced",
		"---":                    "",
	}
	for in, want := range cases {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSnippetScrubsAndTruncates(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		notWant string
	}{
		{"newlines collapse", "line one\nline two\r\tend", "line one line two  end", ""},
		{"bearer is redacted", "authorization: Bearer abc123def", "authorization: Bearer [redacted]", "abc123def"},
		{"lowercase bearer", "bearer abc123def", "bearer [redacted]", "abc123def"},
		{"api key is redacted", `{"key":"sk-ant-oat01-abcdef"}`, `{"key":"[redacted]"}`, "oat01"},
		{"clean body is untouched", "rate limit exceeded", "rate limit exceeded", ""},
		{"empty", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := snippet([]byte(tc.in))
			if got != tc.want {
				t.Errorf("snippet(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if tc.notWant != "" && strings.Contains(got, tc.notWant) {
				t.Errorf("snippet(%q) leaked %q", tc.in, tc.notWant)
			}
		})
	}

	long := strings.Repeat("é", 400)
	if got := []rune(snippet([]byte(long))); len(got) != snippetRunes {
		t.Errorf("snippet truncated to %d runes, want %d", len(got), snippetRunes)
	}
}

func TestParsePayloadTolerance(t *testing.T) {
	cases := []struct {
		name       string
		payload    string
		wantLabels []string
	}{
		{"limits is not an array", `{"limits":{"weekly":1}}`, nil},
		{"limits entries are null", `{"limits":[null,null]}`, nil},
		{"limit without a weekly group or kind", `{"limits":[{"group":"session","kind":"five_hour","percent":5,"scope":{"model":{"display_name":"Fable"}}}]}`, nil},
		{"limit with a weekly kind prefix", `{"limits":[{"kind":"weekly_scoped","percent":5,"scope":{"model":{"display_name":"Fable"}}}]}`, []string{"Weekly (Fable)"}},
		{"window key is not an object", `{"five_hour":42}`, nil},
		{"window key is null", `{"five_hour":null,"seven_day":{"utilization":5}}`, []string{"Weekly"}},
		{"session fallback key", `{"session":{"utilization":5}}`, []string{"Session (5h)"}},
		{"percentage clamps above 100", `{"seven_day":{"utilization":150}}`, []string{"Weekly"}},
		{"percentage clamps below 0", `{"seven_day":{"utilization":-20}}`, []string{"Weekly"}},
		{"unknown percentage key defaults to zero", `{"seven_day":{"consumed":42}}`, []string{"Weekly"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			windows, err := parsePayload([]byte(tc.payload), testNow)
			if err != nil {
				t.Fatalf("parsePayload: %v", err)
			}
			var got []string
			for _, w := range windows {
				got = append(got, w.Label)
				if w.PercentUsed < 0 || w.PercentUsed > 100 {
					t.Errorf("%s: percentUsed = %v, want it clamped to [0,100]", w.Label, w.PercentUsed)
				}
				if w.PercentUsed+w.PercentLeft != 100 {
					t.Errorf("%s: used + left != 100", w.Label)
				}
			}
			if strings.Join(got, "|") != strings.Join(tc.wantLabels, "|") {
				t.Errorf("labels = %v, want %v", got, tc.wantLabels)
			}
		})
	}
}

func TestParsePayloadRejectsNonObject(t *testing.T) {
	if _, err := parsePayload([]byte(`["not","an","object"]`), testNow); err == nil {
		t.Error("parsePayload(array) = nil error, want a decode failure")
	}
}
