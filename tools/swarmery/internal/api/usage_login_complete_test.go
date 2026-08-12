package api

// Tests for the complete-login three-step transaction (usage_login.go):
// CompleteLogin → HandoffToConfigDir → Probe, reported as one completeResponse.
//
// The handoff runs FOR REAL wherever the filesystem can express the outcome:
// attachHomeAccounts (accounts_test.go, same package) points $HOME at a temp
// dir whose config dirs the account registry then reports, so
// usage.HandoffToConfigDir's registry fence passes and the write lands in the
// test's own tree. Only the 'failed' outcome uses the handoffCredential seam —
// it has no deterministic filesystem trigger of its own. The probe is always
// the stub installLoginClient installs (a real probe would exec the CLI).

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/claudeprobe"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/ingest"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/usage"
)

// completeBody is completeResponse decoded — the shape under test.
type completeBody struct {
	Connected bool   `json:"connected"`
	Handoff   string `json:"handoff"`
	Runnable  string `json:"runnable"`
	Reason    string `json:"reason"`
	NextStep  string `json:"nextStep"`
}

// completeLogin runs the two login steps for account and decodes the complete
// response — the connectAccount helper with the outcome kept, not discarded.
func completeLogin(t *testing.T, srv *httptest.Server, account string) completeBody {
	t.Helper()
	status, body := postLogin(t, srv.URL+"/api/usage/accounts/"+account+"/login/start", "")
	if status != 200 {
		t.Fatalf("start status = %d, want 200\n%s", status, body)
	}
	var started struct {
		AuthorizeURL string `json:"authorizeUrl"`
	}
	if err := json.Unmarshal([]byte(body), &started); err != nil {
		t.Fatalf("decode start body: %v\n%s", err, body)
	}
	state := ""
	if i := strings.Index(started.AuthorizeURL, "state="); i >= 0 {
		state = started.AuthorizeURL[i+len("state="):]
		if j := strings.IndexByte(state, '&'); j >= 0 {
			state = state[:j]
		}
	}
	status, body = postLogin(t, srv.URL+"/api/usage/accounts/"+account+"/login/complete",
		`{"code":"`+loginTestCode+`#`+state+`"}`)
	if status != 200 {
		t.Fatalf("complete status = %d, want 200\n%s", status, body)
	}
	var out completeBody
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode complete body: %v\n%s", err, body)
	}
	return out
}

// useHandoffError forces the handoff seam to fail with err.
func useHandoffError(t *testing.T, err error) {
	t.Helper()
	prev := handoffCredential
	handoffCredential = func(string, string) error { return err }
	t.Cleanup(func() { handoffCredential = prev })
}

// TestUsageLoginCompleteHandsOffAndProbes is SC-1 end to end at the API layer:
// one complete performs authorize → handoff → probe and reports all three. The
// REAL handoff runs — the credential file must land in the account's config
// dir — and the stored verdict must be 'ready' with source='probe'.
func TestUsageLoginCompleteHandsOffAndProbes(t *testing.T) {
	useTempCredentialStore(t)
	_, dirs := attachHomeAccounts(t, ingest.DefaultAccount, "nabu-org")
	db, srv := usageTestDB(t, "usage-complete.db", time.Now().UTC().Format("2006-01-02T15:04:05"))
	up := loginUpstream(t, 200)
	installLoginClient(t, usage.Source{Account: "nabu-org", ConfigDir: dirs["nabu-org"]}, up)
	var probed []string
	useProbeResult(t, claudeprobe.Result{Status: claudeprobe.StatusReady}, &probed)

	got := completeLogin(t, srv, "nabu-org")
	want := completeBody{Connected: true, Handoff: "written", Runnable: "ready"}
	if got != want {
		t.Errorf("complete = %+v, want %+v", got, want)
	}

	// The handoff really landed: the CLI-shape credential is in the config dir.
	raw, err := os.ReadFile(filepath.Join(dirs["nabu-org"], ".credentials.json"))
	if err != nil {
		t.Fatalf("read handed-over credential: %v", err)
	}
	if !strings.Contains(string(raw), `"claudeAiOauth"`) || !strings.Contains(string(raw), loginTestAccess) {
		t.Error("the handed-over file is not the CLI-shape credential the exchange produced")
	}

	// The probe ran against THIS account's dir and its verdict was persisted
	// through the Phase 1 store method, provenance included.
	if len(probed) != 1 || probed[0] != dirs["nabu-org"] {
		t.Errorf("probed dirs = %v, want exactly [%s]", probed, dirs["nabu-org"])
	}
	verdict, ok, err := store.GetAccountRunnable(db, "nabu-org")
	if err != nil || !ok {
		t.Fatalf("stored verdict missing (ok=%v err=%v)", ok, err)
	}
	if verdict.Status != "ready" || verdict.Source != "probe" {
		t.Errorf("stored verdict = %+v, want status=ready source=probe", verdict)
	}
}

// TestUsageLoginCompleteHandoffAlreadyPresent: a credential file already in the
// config dir is the CLI's — the handoff reports 'already-present', never
// clobbers it, and the probe still runs (verification decides the rest).
func TestUsageLoginCompleteHandoffAlreadyPresent(t *testing.T) {
	useTempCredentialStore(t)
	_, dirs := attachHomeAccounts(t, ingest.DefaultAccount, "nabu-org")
	_, srv := usageTestDB(t, "usage-complete-present.db", time.Now().UTC().Format("2006-01-02T15:04:05"))
	up := loginUpstream(t, 200)
	installLoginClient(t, usage.Source{Account: "nabu-org", ConfigDir: dirs["nabu-org"]}, up)
	var probed []string
	useProbeResult(t, claudeprobe.Result{Status: claudeprobe.StatusReady}, &probed)

	cliCred := filepath.Join(dirs["nabu-org"], ".credentials.json")
	const cliBody = `{"claudeAiOauth":{"accessToken":"NOT-A-REAL-TOKEN-cli-owned"}}`
	if err := os.WriteFile(cliCred, []byte(cliBody), 0o600); err != nil {
		t.Fatalf("write CLI credential: %v", err)
	}

	got := completeLogin(t, srv, "nabu-org")
	want := completeBody{Connected: true, Handoff: "already-present", Runnable: "ready"}
	if got != want {
		t.Errorf("complete = %+v, want %+v", got, want)
	}
	if raw, err := os.ReadFile(cliCred); err != nil || string(raw) != cliBody {
		t.Errorf("the CLI's own credential was modified (err=%v)", err)
	}
	if len(probed) != 1 {
		t.Errorf("probe ran %d times, want 1 — an existing credential still gets verified", len(probed))
	}
}

// TestUsageLoginCompleteDefaultAccountSkipsHandoff: ~/.claude is the CLI's own
// login — nothing is handed over, and the probe selects the default account by
// ABSENCE of a config dir (empty string, claudeprobe.Probe's contract).
func TestUsageLoginCompleteDefaultAccountSkipsHandoff(t *testing.T) {
	useTempCredentialStore(t)
	_, dirs := attachHomeAccounts(t, ingest.DefaultAccount)
	_, srv := usageTestDB(t, "usage-complete-default.db", time.Now().UTC().Format("2006-01-02T15:04:05"))
	up := loginUpstream(t, 200)
	installLoginClient(t, usage.Source{Account: ingest.DefaultAccount}, up)
	var probed []string
	useProbeResult(t, claudeprobe.Result{Status: claudeprobe.StatusReady}, &probed)

	got := completeLogin(t, srv, ingest.DefaultAccount)
	want := completeBody{Connected: true, Handoff: "skipped-default", Runnable: "ready"}
	if got != want {
		t.Errorf("complete = %+v, want %+v", got, want)
	}
	if len(probed) != 1 || probed[0] != "" {
		t.Errorf("probed dirs = %v, want exactly [\"\"] — the default is selected by absence", probed)
	}
	if _, err := os.Stat(filepath.Join(dirs[ingest.DefaultAccount], ".credentials.json")); !os.IsNotExist(err) {
		t.Errorf("a credential was written into the default config dir (stat err = %v)", err)
	}
}

// TestUsageLoginCompleteUnreadyProbeOffersPTYLogin is SC-2's API half: an
// unready probe answers 200 with nextStep:"pty-login" and a fixed-phrase
// reason — and the stored credential is NOT rolled back, because the quota
// connection genuinely succeeded.
func TestUsageLoginCompleteUnreadyProbeOffersPTYLogin(t *testing.T) {
	credStore := useTempCredentialStore(t)
	_, dirs := attachHomeAccounts(t, ingest.DefaultAccount, "nabu-org")
	db, srv := usageTestDB(t, "usage-complete-nologin.db", time.Now().UTC().Format("2006-01-02T15:04:05"))
	up := loginUpstream(t, 200)
	installLoginClient(t, usage.Source{Account: "nabu-org", ConfigDir: dirs["nabu-org"]}, up)
	useProbeResult(t, claudeprobe.Result{
		Status: claudeprobe.StatusNoLogin, Reason: claudeprobe.ReasonNoLogin,
	}, nil)

	got := completeLogin(t, srv, "nabu-org")
	want := completeBody{
		Connected: true, Handoff: "written", Runnable: "no-login",
		Reason: claudeprobe.ReasonNoLogin, NextStep: "pty-login",
	}
	if got != want {
		t.Errorf("complete = %+v, want %+v", got, want)
	}

	// Not rolled back: swarmery's stored credential survives an unready probe.
	if _, err := os.Stat(filepath.Join(credStore, "nabu-org.json")); err != nil {
		t.Errorf("the stored credential was rolled back: %v", err)
	}
	verdict, ok, _ := store.GetAccountRunnable(db, "nabu-org")
	if !ok || verdict.Status != "no-login" || verdict.Source != "probe" {
		t.Errorf("stored verdict = %+v (ok=%v), want status=no-login source=probe", verdict, ok)
	}
}

// TestUsageLoginCompleteHandoffFailureDoesNotFailTheRequest: a handoff that
// errors for any reason outside the fixed vocabulary reports 'failed' — the
// request stays 200, the credential stays stored, and the UI is pointed at the
// interactive login.
func TestUsageLoginCompleteHandoffFailureDoesNotFailTheRequest(t *testing.T) {
	credStore := useTempCredentialStore(t)
	_, dirs := attachHomeAccounts(t, ingest.DefaultAccount, "nabu-org")
	_, srv := usageTestDB(t, "usage-complete-handoff-fail.db", time.Now().UTC().Format("2006-01-02T15:04:05"))
	up := loginUpstream(t, 200)
	installLoginClient(t, usage.Source{Account: "nabu-org", ConfigDir: dirs["nabu-org"]}, up)
	useHandoffError(t, errors.New("disk full"))
	useProbeResult(t, claudeprobe.Result{
		Status: claudeprobe.StatusNoLogin, Reason: claudeprobe.ReasonNoLogin,
	}, nil)

	got := completeLogin(t, srv, "nabu-org")
	want := completeBody{
		Connected: true, Handoff: "failed", Runnable: "no-login",
		Reason: claudeprobe.ReasonNoLogin, NextStep: "pty-login",
	}
	if got != want {
		t.Errorf("complete = %+v, want %+v", got, want)
	}
	if _, err := os.Stat(filepath.Join(credStore, "nabu-org.json")); err != nil {
		t.Errorf("the stored credential was rolled back on a failed handoff: %v", err)
	}
}

// TestAccountProbePersistsPTYLoginSource: the probe endpoint records the
// verdict's provenance — ?source=pty-login marks a re-check that followed the
// interactive login, and anything outside the fixed set is refused.
func TestAccountProbePersistsPTYLoginSource(t *testing.T) {
	_, dirs := attachHomeAccounts(t, ingest.DefaultAccount, "nabu-org")
	db, srv := usageTestDB(t, "probe-pty-source.db", time.Now().UTC().Format("2006-01-02T15:04:05"))
	var probed []string
	useProbeResult(t, claudeprobe.Result{Status: claudeprobe.StatusReady}, &probed)

	status, body := postLogin(t, srv.URL+"/api/accounts/nabu-org/probe?source=pty-login", "")
	if status != 200 {
		t.Fatalf("probe status = %d, want 200\n%s", status, body)
	}
	if len(probed) != 1 || probed[0] != dirs["nabu-org"] {
		t.Errorf("probed dirs = %v, want [%s]", probed, dirs["nabu-org"])
	}
	verdict, ok, err := store.GetAccountRunnable(db, "nabu-org")
	if err != nil || !ok {
		t.Fatalf("stored verdict missing (ok=%v err=%v)", ok, err)
	}
	if verdict.Source != "pty-login" {
		t.Errorf("verdict source = %q, want pty-login", verdict.Source)
	}

	// The source column is provenance, not free text: anything else is a 400
	// and nothing is probed or stored over the pty-login row.
	status, body = postLogin(t, srv.URL+"/api/accounts/nabu-org/probe?source=made-up", "")
	if status != 400 {
		t.Errorf("invalid source status = %d, want 400\n%s", status, body)
	}
	if len(probed) != 1 {
		t.Errorf("an invalid source still ran the probe (%d runs)", len(probed))
	}
}
