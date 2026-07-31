package api

// Tests for the Connect-an-account endpoints (usage_login.go).
//
// # Isolation contract (extends the one in usage_test.go)
//
// These are the only api endpoints that WRITE a credential, so the isolation
// bar is higher than for the read path: TestMain below points swarmery's own
// credential store at a throwaway directory for the whole test binary, so no
// test — present or future — can write into the operator's real ~/.swarmery.
// The upstream token/profile endpoints are an httptest stub reached through
// usage.Client's APIBase/AuthBase seams; the authorize URL is only ever built,
// never fetched, because in production the operator's browser opens it.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/ingest"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/usage"
)

// TestMain is a hard safety guard for the whole api test binary: the login
// endpoints persist credentials, and nothing here may land in the operator's
// real ~/.swarmery/credentials. Individual tests narrow it further to their own
// t.TempDir; this is the floor that applies even to a test that forgets.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "api-test-credentials")
	if err != nil {
		fmt.Fprintln(os.Stderr, "api: cannot create temp credential store:", err)
		os.Exit(1)
	}
	os.Setenv("SWARMERY_CREDENTIALS_DIR", dir)
	os.Unsetenv("SWARMERY_USAGE_OAUTH")

	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

const (
	loginTestCode    = "NOT-A-REAL-AUTHORIZATION-CODE"
	loginTestAccess  = "sk-ant-oat01-FAKE-LOGIN-ACCESS-TOKEN"
	loginTestRefresh = "sk-ant-ort01-FAKE-LOGIN-REFRESH-TOKEN"
)

// useTempCredentialStore scopes the credential store to one test.
func useTempCredentialStore(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "credentials")
	t.Setenv("SWARMERY_CREDENTIALS_DIR", dir)
	return dir
}

// loginUpstream stubs the Anthropic token + profile endpoints the exchange
// calls. Everything else 404s, so an unexpected call is a visible failure.
// It reuses usageStub only for its call counter and server handle.
func loginUpstream(t *testing.T, status int) *usageStub {
	t.Helper()
	s := &usageStub{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/oauth/token", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		s.calls++
		s.mu.Unlock()
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"access_token":%q,"refresh_token":%q,"expires_in":28800,"scope":"user:profile"}`,
			loginTestAccess, loginTestRefresh)
	})
	mux.HandleFunc("/api/oauth/profile", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"organization":{"rate_limit_tier":"default_claude_max_20x"}}`))
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

// installLoginClient points ONE account's client at the upstream stub, with the
// real credential resolution left in place (no LoadCreds seam): the login path
// must exercise the actual store read/write, which is the behaviour under test.
func installLoginClient(t *testing.T, src usage.Source, up *usageStub) {
	t.Helper()
	c := &usage.Client{
		HTTP:     up.srv.Client(),
		Now:      func() time.Time { return usageStubNow },
		APIBase:  up.srv.URL,
		AuthBase: up.srv.URL,
		Src:      src,
	}
	if src.ConfigDir == "" {
		prev := usageClient
		usageClient = c
		t.Cleanup(func() { usageClient = prev })
	} else {
		usageClientsMu.Lock()
		prev, had := usageClients[src.Account]
		usageClients[src.Account] = c
		usageClientsMu.Unlock()
		t.Cleanup(func() {
			usageClientsMu.Lock()
			if had {
				usageClients[src.Account] = prev
			} else {
				delete(usageClients, src.Account)
			}
			usageClientsMu.Unlock()
		})
	}
	resetUsageCache()
	resetPendingLogins()
	t.Cleanup(func() {
		resetUsageCache()
		resetPendingLogins()
	})
}

// postLogin issues a POST with an optional JSON body and returns status + body.
func postLogin(t *testing.T, url, body string) (int, string) {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("{}")
	} else {
		reader = strings.NewReader(body)
	}
	resp, err := http.Post(url, "application/json", reader)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, string(raw)
}

// TestUsageLoginStartAndComplete is the whole rung-2 dashboard flow: start
// returns an authorize URL (and nothing else), complete exchanges the pasted
// "code#state" value, and the account ends up with a credential in swarmery's
// own store — which is exactly what turns its card from "Connect" into a live
// quota reading.
func TestUsageLoginStartAndComplete(t *testing.T) {
	store := useTempCredentialStore(t)
	_, srv := usageTestDB(t, "usage-login.db", time.Now().UTC().Format("2006-01-02T15:04:05"))
	dirs := attachAccountRoots(t, ingest.DefaultAccount, "nabu-org")
	up := loginUpstream(t, http.StatusOK)
	src := usage.Source{Account: "nabu-org", ConfigDir: dirs["nabu-org"]}
	installLoginClient(t, src, up)

	status, body := postLogin(t, srv.URL+"/api/usage/accounts/nabu-org/login/start", "")
	if status != http.StatusOK {
		t.Fatalf("start status = %d, want 200\n%s", status, body)
	}
	var started struct {
		AuthorizeURL string `json:"authorizeUrl"`
	}
	if err := json.Unmarshal([]byte(body), &started); err != nil {
		t.Fatalf("decode start body: %v\n%s", err, body)
	}

	u, err := url.Parse(started.AuthorizeURL)
	if err != nil {
		t.Fatalf("parse authorizeUrl: %v", err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("state") == "" {
		t.Errorf("authorizeUrl is not a PKCE authorization: %s", started.AuthorizeURL)
	}
	// The verifier is the secret half and must never be served.
	if q.Get("code_verifier") != "" {
		t.Error("the authorize URL carries a code_verifier — PKCE would prove nothing")
	}

	// The state comes back with the code, as the callback page shows it.
	state := q.Get("state")
	status, body = postLogin(t, srv.URL+"/api/usage/accounts/nabu-org/login/complete",
		`{"code":"`+loginTestCode+`#`+state+`"}`)
	if status != http.StatusOK {
		t.Fatalf("complete status = %d, want 200\n%s", status, body)
	}
	if !strings.Contains(body, `"ok":true`) {
		t.Errorf("complete body = %s, want {\"ok\":true}", body)
	}

	// The credential landed where the daemon will read it back.
	raw, err := os.ReadFile(filepath.Join(store, "nabu-org.json"))
	if err != nil {
		t.Fatalf("read stored credential: %v", err)
	}
	if !strings.Contains(string(raw), loginTestAccess) {
		t.Error("the stored credential is not the one the exchange returned")
	}

	// Replaying the same paste must fail: the flow is single-use.
	status, _ = postLogin(t, srv.URL+"/api/usage/accounts/nabu-org/login/complete",
		`{"code":"`+loginTestCode+`#`+state+`"}`)
	if status != http.StatusBadRequest {
		t.Errorf("replayed completion status = %d, want 400 — an authorization must be single-use", status)
	}
}

// TestUsageLoginRejectsUnknownAccounts: only accounts the daemon actually reads
// can be connected, so these routes cannot be used to write a credential file
// for a name nobody polls.
func TestUsageLoginRejectsUnknownAccounts(t *testing.T) {
	useTempCredentialStore(t)
	_, srv := usageTestDB(t, "usage-login-unknown.db", time.Now().UTC().Format("2006-01-02T15:04:05"))
	attachAccountRoots(t, ingest.DefaultAccount, "nabu-org")
	resetPendingLogins()
	t.Cleanup(resetPendingLogins)

	for _, path := range []string{"start", "complete"} {
		t.Run(path, func(t *testing.T) {
			status, body := postLogin(t,
				srv.URL+"/api/usage/accounts/not-an-account/login/"+path, `{"code":"a#b"}`)
			if status != http.StatusNotFound {
				t.Errorf("status = %d, want 404\n%s", status, body)
			}
			if !strings.Contains(body, "unknown account") {
				t.Errorf("body = %s, want an 'unknown account' error", body)
			}
		})
	}
}

// TestUsageLoginHonoursTheKillSwitch: SWARMERY_USAGE_OAUTH=0 turns the whole
// OAuth surface off, the write half included.
func TestUsageLoginHonoursTheKillSwitch(t *testing.T) {
	store := useTempCredentialStore(t)
	_, srv := usageTestDB(t, "usage-login-off.db", time.Now().UTC().Format("2006-01-02T15:04:05"))
	dirs := attachAccountRoots(t, ingest.DefaultAccount, "nabu-org")
	up := loginUpstream(t, http.StatusOK)
	installLoginClient(t, usage.Source{Account: "nabu-org", ConfigDir: dirs["nabu-org"]}, up)
	t.Setenv("SWARMERY_USAGE_OAUTH", "0")

	status, body := postLogin(t, srv.URL+"/api/usage/accounts/nabu-org/login/start", "")
	if status != http.StatusConflict {
		t.Fatalf("start status = %d, want 409\n%s", status, body)
	}
	if !strings.Contains(body, "SWARMERY_USAGE_OAUTH=0") {
		t.Errorf("body = %s, want it to name the kill switch", body)
	}
	if _, err := os.Stat(filepath.Join(store, "nabu-org.json")); !os.IsNotExist(err) {
		t.Error("a credential was written while the OAuth surface was switched off")
	}
	if up.count() != 0 {
		t.Errorf("upstream called %d times while switched off, want 0", up.count())
	}
}

// TestUsageLoginCompleteFailures: every rejection path answers 4xx with a fixed,
// actionable message and writes nothing.
func TestUsageLoginCompleteFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		// start decides whether a flow is opened first.
		start bool
		// code is the pasted value; "$STATE" is replaced with the live state.
		code         string
		body         string
		upstream     int
		wantStatus   int
		wantContains string
	}{
		{
			name: "no login in progress", code: loginTestCode + "#somestate",
			upstream: http.StatusOK, wantStatus: http.StatusBadRequest,
			wantContains: "no login in progress",
		},
		{
			name: "state from a different attempt", start: true,
			code: loginTestCode + "#NOT-THE-ISSUED-STATE", upstream: http.StatusOK,
			wantStatus: http.StatusBadRequest, wantContains: "different login attempt",
		},
		{
			name: "code pasted without the fragment", start: true,
			code: loginTestCode, upstream: http.StatusOK,
			wantStatus: http.StatusBadRequest, wantContains: "including the part after the #",
		},
		{
			name: "upstream declines the exchange", start: true,
			code: loginTestCode + "#$STATE", upstream: http.StatusUnauthorized,
			wantStatus: http.StatusBadRequest, wantContains: "could not be completed",
		},
		{
			name: "malformed request body", start: true, body: `{"code":`,
			upstream: http.StatusOK, wantStatus: http.StatusBadRequest,
			wantContains: "invalid request body",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := useTempCredentialStore(t)
			_, srv := usageTestDB(t, "usage-login-fail.db", time.Now().UTC().Format("2006-01-02T15:04:05"))
			dirs := attachAccountRoots(t, ingest.DefaultAccount, "nabu-org")
			up := loginUpstream(t, tc.upstream)
			installLoginClient(t, usage.Source{Account: "nabu-org", ConfigDir: dirs["nabu-org"]}, up)

			state := ""
			if tc.start {
				status, body := postLogin(t, srv.URL+"/api/usage/accounts/nabu-org/login/start", "")
				if status != http.StatusOK {
					t.Fatalf("start status = %d\n%s", status, body)
				}
				var started struct {
					AuthorizeURL string `json:"authorizeUrl"`
				}
				if err := json.Unmarshal([]byte(body), &started); err != nil {
					t.Fatalf("decode start: %v", err)
				}
				u, err := url.Parse(started.AuthorizeURL)
				if err != nil {
					t.Fatalf("parse: %v", err)
				}
				state = u.Query().Get("state")
			}

			body := tc.body
			if body == "" {
				body = `{"code":"` + strings.ReplaceAll(tc.code, "$STATE", state) + `"}`
			}
			status, got := postLogin(t, srv.URL+"/api/usage/accounts/nabu-org/login/complete", body)
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d\n%s", status, tc.wantStatus, got)
			}
			if !strings.Contains(got, tc.wantContains) {
				t.Errorf("body = %s, want it to mention %q", got, tc.wantContains)
			}
			if _, err := os.Stat(filepath.Join(store, "nabu-org.json")); !os.IsNotExist(err) {
				t.Error("a rejected login still wrote a credential")
			}
		})
	}
}

// TestUsageLoginRejectsCrossOrigin: the login routes are state-changing, so they
// carry the same D4 origin hardening as every other write.
func TestUsageLoginRejectsCrossOrigin(t *testing.T) {
	useTempCredentialStore(t)
	_, srv := usageTestDB(t, "usage-login-origin.db", time.Now().UTC().Format("2006-01-02T15:04:05"))
	attachAccountRoots(t, ingest.DefaultAccount, "nabu-org")
	resetPendingLogins()
	t.Cleanup(resetPendingLogins)

	for _, path := range []string{"start", "complete"} {
		req, err := http.NewRequest(http.MethodPost,
			srv.URL+"/api/usage/accounts/nabu-org/login/"+path, strings.NewReader(`{}`))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Origin", "https://evil.example")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403 for a foreign origin", path, resp.StatusCode)
		}
	}
}

// TestUsageLoginResponsesCarryNoSecrets is the token-leak guard for the write
// path, the counterpart of TestUsageResponseCarriesNoSecrets on the read path.
// The bearer really is issued upstream (the store file proves it) and the
// authorization code really is sent — and neither may appear in a single byte
// the daemon serves back, in either step.
func TestUsageLoginResponsesCarryNoSecrets(t *testing.T) {
	store := useTempCredentialStore(t)
	_, srv := usageTestDB(t, "usage-login-secrets.db", time.Now().UTC().Format("2006-01-02T15:04:05"))
	dirs := attachAccountRoots(t, ingest.DefaultAccount, "nabu-org")
	up := loginUpstream(t, http.StatusOK)
	installLoginClient(t, usage.Source{Account: "nabu-org", ConfigDir: dirs["nabu-org"]}, up)

	startStatus, startBody := postLogin(t, srv.URL+"/api/usage/accounts/nabu-org/login/start", "")
	if startStatus != http.StatusOK {
		t.Fatalf("start status = %d\n%s", startStatus, startBody)
	}
	var started struct {
		AuthorizeURL string `json:"authorizeUrl"`
	}
	if err := json.Unmarshal([]byte(startBody), &started); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	u, err := url.Parse(started.AuthorizeURL)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	verifierProof := u.Query().Get("code_challenge")

	completeStatus, completeBody := postLogin(t, srv.URL+"/api/usage/accounts/nabu-org/login/complete",
		`{"code":"`+loginTestCode+`#`+u.Query().Get("state")+`"}`)
	if completeStatus != http.StatusOK {
		t.Fatalf("complete status = %d\n%s", completeStatus, completeBody)
	}

	// The tokens really were issued — otherwise the scan below is vacuous.
	stored, err := os.ReadFile(filepath.Join(store, "nabu-org.json"))
	if err != nil {
		t.Fatalf("read stored credential: %v", err)
	}
	if !strings.Contains(string(stored), loginTestAccess) ||
		!strings.Contains(string(stored), loginTestRefresh) {
		t.Fatal("the exchange did not actually store the fixture tokens")
	}
	if verifierProof == "" {
		t.Fatal("no PKCE challenge was issued; the flow under test did not happen")
	}

	for _, banned := range []string{
		loginTestAccess, loginTestRefresh, loginTestCode,
		"sk-ant", "accessToken", "access_token", "refreshToken", "refresh_token",
		"code_verifier", "Bearer", "bearer",
	} {
		if strings.Contains(startBody, banned) {
			t.Errorf("login/start body contains %q:\n%s", banned, startBody)
		}
		if strings.Contains(completeBody, banned) {
			t.Errorf("login/complete body contains %q:\n%s", banned, completeBody)
		}
	}
}

// TestPendingLoginExpiry: an abandoned flow stops being completable, and a
// second start replaces the first rather than accumulating CSRF state.
func TestPendingLoginExpiry(t *testing.T) {
	resetPendingLogins()
	t.Cleanup(resetPendingLogins)
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)

	flow := &usage.LoginFlow{Account: "nabu-org", State: "s1"}
	putPendingLogin("nabu-org", flow, now)

	if got := takePendingLogin("nabu-org", now.Add(loginFlowTTL)); got != nil {
		t.Error("a flow exactly at the TTL boundary is still completable")
	}

	putPendingLogin("nabu-org", flow, now)
	if got := takePendingLogin("nabu-org", now.Add(time.Minute)); got != flow {
		t.Error("a fresh flow was not returned")
	}
	if got := takePendingLogin("nabu-org", now.Add(time.Minute)); got != nil {
		t.Error("a taken flow is still available — an authorization must be single-use")
	}

	// A second start supersedes the first: only the newest state completes.
	second := &usage.LoginFlow{Account: "nabu-org", State: "s2"}
	putPendingLogin("nabu-org", flow, now)
	putPendingLogin("nabu-org", second, now)
	if got := takePendingLogin("nabu-org", now); got != second {
		t.Error("starting again did not replace the previous flow")
	}

	// The sweep drops other accounts' expired flows rather than holding their
	// CSRF state for the daemon's lifetime.
	putPendingLogin("stale-account", flow, now)
	putPendingLogin("nabu-org", second, now.Add(2*loginFlowTTL))
	if got := takePendingLogin("stale-account", now.Add(2*loginFlowTTL)); got != nil {
		t.Error("an expired flow for another account survived the sweep")
	}
}
