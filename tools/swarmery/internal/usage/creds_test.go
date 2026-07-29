package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestMain is a hard safety guard for the whole package: no test may read the
// operator's real credential file or trigger a macOS keychain prompt. HOME is
// redirected to a throwaway directory and the keychain seam is stubbed out;
// tests that need the keychain path install their own stub explicitly.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "usage-test-home")
	if err != nil {
		fmt.Fprintln(os.Stderr, "usage: cannot create temp HOME:", err)
		os.Exit(1)
	}
	os.Setenv("HOME", home)
	os.Unsetenv(configDirEnv)
	os.Unsetenv(oauthOptOutEnv)
	keychainCreds = func(context.Context) *Creds { return nil }

	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

const (
	fakeAccess  = "NOT-A-REAL-TOKEN-fixture-access"
	fakeRefresh = "NOT-A-REAL-TOKEN-fixture-refresh"
)

// writeCredFile drops the credentials fixture at dir/.credentials.json.
func writeCredFile(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "credentials.json"))
	if err != nil {
		t.Fatalf("read credentials fixture: %v", err)
	}
	path := filepath.Join(dir, credentialsFile)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// stubKeychain installs a keychain seam for one test and restores it after.
func stubKeychain(t *testing.T, fn func(context.Context) *Creds) *int {
	t.Helper()
	calls := 0
	prev := keychainCreds
	keychainCreds = func(ctx context.Context) *Creds {
		calls++
		return fn(ctx)
	}
	t.Cleanup(func() { keychainCreds = prev })
	return &calls
}

func TestCredsStringRedacts(t *testing.T) {
	c := Creds{
		AccessToken:  fakeAccess,
		RefreshToken: fakeRefresh,
		Scopes:       []string{requiredScope},
	}
	for _, rendered := range []string{
		fmt.Sprintf("%v", c),
		fmt.Sprintf("%v", &c),
		fmt.Sprintf("%s", c),
		fmt.Sprintf("%+v", c),
		fmt.Sprint(c),
	} {
		if strings.Contains(rendered, fakeAccess) || strings.Contains(rendered, fakeRefresh) {
			t.Fatalf("rendered Creds leaked a token: %s", rendered)
		}
		if !strings.Contains(rendered, "redacted") {
			t.Errorf("rendered Creds = %q, want a redaction marker", rendered)
		}
	}
}

func TestLoadCredsResolutionOrder(t *testing.T) {
	cases := []struct {
		name string
		// dir returns the directory the credential file goes in, relative to
		// the temp HOME, plus whether CLAUDE_CONFIG_DIR should point at it.
		subdir     string
		useConfDir bool
	}{
		{"CLAUDE_CONFIG_DIR wins", "custom-config", true},
		{"~/.claude", ".claude", false},
		{"~/.config/claude", filepath.Join(".config", "claude"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			dir := filepath.Join(home, tc.subdir)
			writeCredFile(t, dir)
			if tc.useConfDir {
				t.Setenv(configDirEnv, dir)
			} else {
				t.Setenv(configDirEnv, "")
			}
			calls := stubKeychain(t, func(context.Context) *Creds { return nil })

			got, err := LoadCreds(context.Background())
			if err != nil {
				t.Fatalf("LoadCreds: %v", err)
			}
			if got.AccessToken != fakeAccess || got.RefreshToken != fakeRefresh {
				t.Errorf("LoadCreds returned the wrong credential (tokens elided)")
			}
			if got.SubscriptionType != "max" {
				t.Errorf("subscriptionType = %q, want %q", got.SubscriptionType, "max")
			}
			if !hasScope(got.Scopes, requiredScope) {
				t.Errorf("scopes = %v, want to include %q", got.Scopes, requiredScope)
			}
			if got.ExpiresAt == 0 {
				t.Error("expiresAt = 0, want the fixture's value")
			}
			if *calls != 0 {
				t.Errorf("keychain consulted %d times, want 0 when a file source hit", *calls)
			}
		})
	}
}

func TestLoadCredsPrefersConfigDirOverHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	confDir := filepath.Join(home, "custom")
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, credentialsFile),
		[]byte(`{"claudeAiOauth":{"accessToken":"from-config-dir"}}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	writeCredFile(t, filepath.Join(home, ".claude"))
	t.Setenv(configDirEnv, confDir)
	stubKeychain(t, func(context.Context) *Creds { return nil })

	got, err := LoadCreds(context.Background())
	if err != nil {
		t.Fatalf("LoadCreds: %v", err)
	}
	if got.AccessToken != "from-config-dir" {
		t.Error("LoadCreds did not prefer CLAUDE_CONFIG_DIR over ~/.claude")
	}
}

func TestLoadCredsSkipsUnusableFilesAndFallsThrough(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(configDirEnv, "")

	// ~/.claude holds junk; ~/.config/claude holds the real thing.
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, credentialsFile), []byte("not json at all"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	writeCredFile(t, filepath.Join(home, ".config", "claude"))
	stubKeychain(t, func(context.Context) *Creds { return nil })

	got, err := LoadCreds(context.Background())
	if err != nil {
		t.Fatalf("LoadCreds: %v", err)
	}
	if got.AccessToken != fakeAccess {
		t.Error("LoadCreds did not fall through the unparseable file")
	}
}

func TestLoadCredsMissingIsSentinel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(configDirEnv, "")
	calls := stubKeychain(t, func(context.Context) *Creds { return nil })

	got, err := LoadCreds(context.Background())
	if !errors.Is(err, ErrNoCreds) {
		t.Fatalf("LoadCreds error = %v, want ErrNoCreds", err)
	}
	if got != nil {
		t.Errorf("LoadCreds creds = %v, want nil", got)
	}
	wantCalls := 0
	if runtime.GOOS == "darwin" {
		wantCalls = 1
	}
	if *calls != wantCalls {
		t.Errorf("keychain consulted %d times on %s, want %d", *calls, runtime.GOOS, wantCalls)
	}
}

func TestLoadCredsFromKeychain(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("keychain source is macOS-only")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv(configDirEnv, "")
	stubKeychain(t, func(context.Context) *Creds {
		return &Creds{AccessToken: "from-keychain", Scopes: []string{requiredScope}}
	})

	got, err := LoadCreds(context.Background())
	if err != nil {
		t.Fatalf("LoadCreds: %v", err)
	}
	if got.AccessToken != "from-keychain" {
		t.Error("LoadCreds did not fall back to the keychain")
	}
}

// TestLoadCredsDisabledTouchesNothing is the opt-out contract: with
// SWARMERY_USAGE_OAUTH=0 the credential file that WOULD be read is left alone
// and the keychain is never consulted. The positive control in the same test
// proves the file really was readable.
func TestLoadCredsDisabledTouchesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(configDirEnv, "")
	writeCredFile(t, filepath.Join(home, ".claude"))
	calls := stubKeychain(t, func(context.Context) *Creds {
		t.Error("keychain consulted while SWARMERY_USAGE_OAUTH=0")
		return nil
	})

	// Positive control: without the opt-out this exact setup resolves.
	if _, err := LoadCreds(context.Background()); err != nil {
		t.Fatalf("control LoadCreds: %v", err)
	}

	t.Setenv(oauthOptOutEnv, "0")
	got, err := LoadCreds(context.Background())
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("LoadCreds error = %v, want ErrDisabled", err)
	}
	if got != nil {
		t.Errorf("LoadCreds creds = %v, want nil", got)
	}
	if *calls != 0 {
		t.Errorf("keychain consulted %d times while disabled, want 0", *calls)
	}
}

func TestLoadCredsOptOutOnlyMatchesZero(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(configDirEnv, "")
	writeCredFile(t, filepath.Join(home, ".claude"))
	stubKeychain(t, func(context.Context) *Creds { return nil })

	for _, v := range []string{"1", "", "true", "false"} {
		t.Setenv(oauthOptOutEnv, v)
		if _, err := LoadCreds(context.Background()); err != nil {
			t.Errorf("SWARMERY_USAGE_OAUTH=%q: LoadCreds error = %v, want the credential", v, err)
		}
	}
}

func TestParseCreds(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string // expected access token; "" means nil result
	}{
		{"nested claudeAiOauth", `{"claudeAiOauth":{"accessToken":"nested"}}`, "nested"},
		{"bare object at root", `{"accessToken":"bare","refreshToken":"r"}`, "bare"},
		{"nested wins over bare", `{"accessToken":"bare","claudeAiOauth":{"accessToken":"nested"}}`, "nested"},
		{"nested without a token falls back to bare", `{"accessToken":"bare","claudeAiOauth":{"scopes":[]}}`, "bare"},
		{"empty object", `{}`, ""},
		{"null", `null`, ""},
		{"not json", `<html>nope</html>`, ""},
		{"array", `[1,2,3]`, ""},
		{"empty input", ``, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseCreds([]byte(tc.raw))
			if tc.want == "" {
				if got != nil {
					t.Errorf("parseCreds(%s) = non-nil, want nil", tc.raw)
				}
				return
			}
			if got == nil {
				t.Fatalf("parseCreds(%s) = nil, want a credential", tc.raw)
			}
			if got.AccessToken != tc.want {
				t.Errorf("accessToken = %q, want %q", got.AccessToken, tc.want)
			}
		})
	}
}

func TestParseCredsCarriesAllFields(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "credentials.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got := parseCreds(raw)
	if got == nil {
		t.Fatal("parseCreds(fixture) = nil")
	}
	var want struct {
		ClaudeAiOauth rawCreds `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if got.AccessToken != want.ClaudeAiOauth.AccessToken ||
		got.RefreshToken != want.ClaudeAiOauth.RefreshToken ||
		got.ExpiresAt != want.ClaudeAiOauth.ExpiresAt ||
		got.SubscriptionType != want.ClaudeAiOauth.SubscriptionType ||
		len(got.Scopes) != len(want.ClaudeAiOauth.Scopes) {
		t.Error("parseCreds dropped a field from the fixture")
	}
}

func TestKeychainArgs(t *testing.T) {
	got := keychainArgs()
	want := []string{"find-generic-password", "-s", "Claude Code-credentials", "-w"}
	if len(got) != len(want) {
		t.Fatalf("keychainArgs() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("keychainArgs()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// stubSecurityBin points securityBin at a throwaway shell script so
// readKeychainCreds can be exercised without ever running the real `security`
// binary against the operator's login keychain.
func stubSecurityBin(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "security-stub")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	prev := securityBin
	securityBin = path
	t.Cleanup(func() { securityBin = prev })
}

func TestReadKeychainCreds(t *testing.T) {
	fixture, err := filepath.Abs(filepath.Join("testdata", "credentials.json"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	stubSecurityBin(t, `cat "`+fixture+`"`)

	got := readKeychainCreds(context.Background())
	if got == nil {
		t.Fatal("readKeychainCreds = nil, want the stubbed credential")
	}
	if got.AccessToken != fakeAccess {
		t.Error("readKeychainCreds returned the wrong credential")
	}
}

func TestReadKeychainCredsFailures(t *testing.T) {
	t.Run("non-zero exit", func(t *testing.T) {
		stubSecurityBin(t, "exit 44")
		if got := readKeychainCreds(context.Background()); got != nil {
			t.Errorf("readKeychainCreds = %v, want nil on a failed lookup", got)
		}
	})
	t.Run("garbage output", func(t *testing.T) {
		stubSecurityBin(t, `echo "not json"`)
		if got := readKeychainCreds(context.Background()); got != nil {
			t.Errorf("readKeychainCreds = %v, want nil on unparseable output", got)
		}
	})
	t.Run("missing binary", func(t *testing.T) {
		prev := securityBin
		securityBin = filepath.Join(t.TempDir(), "does-not-exist")
		t.Cleanup(func() { securityBin = prev })
		if got := readKeychainCreds(context.Background()); got != nil {
			t.Errorf("readKeychainCreds = %v, want nil when the binary is absent", got)
		}
	})
}
