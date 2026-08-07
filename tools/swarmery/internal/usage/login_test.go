package usage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── PKCE primitives ────────────────────────────────────────────────────────

// TestCodeChallengeMatchesRFC7636Vector pins S256 against the worked example in
// RFC 7636 Appendix B. A verifier/challenge pair that drifts from this vector is
// rejected by the authorization server with an error the operator cannot read,
// so it is worth pinning to a published constant rather than to our own output.
func TestCodeChallengeMatchesRFC7636Vector(t *testing.T) {
	const (
		verifier  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
		challenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	)
	if got := codeChallenge(verifier); got != challenge {
		t.Errorf("codeChallenge(RFC 7636 verifier) = %q, want %q", got, challenge)
	}
}

// TestRandomB64IsUnpaddedUrlSafeAndUnique: the CLI's primitive is
// base64url(randomBytes(32)) with the padding stripped. Padding or a "+"/"/"
// would have to be percent-encoded in the URL and breaks a naive paste.
func TestRandomB64IsUnpaddedUrlSafeAndUnique(t *testing.T) {
	seen := make(map[string]bool, 64)
	for i := 0; i < 64; i++ {
		got, err := randomB64()
		if err != nil {
			t.Fatalf("randomB64: %v", err)
		}
		if strings.ContainsAny(got, "+/=") {
			t.Fatalf("randomB64 = %q, want unpadded base64url", got)
		}
		// 32 bytes → ceil(32*4/3) = 43 unpadded characters.
		if len(got) != 43 {
			t.Fatalf("randomB64 = %q (%d chars), want 43 — that is not 32 bytes of entropy", got, len(got))
		}
		if seen[got] {
			t.Fatal("randomB64 repeated a value — the state parameter would not be a nonce")
		}
		seen[got] = true
	}
}

func TestSplitPastedCode(t *testing.T) {
	for _, tc := range []struct {
		name, in, code, state string
		ok                    bool
	}{
		{name: "code#state", in: "abc123#st4te", code: "abc123", state: "st4te", ok: true},
		{name: "surrounding whitespace", in: "  abc123#st4te\n", code: "abc123", state: "st4te", ok: true},
		{name: "whitespace around each half", in: "abc123 # st4te", code: "abc123", state: "st4te", ok: true},
		{
			// A state that itself contains '#' cannot occur (base64url), but
			// splitting on the FIRST separator is what the CLI does.
			name: "splits on the first separator",
			in:   "abc#st4te#extra", code: "abc", state: "st4te#extra", ok: true,
		},
		{name: "no separator at all", in: "abc123"},
		{name: "empty state", in: "abc123#"},
		{name: "empty code", in: "#st4te"},
		{name: "separator only", in: "#"},
		{name: "empty", in: ""},
		{name: "whitespace only", in: "   "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, state, ok := splitPastedCode(tc.in)
			if ok != tc.ok {
				t.Fatalf("splitPastedCode(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			}
			if !tc.ok {
				return
			}
			if code != tc.code || state != tc.state {
				t.Errorf("splitPastedCode(%q) = (%q, %q), want (%q, %q)", tc.in, code, state, tc.code, tc.state)
			}
		})
	}
}

// ── the authorize step ─────────────────────────────────────────────────────

// TestStartLoginBuildsTheAuthorizeURL pins every parameter against what the
// `claude` CLI's own buildAuthUrl sends (see the evidence block in login.go).
// A drift here is invisible until an operator's browser lands on an error page.
func TestStartLoginBuildsTheAuthorizeURL(t *testing.T) {
	c := &Client{
		AuthBase:  "https://auth.test",
		LoginBase: "https://login.test",
		Src:       Source{Account: "nabu-org", ConfigDir: t.TempDir()},
	}

	flow, err := c.StartLogin()
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	if flow.Account != "nabu-org" {
		t.Errorf("flow account = %q, want nabu-org", flow.Account)
	}

	u, err := url.Parse(flow.AuthorizeURL)
	if err != nil {
		t.Fatalf("parse authorize URL: %v", err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != "https://login.test"+authorizePath {
		t.Errorf("authorize endpoint = %q, want %q", got, "https://login.test"+authorizePath)
	}

	q := u.Query()
	for _, tc := range []struct{ key, want string }{
		{"code", "true"},
		{"client_id", oauthClientID},
		{"response_type", "code"},
		{"redirect_uri", "https://auth.test" + manualRedirectPath},
		{"code_challenge_method", "S256"},
		{"code_challenge", codeChallenge(flow.Verifier)},
		{"state", flow.State},
		{"scope", strings.Join(loginScopes, " ")},
	} {
		if got := q.Get(tc.key); got != tc.want {
			t.Errorf("authorize %s = %q, want %q", tc.key, got, tc.want)
		}
	}

	// The URL is served to a browser: it must carry the CHALLENGE and never the
	// verifier, or PKCE proves nothing.
	if strings.Contains(flow.AuthorizeURL, flow.Verifier) {
		t.Error("authorize URL leaks the PKCE verifier")
	}
	// Requesting the API-key scope would let this credential mint API keys. The
	// CLI's console flow needs it; swarmery never does.
	if strings.Contains(q.Get("scope"), "org:create_api_key") {
		t.Error("login requests org:create_api_key — swarmery must not hold the power to mint API keys")
	}
	if !strings.Contains(q.Get("scope"), requiredScope) {
		t.Errorf("login scopes %q omit %q, which the quota endpoint requires", q.Get("scope"), requiredScope)
	}
}

// TestStartLoginDefaultsToTheVerifiedHosts: with no overrides the flow points at
// the endpoints extracted from the CLI bundle, not at anything invented.
func TestStartLoginDefaultsToTheVerifiedHosts(t *testing.T) {
	c := &Client{Src: Source{Account: "default"}}
	flow, err := c.StartLogin()
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	const wantPrefix = "https://claude.com/cai/oauth/authorize?"
	if !strings.HasPrefix(flow.AuthorizeURL, wantPrefix) {
		t.Errorf("authorize URL = %q, want it to start with %q", flow.AuthorizeURL, wantPrefix)
	}
	u, err := url.Parse(flow.AuthorizeURL)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := u.Query().Get("redirect_uri"); got != "https://platform.claude.com/oauth/code/callback" {
		t.Errorf("redirect_uri = %q, want the CLI's MANUAL_REDIRECT_URL", got)
	}
}

// TestStartLoginMintsAFreshFlowEachTime: reusing a verifier or a state across
// authorizations would make the CSRF check replayable.
func TestStartLoginMintsAFreshFlowEachTime(t *testing.T) {
	c := &Client{Src: Source{Account: "nabu-org"}}
	a, err := c.StartLogin()
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	b, err := c.StartLogin()
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	if a.Verifier == b.Verifier || a.State == b.State {
		t.Error("two flows share a verifier or a state")
	}
}

func TestStartLoginHonoursOptOut(t *testing.T) {
	t.Setenv(oauthOptOutEnv, "0")
	c := &Client{Src: Source{Account: "nabu-org"}}
	if _, err := c.StartLogin(); !errors.Is(err, ErrDisabled) {
		t.Fatalf("StartLogin error = %v, want ErrDisabled", err)
	}
}

// ── the exchange step ──────────────────────────────────────────────────────

const (
	loginFixtureAccess  = "NOT-A-REAL-TOKEN-login-access"
	loginFixtureRefresh = "NOT-A-REAL-TOKEN-login-refresh"
	loginFixtureCode    = "NOT-A-REAL-AUTHORIZATION-CODE"
)

// loginStub stands in for the token endpoint and the profile endpoint at once,
// recording exactly what the exchange sent.
type loginStub struct {
	srv *httptest.Server

	mu           sync.Mutex
	tokenCalls   int
	profileCalls int
	tokenBody    map[string]string
	profileAuth  string

	// tokenStatus/tokenPayload let a test make the exchange fail.
	tokenStatus  int
	tokenPayload string
	// profileStatus lets a test make the (best-effort) profile fetch fail.
	profileStatus int
}

func newLoginStub(t *testing.T) *loginStub {
	t.Helper()
	s := &loginStub{
		tokenStatus:   http.StatusOK,
		profileStatus: http.StatusOK,
		tokenPayload: `{"access_token":"` + loginFixtureAccess + `",` +
			`"refresh_token":"` + loginFixtureRefresh + `",` +
			`"expires_in":28800,"scope":"user:profile user:inference"}`,
	}
	mux := http.NewServeMux()
	mux.HandleFunc(tokenPath, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]string
		_ = json.Unmarshal(body, &parsed)
		s.mu.Lock()
		s.tokenCalls++
		s.tokenBody = parsed
		status, payload := s.tokenStatus, s.tokenPayload
		s.mu.Unlock()
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(payload))
	})
	mux.HandleFunc(profilePath, func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.profileCalls++
		s.profileAuth = r.Header.Get("authorization")
		status := s.profileStatus
		s.mu.Unlock()
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"organization":{"rate_limit_tier":"default_claude_max_20x"}}`))
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func (s *loginStub) sentTokenBody() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.tokenBody))
	for k, v := range s.tokenBody {
		out[k] = v
	}
	return out
}

func (s *loginStub) sentProfileAuth() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.profileAuth
}

func (s *loginStub) tokenCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokenCalls
}

// newLoginClient wires a client at the stub for both the token and the profile
// call, with a pinned clock so the derived expiry is deterministic.
func newLoginClient(s *loginStub, account string) *Client {
	return &Client{
		HTTP:     s.srv.Client(),
		Now:      func() time.Time { return testNow },
		APIBase:  s.srv.URL,
		AuthBase: s.srv.URL,
		Src:      Source{Account: account},
	}
}

// TestCompleteLoginPersistsTheConnection is the whole rung-2 happy path: the
// exchange sends exactly what the CLI sends, the plan metadata is picked up, and
// the credential lands in swarmery's own store where the next poll will find it.
func TestCompleteLoginPersistsTheConnection(t *testing.T) {
	dir := useTempStore(t)
	s := newLoginStub(t)
	c := newLoginClient(s, "nabu-org")

	flow, err := c.StartLogin()
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	if err := c.CompleteLogin(context.Background(), flow, loginFixtureCode+"#"+flow.State); err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}

	// The grant is the authorization_code grant, JSON-bodied, carrying the
	// verifier, the state and the SAME redirect_uri the authorize step used.
	sent := s.sentTokenBody()
	for _, tc := range []struct{ key, want string }{
		{"grant_type", "authorization_code"},
		{"code", loginFixtureCode},
		{"client_id", oauthClientID},
		{"code_verifier", flow.Verifier},
		{"state", flow.State},
		{"redirect_uri", c.redirectURI()},
	} {
		if sent[tc.key] != tc.want {
			t.Errorf("token request %s = %q, want %q", tc.key, sent[tc.key], tc.want)
		}
	}

	got := storedCreds("nabu-org")
	if got == nil {
		t.Fatal("no credential in the store after a successful login")
	}
	if got.AccessToken != loginFixtureAccess || got.RefreshToken != loginFixtureRefresh {
		t.Error("the stored credential is not the one the exchange returned (tokens elided)")
	}
	if !got.FromStore {
		t.Error("FromStore = false — the refresh path would then refuse to persist rotation")
	}
	if want := testNow.Add(28800 * time.Second).UnixMilli(); got.ExpiresAt != want {
		t.Errorf("expiresAt = %d, want %d (now + expires_in)", got.ExpiresAt, want)
	}
	if !hasScope(got.Scopes, requiredScope) {
		t.Errorf("stored scopes = %v, want the endpoint's echo including %q", got.Scopes, requiredScope)
	}
	// The plan chip: taken from the profile endpoint, exactly as the CLI does.
	if got.RateLimitTier != "default_claude_max_20x" {
		t.Errorf("rateLimitTier = %q, want the profile endpoint's value", got.RateLimitTier)
	}
	if plan := inferPlan(got); plan != "Max" {
		t.Errorf("inferPlan = %q, want %q — the connected card would show no plan", plan, "Max")
	}
	if got := s.sentProfileAuth(); got != "Bearer "+loginFixtureAccess {
		t.Errorf("profile fetch authorization = %q, want the freshly issued bearer", got)
	}

	// Hygiene: the file is 0600 and lives where the hint says it does.
	fi, err := os.Stat(filepath.Join(dir, "nabu-org.json"))
	if err != nil {
		t.Fatalf("stat stored credential: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != storeFileMode {
		t.Errorf("stored credential mode = %o, want %o", perm, storeFileMode)
	}
}

// TestCompleteLoginMakesTheAccountResolvable closes the loop: after connecting,
// LoadCredsFor answers with the stored credential for an account that has NO
// credential file anywhere — the macOS non-default case rung 2 exists for.
func TestCompleteLoginMakesTheAccountResolvable(t *testing.T) {
	configDir := scopedFixture(t) // decoys everywhere, nothing in the account's dir
	useTempStore(t)
	// The plain item resolves (the default account is logged in); the scoped
	// account's suffixed item is empty — so before Connect there is genuinely
	// nothing to read for THIS account.
	stubKeychain(t, func(_ context.Context, service string) *Creds {
		if service == keychainService {
			return &Creds{AccessToken: "from-the-keychain"}
		}
		return nil
	})
	s := newLoginStub(t)
	c := newLoginClient(s, "nabu-org")
	src := Source{Account: "nabu-org", ConfigDir: configDir}

	// Before: nothing to read.
	if _, err := LoadCredsFor(context.Background(), src); !errors.Is(err, ErrNoCreds) {
		t.Fatalf("before connecting: LoadCredsFor error = %v, want ErrNoCreds", err)
	}

	flow, err := c.StartLogin()
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	if err := c.CompleteLogin(context.Background(), flow, loginFixtureCode+"#"+flow.State); err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}

	got, err := LoadCredsFor(context.Background(), src)
	if err != nil {
		t.Fatalf("after connecting: LoadCredsFor: %v", err)
	}
	if got.AccessToken != loginFixtureAccess {
		t.Errorf("resolved %q, want the connected credential", got.AccessToken)
	}
}

// TestCompleteLoginRejections: every way a completion can legitimately fail, and
// the proof that none of them writes a credential.
func TestCompleteLoginRejections(t *testing.T) {
	for _, tc := range []struct {
		name string
		// pasted is built from the live flow's state when useState is true.
		pasted   string
		useState bool
		nilFlow  bool
		optOut   bool
		want     error
	}{
		{name: "no separator", pasted: loginFixtureCode, want: ErrLoginCodeFormat},
		{name: "empty state half", pasted: loginFixtureCode + "#", want: ErrLoginCodeFormat},
		{name: "empty code half", pasted: "#somestate", want: ErrLoginCodeFormat},
		{name: "blank paste", pasted: "   ", want: ErrLoginCodeFormat},
		{
			name:   "state from a different authorization",
			pasted: loginFixtureCode + "#NOT-THE-STATE-THIS-FLOW-ISSUED",
			want:   ErrLoginStateMismatch,
		},
		{name: "no flow at all", nilFlow: true, pasted: loginFixtureCode + "#x", want: ErrLoginStateMismatch},
		{name: "opted out", useState: true, optOut: true, want: ErrDisabled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useTempStore(t)
			s := newLoginStub(t)
			c := newLoginClient(s, "nabu-org")

			flow, err := c.StartLogin()
			if err != nil {
				t.Fatalf("StartLogin: %v", err)
			}
			pasted := tc.pasted
			if tc.useState {
				pasted = loginFixtureCode + "#" + flow.State
			}
			if tc.nilFlow {
				flow = nil
			}
			if tc.optOut {
				t.Setenv(oauthOptOutEnv, "0")
			}

			if err := c.CompleteLogin(context.Background(), flow, pasted); !errors.Is(err, tc.want) {
				t.Fatalf("CompleteLogin error = %v, want %v", err, tc.want)
			}
			if got := storedCreds("nabu-org"); got != nil {
				t.Error("a rejected login still wrote a credential")
			}
			if n := s.tokenCallCount(); n != 0 {
				t.Errorf("token endpoint called %d times on a rejected login, want 0", n)
			}
		})
	}
}

// TestCompleteLoginExchangeFailuresAreOpaque: whatever the token endpoint says,
// the caller gets one fixed sentinel. The upstream body is attacker-influenced
// text that would otherwise reach the operator's browser (R3).
func TestCompleteLoginExchangeFailuresAreOpaque(t *testing.T) {
	const leak = "sk-ant-LEAKED-FROM-THE-UPSTREAM-BODY"
	for _, tc := range []struct {
		name    string
		status  int
		payload string
	}{
		{"401 invalid code", http.StatusUnauthorized, `{"error":"invalid_grant","detail":"` + leak + `"}`},
		{"500 upstream", http.StatusInternalServerError, leak},
		{"200 with an unparseable body", http.StatusOK, `<html>` + leak + `</html>`},
		{"200 with no access token", http.StatusOK, `{"refresh_token":"r","expires_in":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useTempStore(t)
			s := newLoginStub(t)
			s.tokenStatus, s.tokenPayload = tc.status, tc.payload
			c := newLoginClient(s, "nabu-org")

			flow, err := c.StartLogin()
			if err != nil {
				t.Fatalf("StartLogin: %v", err)
			}
			err = c.CompleteLogin(context.Background(), flow, loginFixtureCode+"#"+flow.State)
			if !errors.Is(err, ErrLoginExchange) {
				t.Fatalf("CompleteLogin error = %v, want ErrLoginExchange", err)
			}
			if strings.Contains(err.Error(), leak) || strings.Contains(err.Error(), loginFixtureCode) {
				t.Errorf("error text leaks upstream body or the code: %q", err)
			}
			if got := storedCreds("nabu-org"); got != nil {
				t.Error("a failed exchange still wrote a credential")
			}
		})
	}
}

// TestCompleteLoginSurvivesAFailedProfileFetch: the plan chip is cosmetic, so a
// profile endpoint that is down must not cost the operator the connection they
// just authorized.
func TestCompleteLoginSurvivesAFailedProfileFetch(t *testing.T) {
	useTempStore(t)
	s := newLoginStub(t)
	s.profileStatus = http.StatusServiceUnavailable
	c := newLoginClient(s, "nabu-org")

	flow, err := c.StartLogin()
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	if err := c.CompleteLogin(context.Background(), flow, loginFixtureCode+"#"+flow.State); err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}
	got := storedCreds("nabu-org")
	if got == nil {
		t.Fatal("no credential stored after a failed profile fetch")
	}
	if got.RateLimitTier != "" {
		t.Errorf("rateLimitTier = %q, want it left empty rather than guessed", got.RateLimitTier)
	}
}

// TestCompleteLoginFallsBackToRequestedScopes: the endpoint has shipped token
// responses with no `scope` echo. Recording what we asked for keeps the refresh
// grant (which replays the stored scopes) from silently narrowing the session.
func TestCompleteLoginFallsBackToRequestedScopes(t *testing.T) {
	useTempStore(t)
	s := newLoginStub(t)
	s.tokenPayload = `{"access_token":"` + loginFixtureAccess + `","refresh_token":"r","expires_in":3600}`
	c := newLoginClient(s, "nabu-org")

	flow, err := c.StartLogin()
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	if err := c.CompleteLogin(context.Background(), flow, loginFixtureCode+"#"+flow.State); err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}
	got := storedCreds("nabu-org")
	if got == nil {
		t.Fatal("no credential stored")
	}
	if len(got.Scopes) != len(loginScopes) {
		t.Errorf("stored scopes = %v, want the requested set %v", got.Scopes, loginScopes)
	}
}

// TestCompleteLoginReportsAnUnwritableStore: a connection that cannot be
// persisted is a FAILED connection — reporting success would leave the operator
// with a card that reverts at the next restart and no idea why.
func TestCompleteLoginReportsAnUnwritableStore(t *testing.T) {
	useTempStore(t)
	s := newLoginStub(t)
	c := newLoginClient(s, "nabu-org")

	boom := errors.New("store is unwritable")
	prev := storeCreateTemp
	storeCreateTemp = func(string, string) (*os.File, error) { return nil, boom }
	t.Cleanup(func() { storeCreateTemp = prev })

	flow, err := c.StartLogin()
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	if err := c.CompleteLogin(context.Background(), flow, loginFixtureCode+"#"+flow.State); !errors.Is(err, boom) {
		t.Fatalf("CompleteLogin error = %v, want the store write failure", err)
	}
}

// ── refresh write-back is decided by provenance ────────────────────────────

// TestRefreshPersistsRotationForStoredCreds: swarmery owns this credential, so a
// rotated refresh token MUST reach disk — the old one is already dead upstream,
// and without the write the connection breaks at the next daemon restart.
func TestRefreshPersistsRotationForStoredCreds(t *testing.T) {
	useTempStore(t)
	const rotated = "NOT-A-REAL-TOKEN-rotated-refresh"
	s := newStub(t, serveJSON(fixture(t, "usage-full.json")), func(_ int, w http.ResponseWriter) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"refreshed-token","refresh_token":"` + rotated + `","expires_in":3600}`))
	})

	seed := storeFixtureCreds()
	seed.ExpiresAt = testNow.Add(-time.Hour).UnixMilli() // expired → refresh up front
	if err := writeStoredCreds("nabu-org", seed); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	c := &Client{
		HTTP:     s.srv.Client(),
		Now:      func() time.Time { return testNow },
		APIBase:  s.srv.URL,
		AuthBase: s.srv.URL,
		Src:      Source{Account: "nabu-org"},
	}
	if p := c.Fetch(context.Background()); p.Status != StatusOK {
		t.Fatalf("fetch status = %q (%s), want ok", p.Status, p.Error)
	}

	got := storedCreds("nabu-org")
	if got == nil {
		t.Fatal("the stored credential disappeared across a refresh")
	}
	if got.RefreshToken != rotated {
		t.Error("the rotated refresh token was not persisted — this connection dies at the next restart")
	}
	if got.AccessToken != "refreshed-token" {
		t.Error("the refreshed access token was not persisted")
	}
	if want := testNow.Add(3600 * time.Second).UnixMilli(); got.ExpiresAt != want {
		t.Errorf("persisted expiresAt = %d, want %d", got.ExpiresAt, want)
	}
	// Metadata the refresh response does not carry must survive untouched.
	if got.SubscriptionType != seed.SubscriptionType || got.RateLimitTier != seed.RateLimitTier {
		t.Error("the refresh write-back dropped the plan metadata")
	}
}

// TestRefreshNeverWritesBackFileCreds is the other half of the provenance rule,
// and the one that protects the operator's `claude` login: the CLI is the other
// writer, and a refresh token we rotated behind its back can strand it. The
// account's file must be byte-identical after a refresh, and no swarmery-store
// file may appear for it either.
func TestRefreshNeverWritesBackFileCreds(t *testing.T) {
	storeDir := useTempStore(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(configDirEnv, "")
	stubKeychain(t, func(context.Context, string) *Creds { return nil })

	configDir := filepath.Join(home, ".claude-nabu-org")
	writeCredFileAt(t, configDir, "from-the-config-dir-file", testNow.Add(-time.Hour))
	credPath := filepath.Join(configDir, credentialsFile)
	before, err := os.ReadFile(credPath)
	if err != nil {
		t.Fatalf("read seeded credential file: %v", err)
	}

	s := newStub(t, serveJSON(fixture(t, "usage-full.json")), func(_ int, w http.ResponseWriter) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"refreshed-token","refresh_token":"ROTATED-BEHIND-THE-CLIS-BACK","expires_in":3600}`))
	})
	c := &Client{
		HTTP:     s.srv.Client(),
		Now:      func() time.Time { return testNow },
		APIBase:  s.srv.URL,
		AuthBase: s.srv.URL,
		Src:      Source{Account: "nabu-org", ConfigDir: configDir},
	}
	if p := c.Fetch(context.Background()); p.Status != StatusOK {
		t.Fatalf("fetch status = %q (%s), want ok", p.Status, p.Error)
	}
	// The refresh really happened — otherwise this test passes vacuously.
	if s.refreshCalls == 0 {
		t.Fatal("no refresh was attempted; the write-back rule is untested")
	}

	after, err := os.ReadFile(credPath)
	if err != nil {
		t.Fatalf("read credential file after refresh: %v", err)
	}
	if string(after) != string(before) {
		t.Error("a refresh rewrote the CLI's own credential file — that can strand the operator's login")
	}
	if _, err := os.Stat(filepath.Join(storeDir, "nabu-org.json")); !os.IsNotExist(err) {
		t.Error("a refresh of a file-sourced credential created a swarmery-store file")
	}
}

// TestRefreshWithoutAnAccountKeyDoesNotPersist: a client with no account (the
// legacy single-account seam) has nowhere to write, and must not try.
func TestRefreshWithoutAnAccountKeyDoesNotPersist(t *testing.T) {
	storeDir := useTempStore(t)
	s := newStub(t, serveJSON(fixture(t, "usage-full.json")), nil)
	creds := testCreds()
	creds.FromStore = true
	creds.ExpiresAt = testNow.Add(-time.Hour).UnixMilli()
	c, _ := newClient(s, creds) // Src is the zero Source

	if p := c.Fetch(context.Background()); p.Status != StatusOK {
		t.Fatalf("fetch status = %q (%s), want ok", p.Status, p.Error)
	}
	entries, err := os.ReadDir(storeDir)
	if err == nil && len(entries) != 0 {
		t.Errorf("store holds %d entries, want none for an account-less client", len(entries))
	}
}

// ── disconnect ─────────────────────────────────────────────────────────────

// TestDisconnectRemovesOnlySwarmerysOwnCredential is the whole promise of the
// Disconnect action: it removes the file SWARMERY wrote and nothing else. The
// CLI's own credential for the same account sits right beside it here and must
// survive — after the disconnect the account simply resolves through the CLI
// again, which is the fallback the resolution chain has always had.
func TestDisconnectRemovesOnlySwarmerysOwnCredential(t *testing.T) {
	store := useTempStore(t)
	cliDir := filepath.Join(t.TempDir(), ".claude-nabu-org")
	writeCredFileAt(t, cliDir, "NOT-A-REAL-TOKEN-cli-access", testNow.Add(8*time.Hour))
	if err := writeStoredCreds("nabu-org", storeFixtureCreds()); err != nil {
		t.Fatalf("writeStoredCreds: %v", err)
	}

	c := &Client{
		Now: func() time.Time { return testNow },
		Src: Source{Account: "nabu-org", ConfigDir: cliDir},
	}
	// A bearer refreshed during the connected session. It must not outlive the
	// credential that justified it.
	c.cacheToken("NOT-A-REAL-TOKEN-refreshed")

	// Precondition: while connected, the store is what resolves.
	before, err := LoadCredsFor(context.Background(), c.Src)
	if err != nil || before == nil || !before.FromStore {
		t.Fatalf("LoadCredsFor before disconnect = (%v, %v), want the stored credential", before, err)
	}

	if err := c.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	if _, err := os.Stat(filepath.Join(store, "nabu-org.json")); !os.IsNotExist(err) {
		t.Errorf("swarmery's own credential survived the disconnect (stat err = %v)", err)
	}
	if _, err := os.Stat(filepath.Join(cliDir, credentialsFile)); err != nil {
		t.Errorf("the CLI's own credential was touched: %v", err)
	}
	if c.cachedToken() != "" {
		t.Error("the refreshed bearer survived the disconnect — it would outlive the credential")
	}

	after, err := LoadCredsFor(context.Background(), c.Src)
	if err != nil || after == nil {
		t.Fatalf("LoadCredsFor after disconnect = (%v, %v), want the CLI credential", after, err)
	}
	if after.FromStore {
		t.Error("resolution still reports a store credential after the disconnect")
	}

	// Idempotent: an account with nothing stored is already in the state the
	// caller is asking for.
	if err := c.Disconnect(); err != nil {
		t.Errorf("second Disconnect = %v, want nil — disconnect is idempotent", err)
	}
}

// TestDisconnectHonoursOptOut: the kill switch covers the whole OAuth surface,
// and that includes the one call that DELETES a credential — an operator who
// switched the surface off gets a refusal, not a silent removal.
func TestDisconnectHonoursOptOut(t *testing.T) {
	store := useTempStore(t)
	if err := writeStoredCreds("nabu-org", storeFixtureCreds()); err != nil {
		t.Fatalf("writeStoredCreds: %v", err)
	}
	t.Setenv(oauthOptOutEnv, "0")

	c := &Client{Src: Source{Account: "nabu-org"}}
	if err := c.Disconnect(); !errors.Is(err, ErrDisabled) {
		t.Errorf("Disconnect = %v, want ErrDisabled", err)
	}
	if _, err := os.Stat(filepath.Join(store, "nabu-org.json")); err != nil {
		t.Errorf("a credential was removed while the OAuth surface was switched off: %v", err)
	}
}

// TestDisconnectWithoutAResolvableStore: no account key means no store path and
// nothing to remove. Reported rather than silently treated as done — the caller
// must not tell the operator a connection is gone when nothing was even looked
// at.
func TestDisconnectWithoutAResolvableStore(t *testing.T) {
	useTempStore(t)
	c := &Client{}
	if err := c.Disconnect(); !errors.Is(err, errNoStore) {
		t.Errorf("Disconnect = %v, want errNoStore for an account-less client", err)
	}
}
