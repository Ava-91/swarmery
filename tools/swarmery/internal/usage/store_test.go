package usage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// useTempStore points swarmery's own credential store at a throwaway directory
// for one test. Every store test goes through this: nothing may touch the
// operator's real ~/.swarmery.
func useTempStore(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "credentials")
	t.Setenv(storeDirEnv, dir)
	return dir
}

func storeFixtureCreds() *Creds {
	return &Creds{
		AccessToken:      fakeAccess,
		RefreshToken:     fakeRefresh,
		ExpiresAt:        time.Now().Add(8 * time.Hour).UnixMilli(),
		Scopes:           []string{requiredScope, "user:inference"},
		SubscriptionType: "max",
		RateLimitTier:    "default_claude_max_20x",
	}
}

// TestStoreRoundTrip is the basic contract: what goes in comes out, with the
// FromStore provenance set (which is what licenses the refresh write-back), and
// with the hygiene modes the policy note promises — 0700 directory, 0600 file.
func TestStoreRoundTrip(t *testing.T) {
	dir := useTempStore(t)
	want := storeFixtureCreds()

	if err := writeStoredCreds("nabu-org", want); err != nil {
		t.Fatalf("writeStoredCreds: %v", err)
	}

	got := storedCreds("nabu-org")
	if got == nil {
		t.Fatal("storedCreds = nil, want the credential just written")
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken {
		t.Error("stored credential did not round-trip (tokens elided)")
	}
	if got.ExpiresAt != want.ExpiresAt || got.SubscriptionType != want.SubscriptionType ||
		got.RateLimitTier != want.RateLimitTier || len(got.Scopes) != len(want.Scopes) {
		t.Errorf("stored credential lost a field: %+v", *got)
	}
	if !got.FromStore {
		t.Error("FromStore = false — without it the refresh path will not persist rotation")
	}

	di, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat store dir: %v", err)
	}
	if perm := di.Mode().Perm(); perm != storeDirMode {
		t.Errorf("store dir mode = %o, want %o", perm, storeDirMode)
	}
	fi, err := os.Stat(filepath.Join(dir, "nabu-org.json"))
	if err != nil {
		t.Fatalf("stat credential file: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != storeFileMode {
		t.Errorf("credential file mode = %o, want %o — a credential must not be group/world readable", perm, storeFileMode)
	}
}

// TestStoreWriteIsAtomic pins R2 from both sides.
//
//   - After a successful write the directory holds ONLY the credential file: a
//     temp+rename that forgot to rename (or to clean up) would leave a sibling.
//   - A write that fails midway leaves the PREVIOUS credential byte-for-byte
//     intact. An implementation that truncated the destination in place — the
//     obvious non-atomic version — fails this half, and in production it strands
//     an account with a half-written file or a refresh token already rotated
//     away upstream.
func TestStoreWriteIsAtomic(t *testing.T) {
	dir := useTempStore(t)
	first := storeFixtureCreds()
	if err := writeStoredCreds("nabu-org", first); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "nabu-org.json"))
	if err != nil {
		t.Fatalf("read seeded credential: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read store dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "nabu-org.json" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("store dir holds %v, want only the credential file — a temp file was left behind", names)
	}

	// Force the write to fail at the temp-file step, i.e. after the destination
	// already exists and before anything could have been published.
	boom := errors.New("temp file refused")
	prev := storeCreateTemp
	storeCreateTemp = func(string, string) (*os.File, error) { return nil, boom }
	t.Cleanup(func() { storeCreateTemp = prev })

	second := storeFixtureCreds()
	second.AccessToken = "second-write-that-must-not-land"
	if err := writeStoredCreds("nabu-org", second); !errors.Is(err, boom) {
		t.Fatalf("writeStoredCreds error = %v, want the injected failure", err)
	}

	after, err := os.ReadFile(filepath.Join(dir, "nabu-org.json"))
	if err != nil {
		t.Fatalf("read credential after the failed write: %v", err)
	}
	if string(after) != string(before) {
		t.Error("a failed write modified the stored credential — the write is not atomic")
	}
	if strings.Contains(string(after), second.AccessToken) {
		t.Error("a failed write leaked the new access token into the stored credential")
	}
	if got := storedCreds("nabu-org"); got == nil || got.AccessToken != first.AccessToken {
		t.Error("the previous credential is no longer readable after a failed write")
	}
}

// TestStoreCleansUpAfterAPartialWrite: a failure AFTER the temp file exists must
// still leave the directory with nothing but the destination.
func TestStoreCleansUpAfterAPartialWrite(t *testing.T) {
	dir := useTempStore(t)
	if err := os.MkdirAll(dir, storeDirMode); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	prev := storeCreateTemp
	storeCreateTemp = func(d, pattern string) (*os.File, error) {
		f, err := os.CreateTemp(d, pattern)
		if err != nil {
			return nil, err
		}
		// Closing it early makes every subsequent write/sync fail, which is the
		// closest deterministic stand-in for a crash mid-write.
		f.Close()
		return f, nil
	}
	t.Cleanup(func() { storeCreateTemp = prev })

	if err := writeStoredCreds("nabu-org", storeFixtureCreds()); err == nil {
		t.Fatal("writeStoredCreds = nil, want the write to fail on a closed file")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read store dir: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("store dir holds %v after a failed write, want nothing", names)
	}
}

// TestStoredCredsTreatsUnusableFilesAsAbsent: every unusable state must read as
// "this account is not connected through swarmery" so resolution falls through
// to the config-dir file, rather than hard-failing the account into an error
// card the operator cannot act on.
func TestStoredCredsTreatsUnusableFilesAsAbsent(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"corrupt json", "not json at all{{{"},
		{"truncated json", `{"claudeAiOauth":{"accessToken":"abc`},
		{"empty object", `{}`},
		{"no access token", `{"claudeAiOauth":{"refreshToken":"r"}}`},
		{"empty file", ``},
		{"per-account disabled flag", `{"disabled":true,"claudeAiOauth":{"accessToken":"` + fakeAccess + `"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := useTempStore(t)
			if err := os.MkdirAll(dir, storeDirMode); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, "nabu-org.json"), []byte(tc.raw), storeFileMode); err != nil {
				t.Fatalf("write: %v", err)
			}
			if got := storedCreds("nabu-org"); got != nil {
				t.Errorf("storedCreds = %v, want nil for %s", got, tc.name)
			}
		})
	}
}

func TestStoredCredsMissingFile(t *testing.T) {
	useTempStore(t)
	if got := storedCreds("nabu-org"); got != nil {
		t.Errorf("storedCreds = %v, want nil when nothing was ever written", got)
	}
}

// TestStorePathRejectsUnsafeAccountKeys: the account key is derived from a
// directory name the operator controls, so it is validated rather than trusted —
// a key with a separator or a ".." would otherwise escape the store directory.
func TestStorePathRejectsUnsafeAccountKeys(t *testing.T) {
	useTempStore(t)
	for _, bad := range []string{
		"", ".", "..", "../escape", "nested/name", `back\slash`, ".hidden", "a/../../etc",
	} {
		t.Run("key="+bad, func(t *testing.T) {
			if got := storePath(bad); got != "" {
				t.Errorf("storePath(%q) = %q, want \"\"", bad, got)
			}
			if got := storedCreds(bad); got != nil {
				t.Errorf("storedCreds(%q) = %v, want nil", bad, got)
			}
			if err := writeStoredCreds(bad, storeFixtureCreds()); !errors.Is(err, errNoStore) {
				t.Errorf("writeStoredCreds(%q) error = %v, want errNoStore", bad, err)
			}
		})
	}
	if got := storePath("nabu-org"); got == "" {
		t.Error("storePath rejected a legitimate account key")
	}
}

// TestStoreBaseDirFallsBackToHome: with no override the store lives under the
// operator's home, which is what makes the connection survive a daemon restart.
func TestStoreBaseDirFallsBackToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(storeDirEnv, "")

	want := filepath.Join(home, ".swarmery", "credentials")
	if got := storeBaseDir(); got != want {
		t.Errorf("storeBaseDir() = %q, want %q", got, want)
	}
	if got := storePath("nabu-org"); got != filepath.Join(want, "nabu-org.json") {
		t.Errorf("storePath = %q, want it under %q", got, want)
	}
}

// ── resolution order: the store wins ───────────────────────────────────────

// TestLoadCredsForPrefersTheSwarmeryStore: once an account is connected through
// the dashboard, that connection is the answer — it beats the config-dir file
// for a named account and the legacy chain for the default one.
func TestLoadCredsForPrefersTheSwarmeryStore(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scoped bool
	}{
		{"named account: store beats the config-dir file", true},
		{"default account: store beats the legacy chain", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := scopedFixture(t) // populates ~/.claude and CLAUDE_CONFIG_DIR
			useTempStore(t)
			writeCredFileAt(t, dir, scopedTok, time.Now().Add(24*time.Hour))
			stubKeychain(t, func(context.Context, string) *Creds { return &Creds{AccessToken: "from-the-keychain"} })

			stored := storeFixtureCreds()
			stored.AccessToken = "from-the-swarmery-store"
			if err := writeStoredCreds("nabu-org", stored); err != nil {
				t.Fatalf("writeStoredCreds: %v", err)
			}

			src := Source{Account: "nabu-org"}
			if tc.scoped {
				src.ConfigDir = dir
			}
			got, err := LoadCredsFor(context.Background(), src)
			if err != nil {
				t.Fatalf("LoadCredsFor: %v", err)
			}
			if got.AccessToken != "from-the-swarmery-store" {
				t.Errorf("resolved %q, want the swarmery-store credential", got.AccessToken)
			}
			if !got.FromStore {
				t.Error("FromStore = false on a credential that came from the store")
			}
		})
	}
}

// TestLoadCredsForStoreAbsentIsRungOne is the no-regression guard stated as its
// own assertion rather than left implicit in the rung-1 suite: with no store
// file, every outcome is exactly what shipped before rung 2 — the account's own
// file for a scoped source, ErrNoCreds when it has none, and the chain for the
// default account.
func TestLoadCredsForStoreAbsentIsRungOne(t *testing.T) {
	t.Run("scoped account resolves its own file", func(t *testing.T) {
		dir := scopedFixture(t)
		useTempStore(t)
		writeCredFileAt(t, dir, scopedTok, time.Now().Add(24*time.Hour))
		stubKeychain(t, func(context.Context, string) *Creds { return nil })

		got, err := LoadCredsFor(context.Background(), Source{Account: "nabu-org", ConfigDir: dir})
		if err != nil {
			t.Fatalf("LoadCredsFor: %v", err)
		}
		if got.AccessToken != scopedTok {
			t.Errorf("resolved %q, want %q", got.AccessToken, scopedTok)
		}
		if got.FromStore {
			t.Error("FromStore = true on a config-dir credential — that would license a write-back to the CLI's store")
		}
	})

	t.Run("scoped account with nothing anywhere is ErrNoCreds", func(t *testing.T) {
		dir := scopedFixture(t)
		useTempStore(t)
		// Only the PLAIN item resolves — the default account's login. The
		// scoped lookup may ask for its own suffixed item (empty here) and must
		// still come back with ErrNoCreds, not the default's credential.
		stubKeychain(t, func(_ context.Context, service string) *Creds {
			if service == keychainService {
				return &Creds{AccessToken: "from-the-keychain"}
			}
			return nil
		})

		if _, err := LoadCredsFor(context.Background(), Source{Account: "nabu-org", ConfigDir: dir}); !errors.Is(err, ErrNoCreds) {
			t.Fatalf("LoadCredsFor error = %v, want ErrNoCreds", err)
		}
	})

	t.Run("default account still walks the chain", func(t *testing.T) {
		scopedFixture(t)
		useTempStore(t)
		stubKeychain(t, func(context.Context, string) *Creds { return nil })

		got, err := LoadCredsFor(context.Background(), Source{Account: "default"})
		if err != nil {
			t.Fatalf("LoadCredsFor: %v", err)
		}
		if got.AccessToken != envTok {
			t.Errorf("resolved %q, want the chain's CLAUDE_CONFIG_DIR credential %q", got.AccessToken, envTok)
		}
	})
}

// TestLoadCredsForStoreHonoursOptOut: the kill switch wins before the store is
// opened, exactly as it wins before any file is.
func TestLoadCredsForStoreHonoursOptOut(t *testing.T) {
	useTempStore(t)
	scopedFixture(t)
	if err := writeStoredCreds("nabu-org", storeFixtureCreds()); err != nil {
		t.Fatalf("writeStoredCreds: %v", err)
	}
	src := Source{Account: "nabu-org"}

	if _, err := LoadCredsFor(context.Background(), src); err != nil {
		t.Fatalf("control LoadCredsFor: %v", err)
	}
	t.Setenv(oauthOptOutEnv, "0")
	if _, err := LoadCredsFor(context.Background(), src); !errors.Is(err, ErrDisabled) {
		t.Fatalf("LoadCredsFor error = %v, want ErrDisabled", err)
	}
}

// TestLoadCredsIgnoresTheStore: the package-level LoadCreds (and any zero
// Source) has no account key, so it can never pick up a stored credential — the
// legacy entry point stays byte-identical.
func TestLoadCredsIgnoresTheStore(t *testing.T) {
	dir := useTempStore(t)
	scopedFixture(t)
	stubKeychain(t, func(context.Context, string) *Creds { return nil })
	// A file that would match an empty account key, if one were ever built.
	if err := os.MkdirAll(dir, storeDirMode); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".json"),
		[]byte(`{"claudeAiOauth":{"accessToken":"store-should-not-be-consulted"}}`), storeFileMode); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := LoadCreds(context.Background())
	if err != nil {
		t.Fatalf("LoadCreds: %v", err)
	}
	if got.AccessToken != envTok {
		t.Errorf("LoadCreds resolved %q, want the legacy chain's %q", got.AccessToken, envTok)
	}
}
