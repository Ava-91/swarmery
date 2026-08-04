package api

// ── POST /api/projects/{id}/config/{key}/probe ───────────────────────────────
//
// The endpoint's whole contract is "never make this the operator's problem", so
// the tests are organised around that: the fence cases assert the refusal, and
// every runtime-failure case asserts 200 with a reason. A stub Runner stands in
// for the claude binary throughout — no process is ever spawned, and the timeout
// case does not actually wait three minutes for one.
//
// jira-pack is the fixture for the same reason it is in project_config_test.go:
// it is the only real declaration that ships. The domain words live here, in a
// test; nothing in project_config_probe.go names a pack, a tracker, or a field.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/pluginreq"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/provision"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
)

// probeRequirements is closedRequirements plus a probe block — the shape
// plugins/jira-pack/requirements.json ships.
const probeRequirements = `{
  "version": 1,
  "projectConfig": [
    {
      "key": "jira",
      "title": "Jira tracker",
      "why": "/jira-fix runs with autonomy: auto.",
      "schema": {
        "type": "object",
        "additionalProperties": false,
        "properties": {
          "baseUrl":    { "type": "string" },
          "projectKey": { "type": "string" },
          "qaStatus":   { "type": "string" },
          "repro": {
            "type": "object",
            "additionalProperties": false,
            "properties": {
              "setup": { "type": "string" },
              "test":  { "type": "string" }
            },
            "required": ["test"]
          }
        },
        "required": ["baseUrl", "projectKey", "qaStatus", "repro"]
      },
      "probe": {
        "needs": ["baseUrl", "projectKey"],
        "fields": ["qaStatus", "repro.test"],
        "timeoutSeconds": 5,
        "prompt": "Resolve the tools by name and report only JSON."
      }
    }
  ]
}`

// probeStubRunner is the seam. stdout/err are what the fake claude "returns";
// deadline makes it wait on the context and then report what ClaudeRunner
// reports when the process is killed by one.
type probeStubRunner struct {
	stdout   string
	err      error
	deadline bool

	calls int
	dir   string
	stdin string
	args  []string
}

func (r *probeStubRunner) Claude(ctx context.Context, dir, stdin string, args ...string) (string, error) {
	r.calls++
	r.dir, r.stdin, r.args = dir, stdin, args
	if r.deadline {
		<-ctx.Done()
		return "", errors.New("claude -p timed out; stderr: ")
	}
	return r.stdout, r.err
}

// probeTestServer wires a handler whose Provision service holds the stub Runner,
// seeds the jira-pack catalog with `requirements`, and returns the server URL,
// the project dir, and the stub.
func probeTestServer(t *testing.T, requirements string) (srvURL, projectDir string, runner *probeStubRunner) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "probe-api.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	root := t.TempDir()
	projectDir = filepath.Join(root, "managed")
	writeProjectSettings(t, projectDir, `{
		"enabledPlugins": {"core@swarmery": true, "jira-pack@swarmery": true}
	}`)
	writeProjectConfig(t, projectDir, overlayTenKeys)
	execSQL(t, db, `INSERT INTO projects (id, path, slug, name, first_seen, last_activity, archived)
		VALUES (1, ?, 'managed', 'Managed', '2026-07-10T00:00:00Z', '2026-07-14T00:00:00Z', 0)`, projectDir)

	prev := onboardCfg
	AttachOnboard(OnboardConfig{Roots: []string{root}})
	t.Cleanup(func() { onboardCfg = prev })

	claudeDir := seedPluginCatalog(t, configPackManifest)
	writePackRequirements(t, claudeDir, "jira-pack", requirements)

	runner = &probeStubRunner{}
	h := &Handler{DB: db, Provision: provision.NewService(db, runner)}
	mux := http.NewServeMux()
	Routes(mux, h)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL, projectDir, runner
}

func probeURL(srvURL, projectID, key string) string {
	return srvURL + "/api/projects/" + projectID + "/config/" + key + "/probe"
}

// probeInputs are the declared `needs`, filled — the state in which a probe runs.
func probeInputs() map[string]any {
	return map[string]any{"baseUrl": "acme.example.net", "projectKey": "ABC"}
}

// probeSuggestions pulls the suggestions object out of a 200 body.
func probeSuggestions(t *testing.T, out map[string]any) map[string][]string {
	t.Helper()
	raw, ok := out["suggestions"]
	if !ok {
		t.Fatalf("body has no suggestions field: %v", out)
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("suggestions is not an object (null would break the client): %v", raw)
	}
	got := map[string][]string{}
	for k, v := range obj {
		list, ok := v.([]any)
		if !ok {
			t.Fatalf("suggestions[%q] is not an array: %v", k, v)
		}
		for _, item := range list {
			s, ok := item.(string)
			if !ok {
				t.Fatalf("suggestions[%q] holds a non-string: %v", k, item)
			}
			got[k] = append(got[k], s)
		}
	}
	return got
}

// probeReason asserts a 200-with-reason body and returns the reason.
func probeReason(t *testing.T, out map[string]any) string {
	t.Helper()
	if got := probeSuggestions(t, out); len(got) != 0 {
		t.Errorf("suggestions = %v, want empty on a failed probe", got)
	}
	reason, _ := out["reason"].(string)
	if strings.TrimSpace(reason) == "" {
		t.Fatalf("body has no reason on a failed probe: %v", out)
	}
	return reason
}

// ── the fence, copied from putProjectConfig ──────────────────────────────────

func TestProbeProjectConfigNoFence(t *testing.T) {
	srvURL, _, runner := probeTestServer(t, probeRequirements)
	AttachOnboard(OnboardConfig{})
	t.Cleanup(func() { AttachOnboard(OnboardConfig{}) })

	out := doJSON(t, http.MethodPost, probeURL(srvURL, "1", "jira"),
		map[string]any{"value": probeInputs()}, http.StatusForbidden)
	if msg, _ := out["error"].(string); !strings.Contains(msg, "SWARMERY_ONBOARD_ROOTS") {
		t.Errorf("error = %q, want the SWARMERY_ONBOARD_ROOTS fence message", msg)
	}
	if runner.calls != 0 {
		t.Errorf("runner was invoked %d time(s) behind a closed fence", runner.calls)
	}
}

// A page on another origin must not be able to spawn a process on the
// operator's machine through a daemon listening on loopback.
func TestProbeProjectConfigCrossOrigin(t *testing.T) {
	srvURL, _, runner := probeTestServer(t, probeRequirements)

	req, err := http.NewRequest(http.MethodPost, probeURL(srvURL, "1", "jira"),
		strings.NewReader(`{"value":{"baseUrl":"acme.example.net","projectKey":"ABC"}}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin POST = %d, want 403", resp.StatusCode)
	}
	if runner.calls != 0 {
		t.Errorf("runner was invoked %d time(s) from a cross-origin request", runner.calls)
	}
}

func TestProbeProjectConfigFenceRefusals(t *testing.T) {
	cases := map[string]struct {
		projectID, key string
		want           int
	}{
		"bad id":         {"abc", "jira", http.StatusBadRequest},
		"unknown key":    {"1", "commitScopes", http.StatusNotFound},
		"unknown projet": {"999", "jira", http.StatusNotFound},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srvURL, _, runner := probeTestServer(t, probeRequirements)
			doJSON(t, http.MethodPost, probeURL(srvURL, tc.projectID, tc.key),
				map[string]any{"value": probeInputs()}, tc.want)
			if runner.calls != 0 {
				t.Errorf("runner was invoked %d time(s) on a refused request", runner.calls)
			}
		})
	}
}

func TestProbeProjectConfigOutsideRootsIsRefused(t *testing.T) {
	srvURL, _, runner := probeTestServer(t, probeRequirements)
	// Re-point the fence at a directory the project is not under.
	AttachOnboard(OnboardConfig{Roots: []string{t.TempDir()}})

	doJSON(t, http.MethodPost, probeURL(srvURL, "1", "jira"),
		map[string]any{"value": probeInputs()}, http.StatusForbidden)
	if runner.calls != 0 {
		t.Errorf("runner was invoked %d time(s) for a project outside the roots", runner.calls)
	}
}

func TestProbeProjectConfigRejectsBadBodies(t *testing.T) {
	for name, body := range map[string]any{
		"no value field":    map[string]any{},
		"value is a string": map[string]any{"value": "acme.example.net"},
		"value is null":     map[string]any{"value": nil},
		"value is a list":   map[string]any{"value": []any{1, 2}},
	} {
		t.Run(name, func(t *testing.T) {
			srvURL, _, runner := probeTestServer(t, probeRequirements)
			doJSON(t, http.MethodPost, probeURL(srvURL, "1", "jira"), body, http.StatusBadRequest)
			if runner.calls != 0 {
				t.Errorf("runner was invoked %d time(s) on a malformed body", runner.calls)
			}
		})
	}
}

// A key whose pack declared no probe has no endpoint here — answering "no
// suggestions" would dress a missing route up as a failed lookup.
func TestProbeProjectConfigKeyWithoutProbeIs404(t *testing.T) {
	srvURL, _, runner := probeTestServer(t, closedRequirements)

	out := doJSON(t, http.MethodPost, probeURL(srvURL, "1", "jira"),
		map[string]any{"value": probeInputs()}, http.StatusNotFound)
	if msg, _ := out["error"].(string); !strings.Contains(msg, "no probe declared") {
		t.Errorf("error = %q, want the no-probe-declared message", msg)
	}
	if runner.calls != 0 {
		t.Errorf("runner was invoked %d time(s) for a key with no probe", runner.calls)
	}
}

// ── needs: the one failure the operator can fix in the form ──────────────────

func TestProbeProjectConfigUnfilledNeedsIs400(t *testing.T) {
	for name, value := range map[string]map[string]any{
		"both absent":  {},
		"one absent":   {"baseUrl": "acme.example.net"},
		"one blank":    {"baseUrl": "acme.example.net", "projectKey": "   "},
		"one null":     {"baseUrl": "acme.example.net", "projectKey": nil},
		"only unneeds": {"qaStatus": "In QA"},
	} {
		t.Run(name, func(t *testing.T) {
			srvURL, _, runner := probeTestServer(t, probeRequirements)
			out := doJSON(t, http.MethodPost, probeURL(srvURL, "1", "jira"),
				map[string]any{"value": value}, http.StatusBadRequest)
			if len(problemStrings(t, out)) == 0 {
				t.Error("400 body carried no problems; the modal has nothing to place on a field")
			}
			if runner.calls != 0 {
				t.Errorf("runner was invoked %d time(s) with unfilled needs — that is minutes of agent time spent to learn nothing", runner.calls)
			}
		})
	}
}

// ── the happy path ───────────────────────────────────────────────────────────

func TestProbeProjectConfigCleanJSON(t *testing.T) {
	srvURL, projectDir, runner := probeTestServer(t, probeRequirements)
	runner.stdout = `{"suggestions":{"qaStatus":["In QA","Ready for QA"],"repro.test":["make test"]},"notes":"two statuses"}`

	out := doJSON(t, http.MethodPost, probeURL(srvURL, "1", "jira"),
		map[string]any{"value": probeInputs()}, http.StatusOK)

	got := probeSuggestions(t, out)
	if len(got["qaStatus"]) != 2 || got["qaStatus"][0] != "In QA" {
		t.Errorf("qaStatus = %v, want the two board statuses verbatim", got["qaStatus"])
	}
	if len(got["repro.test"]) != 1 || got["repro.test"][0] != "make test" {
		t.Errorf("repro.test = %v, want [make test]", got["repro.test"])
	}
	if reason, _ := out["reason"].(string); reason != "" {
		t.Errorf("reason = %q, want empty on a successful probe", reason)
	}
	if notes, _ := out["notes"].(string); notes != "two statuses" {
		t.Errorf("notes = %q, want the agent's note passed through", notes)
	}

	// The seam was used as the design says: cwd is the project, the prompt goes
	// in on stdin (never argv, where it would come back inside error text), and
	// the daemon added nothing but the operator's own partial value.
	if runner.calls != 1 {
		t.Fatalf("runner calls = %d, want exactly 1", runner.calls)
	}
	// Compared through EvalSymlinks because the fence hands the handler the
	// RESOLVED path (resolveUnderRoots), and on macOS a temp dir is reached via
	// /var → /private/var. The assertion that matters is "cwd is the project",
	// not which spelling of it.
	wantDir, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if runner.dir != wantDir {
		t.Errorf("cwd = %q, want the project path %q", runner.dir, wantDir)
	}
	if !strings.Contains(runner.stdin, "Resolve the tools by name") {
		t.Errorf("stdin does not carry the pack's prompt: %q", runner.stdin)
	}
	if !strings.Contains(runner.stdin, "acme.example.net") {
		t.Errorf("stdin does not carry the operator's partial value: %q", runner.stdin)
	}
	for _, arg := range runner.args {
		if strings.Contains(arg, "Resolve the tools by name") {
			t.Errorf("the prompt was passed in argv (%q) — it would echo back inside every error", arg)
		}
	}
}

// The agent is told to emit JSON and nothing else, and mostly complies. Throwing
// away real work over a line of narration would make the feature feel unreliable.
func TestProbeProjectConfigJSONWithProseAround(t *testing.T) {
	srvURL, _, runner := probeTestServer(t, probeRequirements)
	runner.stdout = "I looked up the board. Here is the result:\n\n```json\n" +
		`{"suggestions":{"qaStatus":["In QA"]}}` +
		"\n```\n\nLet me know if you need anything else."

	out := doJSON(t, http.MethodPost, probeURL(srvURL, "1", "jira"),
		map[string]any{"value": probeInputs()}, http.StatusOK)
	if got := probeSuggestions(t, out); len(got["qaStatus"]) != 1 || got["qaStatus"][0] != "In QA" {
		t.Errorf("qaStatus = %v, want [In QA] extracted from around the prose", got["qaStatus"])
	}
}

// A brace inside a quoted value must not close the object early — the bug that
// turns a lenient scanner into a truncating one.
func TestProbeProjectConfigBraceInsideStringDoesNotTruncate(t *testing.T) {
	srvURL, _, runner := probeTestServer(t, probeRequirements)
	runner.stdout = `prefix {"suggestions":{"repro.test":["npm test -- --grep \"a{1,2}\""],"qaStatus":["In QA"]}} suffix`

	out := doJSON(t, http.MethodPost, probeURL(srvURL, "1", "jira"),
		map[string]any{"value": probeInputs()}, http.StatusOK)
	got := probeSuggestions(t, out)
	if len(got["qaStatus"]) != 1 {
		t.Errorf("qaStatus = %v — the scan stopped at a brace inside a string literal", got["qaStatus"])
	}
	if len(got["repro.test"]) != 1 || !strings.Contains(got["repro.test"][0], "a{1,2}") {
		t.Errorf("repro.test = %v, want the command with its literal braces intact", got["repro.test"])
	}
}

// ── trimming: the pack's whitelist is authoritative ──────────────────────────

func TestProbeProjectConfigDiscardsFieldsOutsideTheWhitelist(t *testing.T) {
	srvURL, _, runner := probeTestServer(t, probeRequirements)
	// baseUrl and projectKey are the probe's INPUTS, and `secret` was never
	// declared at all. A wandering session must not put any of them in front of
	// the operator.
	runner.stdout = `{"suggestions":{
		"qaStatus":["In QA"],
		"baseUrl":["evil.example.com"],
		"projectKey":["XXX"],
		"secret":["hunter2"]
	}}`

	out := doJSON(t, http.MethodPost, probeURL(srvURL, "1", "jira"),
		map[string]any{"value": probeInputs()}, http.StatusOK)
	got := probeSuggestions(t, out)
	if len(got) != 1 {
		t.Errorf("suggestions = %v, want only the declared field to survive", got)
	}
	for _, undeclared := range []string{"baseUrl", "projectKey", "secret"} {
		if _, ok := got[undeclared]; ok {
			t.Errorf("suggestions carried %q, which the pack never nominated", undeclared)
		}
	}
}

func TestProbeProjectConfigTrimsTo50ValuesPerField(t *testing.T) {
	srvURL, _, runner := probeTestServer(t, probeRequirements)
	many := make([]string, 150)
	for i := range many {
		many[i] = fmt.Sprintf("status-%d", i)
	}
	payload, err := json.Marshal(map[string]any{"suggestions": map[string]any{"qaStatus": many}})
	if err != nil {
		t.Fatal(err)
	}
	runner.stdout = string(payload)

	out := doJSON(t, http.MethodPost, probeURL(srvURL, "1", "jira"),
		map[string]any{"value": probeInputs()}, http.StatusOK)
	got := probeSuggestions(t, out)
	if len(got["qaStatus"]) != 50 {
		t.Errorf("qaStatus length = %d, want it capped at 50", len(got["qaStatus"]))
	}
	if got["qaStatus"][0] != "status-0" {
		t.Errorf("qaStatus[0] = %q, want the cap to keep the FIRST values", got["qaStatus"][0])
	}
}

// ── every runtime failure is a 200 with a reason ─────────────────────────────

// The headline guarantee. Each row is a way the probe can fail in production;
// none of them may reach the operator as an error.
func TestProbeProjectConfigFailuresAre200WithReason(t *testing.T) {
	cases := map[string]struct {
		stdout   string
		err      error
		wantHint string
	}{
		"garbage output":      {stdout: "I could not find the board, sorry.", wantHint: "did not return JSON"},
		"empty output":        {stdout: "", wantHint: "did not return JSON"},
		"unbalanced JSON":     {stdout: `{"suggestions":{"qaStatus":["In QA"]`, wantHint: "did not return JSON"},
		"wrong shape":         {stdout: `{"suggestions":{"qaStatus":"In QA"}}`, wantHint: "unexpected shape"},
		"suggestions is list": {stdout: `{"suggestions":["In QA"]}`, wantHint: "unexpected shape"},
		"nothing found":       {stdout: `{"suggestions":{}}`, wantHint: "found no values"},
		"only undeclared":     {stdout: `{"suggestions":{"nope":["x"]}}`, wantHint: "found no values"},
		"only blank values":   {stdout: `{"suggestions":{"qaStatus":["","   "]}}`, wantHint: "found no values"},
		"claude not on PATH":  {err: errors.New(`claude -p: exec: "claude": executable file not found in $PATH; stderr: `), wantHint: "executable file not found"},
		"process failed":      {err: errors.New("claude -p: exit status 1; stderr: MCP server not authorised"), wantHint: "exit status 1"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srvURL, _, runner := probeTestServer(t, probeRequirements)
			runner.stdout, runner.err = tc.stdout, tc.err

			out := doJSON(t, http.MethodPost, probeURL(srvURL, "1", "jira"),
				map[string]any{"value": probeInputs()}, http.StatusOK)
			if reason := probeReason(t, out); !strings.Contains(reason, tc.wantHint) {
				t.Errorf("reason = %q, want it to mention %q", reason, tc.wantHint)
			}
		})
	}
}

// The timeout branch, exercised through runProbe with an already-expired parent
// context. Deterministic and instant: a test that actually waited out a real
// deadline would trade three minutes (or a contrived 1-second declaration) for
// no extra confidence, and would flake on a loaded CI box.
func TestProbeRunTimeoutIsAReasonNotAnError(t *testing.T) {
	runner := &probeStubRunner{deadline: true}
	h := &Handler{Provision: provision.NewService(nil, runner)}
	spec := pluginreq.ProbeSpec{Fields: []string{"qaStatus"}, Prompt: "p", TimeoutSeconds: 300}

	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	out := h.runProbe(expired, t.TempDir(), spec, json.RawMessage(`{}`))
	if out.suggestions == nil {
		t.Error("suggestions is nil on a timed-out probe; the contract says it is always an object")
	}
	if len(out.suggestions) != 0 {
		t.Errorf("suggestions = %v, want empty", out.suggestions)
	}
	if !strings.Contains(out.reason, "timed out") {
		t.Errorf("reason = %q, want it to name the timeout", out.reason)
	}
	if !strings.Contains(out.reason, "5m0s") {
		t.Errorf("reason = %q, want it to name how long it waited", out.reason)
	}
}

// A daemon without a provision service must still answer the contract rather
// than panic on a nil dereference.
func TestProbeProjectConfigWithoutRunnerIs200WithReason(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "no-runner.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	root := t.TempDir()
	projectDir := filepath.Join(root, "managed")
	writeProjectSettings(t, projectDir, `{"enabledPlugins": {"jira-pack@swarmery": true}}`)
	writeProjectConfig(t, projectDir, overlayTenKeys)
	execSQL(t, db, `INSERT INTO projects (id, path, slug, name, first_seen, last_activity, archived)
		VALUES (1, ?, 'managed', 'Managed', '2026-07-10T00:00:00Z', '2026-07-14T00:00:00Z', 0)`, projectDir)

	prev := onboardCfg
	AttachOnboard(OnboardConfig{Roots: []string{root}})
	t.Cleanup(func() { onboardCfg = prev })

	claudeDir := seedPluginCatalog(t, configPackManifest)
	writePackRequirements(t, claudeDir, "jira-pack", probeRequirements)

	// No Provision at all — the state a daemon is in before it is attached, and
	// the state a future refactor could leave it in.
	mux := http.NewServeMux()
	Routes(mux, &Handler{DB: db})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	out := doJSON(t, http.MethodPost, probeURL(srv.URL, "1", "jira"),
		map[string]any{"value": probeInputs()}, http.StatusOK)
	if reason := probeReason(t, out); !strings.Contains(reason, "runner is not attached") {
		t.Errorf("reason = %q, want the missing-runner explanation", reason)
	}
}

// A reason is a grey line in a modal, not a log file: a kilobyte of stack trace
// would push the form off the screen.
func TestProbeProjectConfigReasonIsShortAndSingleLine(t *testing.T) {
	srvURL, _, runner := probeTestServer(t, probeRequirements)
	runner.err = errors.New("claude -p: exit status 1; stderr: " + strings.Repeat("panic goroutine 1\n", 200))

	out := doJSON(t, http.MethodPost, probeURL(srvURL, "1", "jira"),
		map[string]any{"value": probeInputs()}, http.StatusOK)
	reason := probeReason(t, out)
	if len(reason) > reasonMaxBytes+8 {
		t.Errorf("reason is %d bytes, want it capped near %d", len(reason), reasonMaxBytes)
	}
	if strings.ContainsAny(reason, "\n\r\t") {
		t.Errorf("reason carries control characters: %q", reason)
	}
}

// ── the plugins row exposes the probe, minus the prompt ──────────────────────

func TestProjectPluginsRowCarriesProbeWithoutThePrompt(t *testing.T) {
	srvURL, _, _ := probeTestServer(t, probeRequirements)

	row := pluginRowByName(t, srvURL, "1", "jira-pack")
	if row.ConfigProbe == nil {
		t.Fatal("row carries no configProbe, so the modal can never offer the button")
	}
	if len(row.ConfigProbe.Needs) != 2 || row.ConfigProbe.Needs[0] != "baseUrl" {
		t.Errorf("needs = %v, want the declared inputs", row.ConfigProbe.Needs)
	}
	if len(row.ConfigProbe.Fields) != 2 {
		t.Errorf("fields = %v, want the two declared fields", row.ConfigProbe.Fields)
	}
	// The prompt is the daemon's business; shipping it would ride on every poll.
	blob, err := json.Marshal(row.ConfigProbe)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "Resolve the tools by name") {
		t.Errorf("the prompt reached the browser: %s", blob)
	}
}

// A pack whose probe block is broken keeps its config form — the operator loses
// the suggestions, not the ability to configure the pack.
func TestProjectPluginsBrokenProbeKeepsTheRequirement(t *testing.T) {
	for name, probe := range map[string]string{
		"probe is a string":   `"yes please"`,
		"probe has no fields": `{"needs":["baseUrl"],"prompt":"do it"}`,
		"probe has no prompt": `{"needs":["baseUrl"],"fields":["qaStatus"]}`,
		"fields is a string":  `{"fields":"qaStatus","prompt":"do it"}`,
	} {
		t.Run(name, func(t *testing.T) {
			decl := strings.Replace(probeRequirements,
				`"probe": {
        "needs": ["baseUrl", "projectKey"],
        "fields": ["qaStatus", "repro.test"],
        "timeoutSeconds": 5,
        "prompt": "Resolve the tools by name and report only JSON."
      }`, `"probe": `+probe, 1)
			if decl == probeRequirements {
				t.Fatal("fixture substitution missed — the probe block moved")
			}
			srvURL, _, _ := probeTestServer(t, decl)

			row := pluginRowByName(t, srvURL, "1", "jira-pack")
			if row.ConfigKey != "jira" {
				t.Errorf("configKey = %q, want the requirement to survive a broken probe", row.ConfigKey)
			}
			if row.ConfigProbe != nil {
				t.Errorf("configProbe = %v, want it dropped when the block is unusable", row.ConfigProbe)
			}
			doJSON(t, http.MethodPost, probeURL(srvURL, "1", "jira"),
				map[string]any{"value": probeInputs()}, http.StatusNotFound)
		})
	}
}
