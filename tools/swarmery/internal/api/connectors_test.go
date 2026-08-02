package api

// Connectors endpoint tests. A fake mcpcfg runner stands in for the real
// `claude mcp …` CLI (records argv, returns canned stdout), so these tests are
// hermetic — no CLI on PATH, no DB, and critically no real MCP-config mutation.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/mcpcfg"
)

const connListFixture = `Checking MCP server health…

alpha-stdio: npx -y alpha - ✔ Connected
beta-http: https://beta.example.com/mcp - ! Needs authentication
gamma-sse: https://gamma.example.com/sse (SSE) - ✘ Failed to connect
`

// recordingRunner captures every argv the reader executes and replies with a
// canned list fixture. Thread-safe: the httptest server may serve concurrently.
type recordingRunner struct {
	mu    sync.Mutex
	calls [][]string
	err   error
}

func (rr *recordingRunner) run(_ context.Context, args ...string) ([]byte, error) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	rr.calls = append(rr.calls, append([]string(nil), args...))
	if rr.err != nil {
		return nil, rr.err
	}
	return []byte(connListFixture), nil
}

func (rr *recordingRunner) last() []string {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	if len(rr.calls) == 0 {
		return nil
	}
	return rr.calls[len(rr.calls)-1]
}

// connectorsTestServer wires a bare Handler + the connectors routes against a
// reader driven by rr. No DB is needed — the connectors handler touches none.
func connectorsTestServer(t *testing.T, rr *recordingRunner) *httptest.Server {
	t.Helper()
	AttachConnectorReader(mcpcfg.NewWithRunner(rr.run))
	t.Cleanup(func() { AttachConnectorReader(nil) })
	mux := http.NewServeMux()
	Routes(mux, &Handler{})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestGetConnectors(t *testing.T) {
	rr := &recordingRunner{}
	srv := connectorsTestServer(t, rr)

	var resp struct {
		Connectors []map[string]any `json:"connectors"`
	}
	getJSON(t, srv.URL+"/api/connectors", &resp)

	if len(resp.Connectors) != 3 {
		t.Fatalf("connectors = %d, want 3", len(resp.Connectors))
	}
	if resp.Connectors == nil {
		t.Error("connectors must serialize as [], never null")
	}
	// Spot-check the first row's normalized fields.
	first := resp.Connectors[0]
	if first["name"] != "alpha-stdio" || first["status"] != "connected" || first["transport"] != "stdio" {
		t.Errorf("row 0 = %+v, want alpha-stdio/connected/stdio", first)
	}
	if want := []string{"mcp", "list"}; !equalArgs(rr.last(), want) {
		t.Errorf("GET drove argv %v, want %v", rr.last(), want)
	}
}

func TestGetConnectors503WhenUnattached(t *testing.T) {
	// Explicitly detach so the endpoint reports unavailable.
	AttachConnectorReader(nil)
	mux := http.NewServeMux()
	Routes(mux, &Handler{})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/api/connectors")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("unattached GET status = %d, want 503", resp.StatusCode)
	}
}

func TestAddConnectorHTTP(t *testing.T) {
	rr := &recordingRunner{}
	srv := connectorsTestServer(t, rr)

	body := `{"name":"newsrv","transport":"http","url":"https://new.example.com/mcp","scope":"user"}`
	resp, err := http.Post(srv.URL+"/api/connectors", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add status = %d, want 200", resp.StatusCode)
	}
	// The refreshed list is returned.
	var out struct {
		Connectors []map[string]any `json:"connectors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Connectors) != 3 {
		t.Errorf("add returned %d connectors, want the refreshed 3", len(out.Connectors))
	}
	// The add call must have run, and the URL must appear as a discrete argv token.
	want := []string{"mcp", "add", "--transport", "http", "-s", "user", "newsrv", "https://new.example.com/mcp"}
	// The LAST call is the refresh list; the add is the second-to-last.
	rr.mu.Lock()
	if len(rr.calls) < 2 {
		rr.mu.Unlock()
		t.Fatalf("want >=2 calls (add + refresh), got %d", len(rr.calls))
	}
	addCall := rr.calls[len(rr.calls)-2]
	rr.mu.Unlock()
	if !equalArgs(addCall, want) {
		t.Errorf("add argv = %v, want %v", addCall, want)
	}
}

func TestAddConnectorRejectsBadTransport(t *testing.T) {
	rr := &recordingRunner{}
	srv := connectorsTestServer(t, rr)

	body := `{"name":"x","transport":"carrier-pigeon","command":"echo","scope":"local"}`
	resp, err := http.Post(srv.URL+"/api/connectors", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad transport status = %d, want 400", resp.StatusCode)
	}
	// No `claude mcp add` should have run.
	rr.mu.Lock()
	calls := len(rr.calls)
	rr.mu.Unlock()
	if calls != 0 {
		t.Errorf("bad input must not exec the CLI, got %d calls", calls)
	}
}

// TestAddConnectorInjectionSafe drives a shell-metacharacter name through the
// full HTTP path and asserts it lands as ONE literal argv token — the
// end-to-end injection gate.
func TestAddConnectorInjectionSafe(t *testing.T) {
	rr := &recordingRunner{}
	srv := connectorsTestServer(t, rr)

	payload := "evil; touch /tmp/pwned #"
	req := addConnectorRequest{Name: payload, Transport: "stdio", Command: "echo", Scope: "local"}
	b, _ := json.Marshal(req)
	resp, err := http.Post(srv.URL+"/api/connectors", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add status = %d, want 200", resp.StatusCode)
	}
	rr.mu.Lock()
	addCall := rr.calls[len(rr.calls)-2]
	rr.mu.Unlock()
	found := 0
	for _, a := range addCall {
		if a == payload {
			found++
		}
		if strings.Contains(a, "touch") && a != payload {
			t.Errorf("payload appears to have been split: token %q", a)
		}
	}
	if found != 1 {
		t.Errorf("payload must be exactly one literal argv token, found %d in %v", found, addCall)
	}
}

func TestAddConnectorRejectsForeignOrigin(t *testing.T) {
	rr := &recordingRunner{}
	srv := connectorsTestServer(t, rr)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/connectors",
		strings.NewReader(`{"name":"x","transport":"http","url":"https://e.com","scope":"user"}`))
	req.Header.Set("Origin", "https://evil.example.com")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("foreign-origin add status = %d, want 403", resp.StatusCode)
	}
	rr.mu.Lock()
	calls := len(rr.calls)
	rr.mu.Unlock()
	if calls != 0 {
		t.Errorf("foreign origin must be rejected before exec, got %d calls", calls)
	}
}

func TestRemoveConnector(t *testing.T) {
	rr := &recordingRunner{}
	srv := connectorsTestServer(t, rr)

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/connectors/beta-http?scope=user", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("remove status = %d, want 200", resp.StatusCode)
	}
	want := []string{"mcp", "remove", "beta-http", "-s", "user"}
	rr.mu.Lock()
	removeCall := rr.calls[len(rr.calls)-2]
	rr.mu.Unlock()
	if !equalArgs(removeCall, want) {
		t.Errorf("remove argv = %v, want %v", removeCall, want)
	}
}

func TestRemoveConnectorNameWithSpace(t *testing.T) {
	rr := &recordingRunner{}
	srv := connectorsTestServer(t, rr)

	// A name with a space + dot must round-trip through the path segment.
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/connectors/claude.ai%20Linear", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("remove status = %d, want 200", resp.StatusCode)
	}
	rr.mu.Lock()
	removeCall := rr.calls[len(rr.calls)-2]
	rr.mu.Unlock()
	if len(removeCall) < 3 || removeCall[2] != "claude.ai Linear" {
		t.Errorf("name did not round-trip: argv = %v", removeCall)
	}
}

func TestConnectorsListError(t *testing.T) {
	rr := &recordingRunner{err: errors.New("claude exploded")}
	srv := connectorsTestServer(t, rr)
	resp, err := http.Get(srv.URL + "/api/connectors")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("list-error status = %d, want 500", resp.StatusCode)
	}
}

// TestConnectorsClaudeNotFound503 pins the contract the web layer degrades on:
// a claude binary the daemon cannot locate is 503 + {error, hint} on ALL three
// verbs — the same "unavailable" class as an unattached reader — and the hint
// names the escape hatch. A missing CLI is a host condition, not a 500.
func TestConnectorsClaudeNotFound503(t *testing.T) {
	notFound := fmt.Errorf("%w: boom", mcpcfg.ErrClaudeNotFound)

	newReq := func(t *testing.T, srv *httptest.Server) []*http.Request {
		t.Helper()
		get, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/connectors", nil)
		post, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/connectors",
			strings.NewReader(`{"name":"x","transport":"http","url":"https://e.example.com/mcp","scope":"user"}`))
		post.Header.Set("Content-Type", "application/json")
		del, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/connectors/x?scope=user", nil)
		return []*http.Request{get, post, del}
	}

	rr := &recordingRunner{err: notFound}
	srv := connectorsTestServer(t, rr)

	for _, req := range newReq(t, srv) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", req.Method, err)
		}
		var body map[string]string
		decErr := json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("%s status = %d, want 503", req.Method, resp.StatusCode)
		}
		if decErr != nil {
			t.Fatalf("%s body decode: %v", req.Method, decErr)
		}
		if body["error"] == "" {
			t.Errorf("%s: empty error field in %v", req.Method, body)
		}
		if body["hint"] == "" {
			t.Errorf("%s: empty hint field in %v", req.Method, body)
		}
		if !strings.Contains(body["hint"], "SWARMERY_CLAUDE_BIN") {
			t.Errorf("%s hint = %q, want it to name SWARMERY_CLAUDE_BIN", req.Method, body["hint"])
		}
		// The 503 body is static: it must not carry filesystem detail.
		for _, leak := range []string{"/Users/", "/home/"} {
			for k, v := range body {
				if strings.Contains(v, leak) {
					t.Errorf("%s: field %q leaks %q: %q", req.Method, k, leak, v)
				}
			}
		}
	}
}

func equalArgs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
