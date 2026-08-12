package usage

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// handoffFixture builds the world one handoff needs, all inside temp dirs:
// $HOME redirected (claudeacct discovery and ConfigDirFor both resolve through
// it), the swarmery store redirected through its own seam, and one stored
// credential for the account. It returns the account's canonical config dir —
// NOT created on disk; the caller decides whether it pre-exists.
//
// No test in this file may touch the real ~/.swarmery or ~/.claude.
func handoffFixture(t *testing.T, account string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	useTempStore(t)
	if err := writeStoredCreds(account, storeFixtureCreds()); err != nil {
		t.Fatalf("seed the store: %v", err)
	}
	return filepath.Join(home, ".claude-"+account)
}

// captureHandoffLog routes the standard logger into a buffer for one test so
// assertions can prove what a failure does — and does not — log.
func captureHandoffLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &buf
}

// TestHandoffWritesTheCLIShape pins the whole happy path: the file lands at
// mode 0600 in a dir created 0700, carries EXACTLY the {"claudeAiOauth":{...}}
// shape (one top-level key, no swarmery-local `disabled` flag), and parseCreds
// round-trips it — the proof the CLI-side reader and this writer agree.
func TestHandoffWritesTheCLIShape(t *testing.T) {
	dir := handoffFixture(t, "nabu-org")
	want := storedCreds("nabu-org")
	if want == nil {
		t.Fatal("fixture did not seed the store")
	}

	if err := HandoffToConfigDir("nabu-org", dir); err != nil {
		t.Fatalf("HandoffToConfigDir: %v", err)
	}

	di, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat config dir: %v", err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("config dir mode = %o, want 700", perm)
	}
	dst := filepath.Join(dir, credentialsFile)
	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat handed-over file: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %o, want 600 — a credential must not be group/world readable", perm)
	}

	raw, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read handed-over file: %v", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("handed-over file is not JSON: %v", err)
	}
	if len(top) != 1 {
		t.Errorf("top-level keys = %d, want exactly 1 (claudeAiOauth)", len(top))
	}
	if _, ok := top["claudeAiOauth"]; !ok {
		t.Error("handed-over file has no claudeAiOauth key — the CLI would not read it")
	}
	if strings.Contains(string(raw), "disabled") {
		t.Error("swarmery's per-account disabled flag leaked into the CLI's file")
	}

	got := parseCreds(raw)
	if got == nil {
		t.Fatal("parseCreds cannot read the handed-over file")
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken {
		t.Error("handed-over credential did not round-trip (tokens elided)")
	}
	if got.ExpiresAt != want.ExpiresAt || got.SubscriptionType != want.SubscriptionType ||
		got.RateLimitTier != want.RateLimitTier || len(got.Scopes) != len(want.Scopes) {
		t.Error("handed-over credential lost a field")
	}

	// The store dir must hold only the store file, the config dir only the
	// handed-over file — a leftover temp sibling means the publish leaked.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read config dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != credentialsFile {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("config dir holds %v, want only %s", names, credentialsFile)
	}
}

// TestHandoffNeverOverwrites: an existing .credentials.json is the CLI's. The
// call must answer ErrHandoffExists and leave the bytes untouched.
func TestHandoffNeverOverwrites(t *testing.T) {
	dir := handoffFixture(t, "nabu-org")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dst := filepath.Join(dir, credentialsFile)
	theCLIs := []byte(`{"claudeAiOauth":{"accessToken":"the-CLIs-own-login"}}`)
	if err := os.WriteFile(dst, theCLIs, 0o600); err != nil {
		t.Fatalf("write the CLI's file: %v", err)
	}

	if err := HandoffToConfigDir("nabu-org", dir); !errors.Is(err, ErrHandoffExists) {
		t.Fatalf("HandoffToConfigDir error = %v, want ErrHandoffExists", err)
	}
	after, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read the CLI's file back: %v", err)
	}
	if !bytes.Equal(after, theCLIs) {
		t.Error("the CLI's credential file was modified — a working login was clobbered")
	}
}

// TestHandoffRefusesTheDefaultAccount: ~/.claude is the operator's primary
// login and swarmery must never write into it — not even when explicitly asked.
func TestHandoffRefusesTheDefaultAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	useTempStore(t)
	if err := writeStoredCreds("default", storeFixtureCreds()); err != nil {
		t.Fatalf("seed the store: %v", err)
	}
	dir := filepath.Join(home, ".claude")

	if err := HandoffToConfigDir("default", dir); !errors.Is(err, ErrHandoffDefaultAccount) {
		t.Fatalf("HandoffToConfigDir error = %v, want ErrHandoffDefaultAccount", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, credentialsFile)); !errors.Is(err, os.ErrNotExist) {
		t.Error("a refused default-account handoff still created a file under ~/.claude")
	}
}

// TestHandoffRefusesAForeignConfigDir: the registry (claudeacct) is the source
// of truth for where an account lives; any other target — a request-body path
// most of all — is refused, whether the account's real dir exists on disk
// (Discover) or not yet (ConfigDirFor).
func TestHandoffRefusesAForeignConfigDir(t *testing.T) {
	dir := handoffFixture(t, "nabu-org")
	// Make the account discoverable at its real dir, the way a provisioned
	// account is: the glob needs <dir>/projects.
	if err := os.MkdirAll(filepath.Join(dir, "projects"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	foreign := t.TempDir()

	if err := HandoffToConfigDir("nabu-org", foreign); !errors.Is(err, errHandoffDirMismatch) {
		t.Fatalf("HandoffToConfigDir error = %v, want errHandoffDirMismatch", err)
	}
	if _, err := os.Lstat(filepath.Join(foreign, credentialsFile)); !errors.Is(err, os.ErrNotExist) {
		t.Error("a refused handoff still wrote into the foreign dir")
	}
	if _, err := os.Lstat(filepath.Join(dir, credentialsFile)); !errors.Is(err, os.ErrNotExist) {
		t.Error("a refused handoff wrote into the registry dir instead")
	}

	// The registry dir itself is accepted — the refusal above was about the
	// path, not the account.
	if err := HandoffToConfigDir("nabu-org", dir); err != nil {
		t.Fatalf("HandoffToConfigDir into the registry's own dir: %v", err)
	}
}

// TestHandoffWithoutAStoredCredential: nothing in the store — including a
// credential parked by the disabled flag — hands nothing over, cleanly, and
// creates nothing on disk.
func TestHandoffWithoutAStoredCredential(t *testing.T) {
	for _, tc := range []struct {
		name string
		seed func(t *testing.T)
	}{
		{"no store file", func(t *testing.T) {}},
		{"disabled credential", func(t *testing.T) {
			base := storeBaseDir()
			if err := os.MkdirAll(base, storeDirMode); err != nil {
				t.Fatalf("mkdir store: %v", err)
			}
			raw := `{"disabled":true,"claudeAiOauth":{"accessToken":"` + fakeAccess + `"}}`
			if err := os.WriteFile(filepath.Join(base, "nabu-org.json"), []byte(raw), storeFileMode); err != nil {
				t.Fatalf("write disabled credential: %v", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			useTempStore(t)
			tc.seed(t)
			dir := filepath.Join(home, ".claude-nabu-org")

			if err := HandoffToConfigDir("nabu-org", dir); !errors.Is(err, ErrNoCreds) {
				t.Fatalf("HandoffToConfigDir error = %v, want ErrNoCreds", err)
			}
			if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
				t.Error("a handoff with nothing to hand over still created the config dir")
			}
		})
	}
}

// TestHandoffFailureLeavesNoPartialFile drives a crash mid-write through the
// store's own temp-file seam, the same way store_test proves the store's
// atomicity: every write in this package goes through storeCreateTemp, so the
// seam covers the handoff too.
func TestHandoffFailureLeavesNoPartialFile(t *testing.T) {
	dir := handoffFixture(t, "nabu-org")

	prev := storeCreateTemp
	storeCreateTemp = func(d, pattern string) (*os.File, error) {
		f, err := os.CreateTemp(d, pattern)
		if err != nil {
			return nil, err
		}
		// Closing it early makes every subsequent write/sync fail — the
		// closest deterministic stand-in for a crash mid-write.
		f.Close()
		return f, nil
	}
	t.Cleanup(func() { storeCreateTemp = prev })

	if err := HandoffToConfigDir("nabu-org", dir); err == nil {
		t.Fatal("HandoffToConfigDir = nil, want the write to fail on a closed file")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read config dir: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("config dir holds %v after a failed handoff, want nothing — no partial file, no temp leftover", names)
	}
}

// TestHandoffIsWriteOnce pins SC-7 from both directions: a token refresh in
// swarmery's own store leaves the handed-over file byte-identical (there IS no
// code path from the store's writer to a config dir — writeStoredCreds is
// exactly what the refresh path calls), and a second handoff refuses rather
// than rewrites.
func TestHandoffIsWriteOnce(t *testing.T) {
	dir := handoffFixture(t, "nabu-org")
	if err := HandoffToConfigDir("nabu-org", dir); err != nil {
		t.Fatalf("HandoffToConfigDir: %v", err)
	}
	dst := filepath.Join(dir, credentialsFile)
	before, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read handed-over file: %v", err)
	}

	// A refresh rotates the stored credential — claude.go's refresh path
	// persists through writeStoredCreds, which this calls directly.
	rotated := storeFixtureCreds()
	rotated.AccessToken = "rotated-access-must-stay-out-of-the-config-dir"
	rotated.RefreshToken = "rotated-refresh-must-stay-out-of-the-config-dir"
	rotated.ExpiresAt = time.Now().Add(16 * time.Hour).UnixMilli()
	if err := writeStoredCreds("nabu-org", rotated); err != nil {
		t.Fatalf("writeStoredCreds (the refresh path's writer): %v", err)
	}

	after, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read handed-over file after the refresh: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("a store refresh rewrote the handed-over file — swarmery became a second writer of a rotating token (SC-7)")
	}

	// The "at most once per connect" half: a retried connect refuses.
	if err := HandoffToConfigDir("nabu-org", dir); !errors.Is(err, ErrHandoffExists) {
		t.Fatalf("second HandoffToConfigDir error = %v, want ErrHandoffExists", err)
	}
	if final, _ := os.ReadFile(dst); !bytes.Equal(before, final) {
		t.Error("a second handoff modified the file")
	}
}

// TestHandoffLogsNoCredentialMaterial pins SC-10 over every outcome: a failure
// logs the account key and a fixed phrase, a success logs nothing here, and no
// log line or returned error ever carries token material.
func TestHandoffLogsNoCredentialMaterial(t *testing.T) {
	dir := handoffFixture(t, "nabu-org")
	buf := captureHandoffLog(t)

	var errs []error
	// Failure: foreign dir.
	errs = append(errs, HandoffToConfigDir("nabu-org", t.TempDir()))
	// Failure: default account.
	errs = append(errs, HandoffToConfigDir("default", dir))
	// Success.
	if err := HandoffToConfigDir("nabu-org", dir); err != nil {
		t.Fatalf("HandoffToConfigDir: %v", err)
	}
	// Failure: already handed over.
	errs = append(errs, HandoffToConfigDir("nabu-org", dir))

	logged := buf.String()
	if got := strings.Count(logged, "usage: credential handoff failed: account=nabu-org"); got != 2 {
		t.Errorf("nabu-org failure lines = %d, want 2 (the fixed phrase, with the account key)", got)
	}
	if !strings.Contains(logged, "account=default") {
		t.Error("the default-account refusal did not log its fixed phrase")
	}
	for _, secret := range []string{fakeAccess, fakeRefresh} {
		if strings.Contains(logged, secret) {
			t.Error("a log line carries credential material (token elided)")
		}
		for _, err := range errs {
			if err != nil && strings.Contains(err.Error(), secret) {
				t.Error("an error message carries credential material (token elided)")
			}
		}
	}
	// The success path logged nothing: three failures, three lines.
	if got := len(strings.Split(strings.TrimSpace(logged), "\n")); got != 3 {
		t.Errorf("log lines = %d, want exactly 3 — one per failure, none for success", got)
	}
}
