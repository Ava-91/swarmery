package claudeacct

// Tests for Provision/Remove. Every one of them runs against fakeHome(t) — the
// $HOME seam armed with a failing stub in TestMain — so no test here can create
// or delete anything in the operator's real home directory. That is the whole
// safety property of a package whose Remove calls os.RemoveAll.

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// dirMode returns path's permission bits, failing the test when it is not a dir.
func dirMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if !fi.IsDir() {
		t.Fatalf("%s is not a directory", path)
	}
	return fi.Mode().Perm()
}

// entryNames lists dir's immediate children, sorted.
func entryNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	slices.Sort(names)
	return names
}

// TestProvisionCreatesTheConfigDir is the shape contract: <dir> at 0700 with an
// empty projects/ inside it, and the returned Account naming both.
func TestProvisionCreatesTheConfigDir(t *testing.T) {
	home := fakeHome(t)

	acct, err := Provision("nabu-org")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}

	want := filepath.Join(home, ".claude-nabu-org")
	if acct.Key != "nabu-org" || acct.ConfigDir != want || acct.IsDefault {
		t.Errorf("Provision = %+v, want {Key:nabu-org ConfigDir:%s IsDefault:false}", acct, want)
	}
	if got := dirMode(t, want); got != 0o700 {
		t.Errorf("config dir mode = %04o, want 0700 — the CLI may drop a credential in here", got)
	}
	projects := filepath.Join(want, "projects")
	if got := dirMode(t, projects); got != 0o700 {
		t.Errorf("projects dir mode = %04o, want 0700", got)
	}
	if got := acct.ProjectsRoot(); got != projects {
		t.Errorf("ProjectsRoot() = %s, want %s", got, projects)
	}

	// A freshly provisioned account is discoverable — that is what makes the
	// daemon able to ingest it after a restart.
	if got := Discover(); !slices.Contains(got, acct) {
		t.Errorf("Discover() = %+v, want it to contain %+v", got, acct)
	}
}

// TestProvisionWritesOnlyDirectories is the VARIANT A guard, stated as a test
// rather than as a promise in a comment: the phase-1 spike concluded swarmery
// must never write CLI credential material, so provisioning must leave nothing
// but directories behind — no .credentials.json, no file of any kind.
func TestProvisionWritesOnlyDirectories(t *testing.T) {
	home := fakeHome(t)

	acct, err := Provision("nabu-org")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}

	if got := entryNames(t, acct.ConfigDir); !slices.Equal(got, []string{"projects"}) {
		t.Errorf("config dir contains %v, want exactly [projects] — provisioning writes no files", got)
	}
	if got := entryNames(t, acct.ProjectsRoot()); len(got) != 0 {
		t.Errorf("projects dir contains %v, want it empty", got)
	}
	if _, err := os.Stat(filepath.Join(acct.ConfigDir, ".credentials.json")); !os.IsNotExist(err) {
		t.Errorf("provisioning wrote a credential file (stat err = %v) — VARIANT A forbids it", err)
	}
	// Nothing was created for the default account either.
	if _, err := os.Stat(filepath.Join(home, ".claude")); !os.IsNotExist(err) {
		t.Errorf("provisioning touched ~/.claude (stat err = %v)", err)
	}
}

// TestProvisionIsIdempotent: provisioning an existing account succeeds and
// leaves its contents alone. A second call must not be able to wipe a config dir
// the CLI has already logged into.
func TestProvisionIsIdempotent(t *testing.T) {
	fakeHome(t)

	first, err := Provision("nabu-org")
	if err != nil {
		t.Fatalf("first Provision: %v", err)
	}
	// Stand in for the state the CLI puts there on its first run.
	marker := filepath.Join(first.ConfigDir, ".claude.json")
	if err := os.WriteFile(marker, []byte(`{"cli":"state"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(first.ProjectsRoot(), "session.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	second, err := Provision("nabu-org")
	if err != nil {
		t.Fatalf("second Provision: %v — an existing dir is success, not a conflict", err)
	}
	if second != first {
		t.Errorf("second Provision = %+v, want %+v", second, first)
	}
	for _, path := range []string{marker, transcript} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("re-provisioning destroyed %s: %v", path, err)
		}
	}
}

// TestProvisionRejectsBadKeys: the key becomes a directory name under $HOME, so
// every one of these must be refused BEFORE any filesystem call — and $HOME must
// come out of the attempt with nothing new in it.
func TestProvisionRejectsBadKeys(t *testing.T) {
	for _, key := range []string{
		"", "   ", ".", "..", "../escape", "a/b", `a\b`, ".hidden",
		"has space", "tab\there", "nul\x00byte",
	} {
		t.Run("key="+key, func(t *testing.T) {
			home := fakeHome(t)
			if _, err := Provision(key); err == nil {
				t.Fatalf("Provision(%q) = nil error, want a rejection", key)
			}
			if err := Remove(key); err == nil {
				t.Errorf("Remove(%q) = nil error, want a rejection", key)
			}
			if got := entryNames(t, home); len(got) != 0 {
				t.Errorf("$HOME gained %v from a rejected key", got)
			}
		})
	}
}

// TestProvisionAndRemoveRefuseTheDefaultAccount is the single most important
// guard in this file: ~/.claude is the operator's primary login, and Remove
// calls os.RemoveAll.
func TestProvisionAndRemoveRefuseTheDefaultAccount(t *testing.T) {
	home := fakeHome(t)
	defaultDir := filepath.Join(home, ".claude")
	mkdirs(t, filepath.Join(defaultDir, "projects"))
	cred := filepath.Join(defaultDir, ".credentials.json")
	const credBody = `{"claudeAiOauth":{"accessToken":"NOT-A-REAL-TOKEN"}}`
	if err := os.WriteFile(cred, []byte(credBody), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Provision("default"); !errors.Is(err, ErrDefaultAccount) {
		t.Errorf("Provision(default) error = %v, want ErrDefaultAccount", err)
	}
	if err := Remove("default"); !errors.Is(err, ErrDefaultAccount) {
		t.Errorf("Remove(default) error = %v, want ErrDefaultAccount", err)
	}

	// The default account survived both attempts, credential included.
	if got, err := os.ReadFile(cred); err != nil || string(got) != credBody {
		t.Errorf("the default account's credential was modified or removed (err = %v)", err)
	}
	if _, err := os.Stat(filepath.Join(defaultDir, "projects")); err != nil {
		t.Errorf("the default account's transcripts were removed: %v", err)
	}
}

// TestRemoveDeletesTheConfigDir: the happy path, plus the idempotent repeat.
func TestRemoveDeletesTheConfigDir(t *testing.T) {
	home := fakeHome(t)
	mkdirs(t, filepath.Join(home, ".claude", "projects")) // the default account, a bystander

	acct, err := Provision("nabu-org")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if err := os.WriteFile(filepath.Join(acct.ConfigDir, ".claude.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Remove("nabu-org"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(acct.ConfigDir); !os.IsNotExist(err) {
		t.Errorf("the config dir survived Remove (stat err = %v)", err)
	}
	if err := Remove("nabu-org"); err != nil {
		t.Errorf("second Remove = %v, want nil — an account already gone is already removed", err)
	}
	// The default account was never in scope.
	if _, err := os.Stat(filepath.Join(home, ".claude", "projects")); err != nil {
		t.Errorf("removing another account touched ~/.claude: %v", err)
	}
}

// TestProvisionAndRemoveFollowANonCanonicalDir: an operator whose config dir is
// ~/.claude.work (dot, not dash) has an account keyed "work". Both operations
// must act on the dir that account ACTUALLY lives in — provisioning the
// canonical ~/.claude-work would give one account two dirs, and removing it
// would report success while leaving the real dir behind.
func TestProvisionAndRemoveFollowANonCanonicalDir(t *testing.T) {
	home := fakeHome(t)
	actual := filepath.Join(home, ".claude.work")
	mkdirs(t, filepath.Join(actual, "projects"))

	acct, err := Provision("work")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if acct.ConfigDir != actual {
		t.Fatalf("Provision resolved %s, want the discovered dir %s", acct.ConfigDir, actual)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude-work")); !os.IsNotExist(err) {
		t.Errorf("a second config dir was created for one account (stat err = %v)", err)
	}

	if err := Remove("work"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(actual); !os.IsNotExist(err) {
		t.Errorf("Remove reported success but %s is still there", actual)
	}
}

// TestProvisionRefusesANonDirectory: a FILE where the config dir belongs is a
// hard error, never a silent overwrite.
func TestProvisionRefusesANonDirectory(t *testing.T) {
	home := fakeHome(t)
	path := filepath.Join(home, ".claude-nabu-org")
	if err := os.WriteFile(path, []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Provision("nabu-org"); err == nil {
		t.Fatal("Provision over a file = nil error, want a refusal")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "not a dir" {
		t.Errorf("the existing file was modified (err = %v)", err)
	}
}
