package usage

// Rung 2's authorization flow: swarmery's own OAuth session, so an account with
// no readable credential file (typical for a non-default account on macOS) can
// still show a live quota card.
//
// # Where these parameters come from — VERIFIED, not invented
//
// The authorize step is not publicly documented, so every constant below was
// extracted from the installed `claude` CLI's own bundle rather than guessed.
// Evidence (Claude Code 2.1.220, the standalone binary at
// ~/.local/share/claude/versions/2.1.220; the OAuth module is minified JS inside
// it, readable with `strings`):
//
//	ZIl = {                                      // the "prod" endpoint table
//	  BASE_API_URL:            "https://api.anthropic.com",
//	  CLAUDE_AI_AUTHORIZE_URL: "https://claude.com/cai/oauth/authorize",
//	  TOKEN_URL:               "https://platform.claude.com/v1/oauth/token",
//	  MANUAL_REDIRECT_URL:     "https://platform.claude.com/oauth/code/callback",
//	  CLIENT_ID:               "9d1c250a-e61b-44d9-88ed-5944d1962f5e", … }
//
//	// buildAuthUrl — the authorize URL, verbatim parameter order:
//	d.searchParams.append("code","true"); …("client_id",CLIENT_ID);
//	…("response_type","code"); …("redirect_uri", isManual ? MANUAL_REDIRECT_URL
//	  : `http://localhost:${port}/callback`); …("scope", scopes.join(" "));
//	…("code_challenge", e); …("code_challenge_method","S256"); …("state", t)
//
//	// exchangeCodeForTokens — JSON body (NOT form-encoded), state included:
//	{grant_type:"authorization_code", code, redirect_uri, client_id,
//	 code_verifier, state}  →  POST TOKEN_URL, Content-Type: application/json
//
//	// PKCE primitives (base64url, unpadded):
//	b64url(buf) = buf.toString("base64").replaceAll("+","-")
//	                 .replaceAll("/","_").replaceAll("=","")
//	verifier  = b64url(randomBytes(32))
//	challenge = b64url(sha256(verifier))
//	state     = b64url(randomBytes(32))
//
//	// the manual paste is "code#state":
//	let [code, state] = pasted.split("#");
//	if (!code || !state) → "Invalid code. Please make sure the full code was copied"
//
// The client id and TOKEN_URL are the ones this package already embedded and
// exercises on the refresh grant (claude.go), which independently confirms the
// table above is the live one.
//
// # Why the MANUAL redirect, not a loopback port
//
// The CLI supports both. We use the manual one exclusively: it needs no listener
// and no free port, works from a daemon behind any firewall, and works when the
// browser doing the login is not on this machine at all. The cost is one paste,
// which is the whole interaction anyway.
//
// # Secret hygiene (R3)
//
// The verifier, the authorization code and every token stay inside this package
// and the store. No function here returns token material, and every error is a
// fixed sentinel — upstream response bodies are never interpolated into an
// error, so nothing an authorization server says can be reflected to the browser.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Login flow outcomes. All fixed strings: see the hygiene note above.
var (
	// ErrLoginCodeFormat means the pasted value was not "code#state".
	ErrLoginCodeFormat = errors.New("usage: authorization code must be the full \"code#state\" value")
	// ErrLoginStateMismatch means the state returned with the code is not the
	// one this flow issued — the CSRF check, and the reason a code pasted from
	// somebody else's authorization is refused.
	ErrLoginStateMismatch = errors.New("usage: authorization state did not match")
	// ErrLoginExchange means the token endpoint declined the exchange or
	// answered something unusable. Deliberately opaque.
	ErrLoginExchange = errors.New("usage: authorization code could not be exchanged")
)

const (
	// authorizePath is CLAUDE_AI_AUTHORIZE_URL's path — the claude.ai
	// SUBSCRIPTION authorization page (as opposed to CONSOLE_AUTHORIZE_URL,
	// which authorizes an API-billing Console account). Quota windows belong to
	// a subscription, so this is the only correct one of the two.
	authorizePath = "/cai/oauth/authorize"
	// manualRedirectPath is MANUAL_REDIRECT_URL's path on the auth host. Derived
	// from authBase rather than hard-coded whole, exactly as the CLI derives it,
	// so the redirect_uri sent to the authorize step and the one sent to the
	// token step can never drift apart.
	manualRedirectPath = "/oauth/code/callback"
	// defaultLoginBase hosts the authorize page. A THIRD host: authorize is on
	// claude.com, the token endpoint on platform.claude.com, the usage endpoint
	// on api.anthropic.com.
	defaultLoginBase = "https://claude.com"
	// profilePath returns the subscription metadata the plan chip renders.
	profilePath = "/api/oauth/profile"
	// pkceBytes is the entropy behind both the verifier and the state, matching
	// the CLI's randomBytes(32).
	pkceBytes = 32
)

// loginScopes is the CLI's CLAUDE_AI_OAUTH_SCOPES — the scope set a claude.ai
// subscription login grants, and the set its own refresh grant defaults to when
// a stored credential carries none. Used verbatim because a proven-valid scope
// set is worth more here than a minimal one: a set the authorize page rejects is
// a Connect button that simply never works.
//
// Only "user:profile" is actually exercised by this package (the quota endpoint
// requires it). Notably ABSENT is the CLI's "org:create_api_key", which belongs
// to the Console flow — swarmery never mints API keys and must not hold the
// power to.
var loginScopes = []string{
	requiredScope, // user:profile
	"user:inference",
	"user:sessions:claude_code",
	"user:mcp_servers",
	"user:file_upload",
}

// LoginFlow is one in-progress authorization. The caller holds it between the
// start and complete steps and must treat it as secret: Verifier is the PKCE
// proof, and State is the CSRF token.
//
// Only AuthorizeURL is ever meant to leave the daemon.
type LoginFlow struct {
	// Account this flow will connect on success.
	Account string
	// AuthorizeURL is what the operator opens in a browser. Safe to serve: it
	// carries the PKCE CHALLENGE, never the verifier.
	AuthorizeURL string
	// State is the CSRF nonce echoed back with the code.
	State string
	// Verifier is the PKCE secret. Never serialize it.
	Verifier string
}

// loginBase is the authorize host, honouring the Client's override so a test can
// assert the built URL without reaching the network.
func (c *Client) loginBase() string {
	if c.LoginBase != "" {
		return strings.TrimRight(c.LoginBase, "/")
	}
	return defaultLoginBase
}

// redirectURI is the manual-paste callback the authorize step redirects to and
// the token step must repeat verbatim. Derived from authBase so both steps agree
// by construction — a redirect_uri mismatch is the classic silent OAuth failure.
func (c *Client) redirectURI() string { return c.authBase() + manualRedirectPath }

// StartLogin mints a fresh PKCE verifier + CSRF state and builds the authorize
// URL for this client's account. Nothing is persisted and no request is made:
// the operator's browser performs the authorization, and CompleteLogin finishes
// it.
//
// SWARMERY_USAGE_OAUTH=0 refuses here too — the kill switch disables the whole
// OAuth surface, not just the read side.
func (c *Client) StartLogin() (*LoginFlow, error) {
	if oauthOptedOut() {
		return nil, ErrDisabled
	}
	verifier, err := randomB64()
	if err != nil {
		return nil, err
	}
	state, err := randomB64()
	if err != nil {
		return nil, err
	}

	q := url.Values{}
	q.Set("code", "true")
	q.Set("client_id", oauthClientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", c.redirectURI())
	q.Set("scope", strings.Join(loginScopes, " "))
	q.Set("code_challenge", codeChallenge(verifier))
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)

	return &LoginFlow{
		Account:      c.Src.Account,
		AuthorizeURL: c.loginBase() + authorizePath + "?" + q.Encode(),
		State:        state,
		Verifier:     verifier,
	}, nil
}

// CompleteLogin finishes an authorization: it parses the pasted "code#state"
// value, verifies the state against the flow (CSRF), exchanges the code for
// tokens, best-effort enriches them with the subscription's plan metadata, and
// persists the result to swarmery's own store.
//
// It returns nothing on success — deliberately: the caller has no use for token
// material and must not be able to leak what it never received.
func (c *Client) CompleteLogin(ctx context.Context, flow *LoginFlow, pasted string) error {
	if oauthOptedOut() {
		return ErrDisabled
	}
	if flow == nil {
		return ErrLoginStateMismatch
	}
	code, state, ok := splitPastedCode(pasted)
	if !ok {
		return ErrLoginCodeFormat
	}
	// Constant-time so the comparison cannot be turned into an oracle for a
	// state the attacker is guessing byte by byte.
	if subtle.ConstantTimeCompare([]byte(state), []byte(flow.State)) != 1 {
		return ErrLoginStateMismatch
	}

	creds, err := c.exchangeCode(ctx, code, state, flow.Verifier)
	if err != nil {
		return err
	}
	c.enrichPlan(ctx, creds)

	account := flow.Account
	if account == "" {
		account = c.Src.Account
	}
	if err := writeStoredCreds(account, creds); err != nil {
		return err
	}
	// The connection supersedes whatever bearer this client was replaying; the
	// next poll must start from the credential just written.
	c.cacheToken("")
	return nil
}

// splitPastedCode parses the value the callback page shows, which is
// "code#state" (the CLI splits it exactly this way). Both halves are required:
// a value pasted without the fragment cannot be state-checked, and accepting it
// would quietly turn the CSRF guard off.
func splitPastedCode(pasted string) (code, state string, ok bool) {
	code, state, found := strings.Cut(strings.TrimSpace(pasted), "#")
	code, state = strings.TrimSpace(code), strings.TrimSpace(state)
	if !found || code == "" || state == "" {
		return "", "", false
	}
	return code, state, true
}

// tokenResponse is the token endpoint's success body. Only the fields we
// actually store are decoded.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

// exchangeCode performs the authorization_code grant. The body is JSON and
// carries `state` alongside the usual fields — both mirror what the CLI sends;
// a form-encoded body or an omitted state is refused upstream.
func (c *Client) exchangeCode(ctx context.Context, code, state, verifier string) (*Creds, error) {
	body, err := json.Marshal(map[string]string{
		"grant_type":    "authorization_code",
		"code":          code,
		"redirect_uri":  c.redirectURI(),
		"client_id":     oauthClientID,
		"code_verifier": verifier,
		"state":         state,
	})
	if err != nil {
		return nil, ErrLoginExchange
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.authBase()+tokenPath, bytes.NewReader(body))
	if err != nil {
		return nil, ErrLoginExchange
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("user-agent", userAgent)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, ErrLoginExchange
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	// The upstream body is deliberately NOT inspected or reported on failure: it
	// is attacker-influenced text that would otherwise reach a browser.
	if err != nil || resp.StatusCode != http.StatusOK {
		return nil, ErrLoginExchange
	}

	var out tokenResponse
	if err := json.Unmarshal(raw, &out); err != nil || out.AccessToken == "" {
		return nil, ErrLoginExchange
	}

	creds := &Creds{
		AccessToken:  out.AccessToken,
		RefreshToken: out.RefreshToken,
		Scopes:       parseScopeList(out.Scope),
		FromStore:    true,
	}
	if out.ExpiresIn > 0 {
		creds.ExpiresAt = c.now().Add(time.Duration(out.ExpiresIn) * time.Second).UnixMilli()
	}
	if len(creds.Scopes) == 0 {
		// The endpoint has shipped responses without a scope echo. Recording
		// what we ASKED for keeps the refresh grant (which replays the stored
		// scopes) sending the same set, rather than silently narrowing it.
		creds.Scopes = append([]string(nil), loginScopes...)
	}
	return creds, nil
}

// profileResponse is the subset of GET /api/oauth/profile the plan chip needs.
type profileResponse struct {
	Organization struct {
		RateLimitTier string `json:"rate_limit_tier"`
	} `json:"organization"`
}

// enrichPlan fills in the subscription tier so a connected card shows the same
// "Max"/"Pro" chip a CLI-written credential does — the CLI populates its own
// credential from this endpoint the same way (fetchProfileInfo → rate_limit_tier).
//
// BEST EFFORT by design: this is cosmetic metadata, and a profile fetch that
// fails must never cost the operator the connection they just authorized.
func (c *Client) enrichPlan(ctx context.Context, creds *Creds) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase()+profilePath, nil)
	if err != nil {
		return
	}
	req.Header.Set("authorization", "Bearer "+creds.AccessToken)
	req.Header.Set("anthropic-beta", anthropicBeta)
	req.Header.Set("user-agent", userAgent)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil || resp.StatusCode != http.StatusOK {
		return
	}
	var out profileResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return
	}
	creds.RateLimitTier = strings.TrimSpace(out.Organization.RateLimitTier)
}

// parseScopeList splits the space-separated `scope` echo.
func parseScopeList(s string) []string {
	return strings.Fields(s)
}

// oauthOptedOut is the one reading of the kill switch, shared by the read path
// and the login path so they can never disagree about what "off" means.
func oauthOptedOut() bool { return os.Getenv(oauthOptOutEnv) == "0" }

// randomB64 mints 32 bytes of CSPRNG entropy as unpadded base64url — the CLI's
// verifier/state primitive.
func randomB64() (string, error) {
	b := make([]byte, pkceBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// codeChallenge is the S256 transformation: unpadded base64url of the verifier's
// SHA-256 digest (RFC 7636 §4.2).
func codeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
