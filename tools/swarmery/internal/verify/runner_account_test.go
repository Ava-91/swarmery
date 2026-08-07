package verify

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/claudeacct"
)

// unsetMarker is what the fake claude prints when CLAUDE_CONFIG_DIR is not in
// its environment AT ALL. `${VAR-default}` (no colon) distinguishes "unset" from
// "set to empty" — the "nothing broke" claim is that the variable is absent, not
// that it is empty.
const unsetMarker = "__UNSET__"

// unsetConfigDir removes CLAUDE_CONFIG_DIR from the TEST process's environment
// for the duration of the test, so a developer shell (or a daemon running under
// a non-default account) cannot leak into os.Environ() and skew the result.
func unsetConfigDir(t *testing.T) {
	t.Helper()
	prev, had := os.LookupEnv("CLAUDE_CONFIG_DIR")
	if !had {
		return
	}
	if err := os.Unsetenv("CLAUDE_CONFIG_DIR"); err != nil {
		t.Fatalf("unset CLAUDE_CONFIG_DIR: %v", err)
	}
	t.Cleanup(func() { os.Setenv("CLAUDE_CONFIG_DIR", prev) })
}

// childConfigDir runs the REAL ClaudeRunner against a fake `claude` that reports
// its own CLAUDE_CONFIG_DIR on stdout, and returns what the CHILD saw
// (unsetMarker when the variable is absent). The verifier captures stdout, so
// unlike dispatch this needs no scratch file.
func childConfigDir(t *testing.T, spec RunSpec, cwd string) string {
	t.Helper()
	fakeClaudeRunner(t, `printf '%s\n' "${CLAUDE_CONFIG_DIR-`+unsetMarker+`}"; exit 0`)
	spec.Cwd = cwd
	run, err := ClaudeRunner{Timeout: 30 * time.Second}.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return strings.TrimSpace(run.Output)
}

// The "nothing broke" criterion for verification: a task whose project has no
// account binding spawns with no CLAUDE_CONFIG_DIR.
func TestVerifySpawnUnboundAccountLeavesChildEnvUntouched(t *testing.T) {
	unsetConfigDir(t)
	t.Setenv("HOME", t.TempDir())
	got := childConfigDir(t, RunSpec{Prompt: "p", SessionUUID: "acct-unbound"}, t.TempDir())
	if got != unsetMarker {
		t.Errorf("child saw CLAUDE_CONFIG_DIR=%q, want it absent", got)
	}
}

// "No binding ≡ bound to default" — keyed on the account KEY, never on an empty
// ConfigDir (claudeacct.Account.ConfigDir IS populated for the default account).
func TestVerifySpawnDefaultAccountKeyProducesNoDelta(t *testing.T) {
	unsetConfigDir(t)
	t.Setenv("HOME", t.TempDir())
	got := childConfigDir(t, RunSpec{Prompt: "p", SessionUUID: "acct-default", Account: "default"}, t.TempDir())
	if got != unsetMarker {
		t.Errorf("default account produced CLAUDE_CONFIG_DIR=%q, want no variable at all", got)
	}
}

// A bound project's verifier run reaches the CLI with the account's config dir.
func TestVerifySpawnBoundAccountSetsConfigDir(t *testing.T) {
	unsetConfigDir(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	got := childConfigDir(t, RunSpec{Prompt: "p", SessionUUID: "acct-bound", Account: "nabu-org"}, t.TempDir())
	if want := filepath.Join(home, ".claude-nabu-org"); got != want {
		t.Errorf("child CLAUDE_CONFIG_DIR = %q, want %q", got, want)
	}
}

// Direct test of trap A3 on the verify side: Cwd is a worktree with NO
// .claude/settings.local.json (asserted as a precondition), yet the account still
// reaches the child because it travels in the spec.
func TestVerifySpawnAppliesAccountWhenCwdHasNoProjectSettings(t *testing.T) {
	unsetConfigDir(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	worktree := t.TempDir()

	if _, err := os.Stat(filepath.Join(worktree, claudeacct.BindingFile)); !os.IsNotExist(err) {
		t.Fatalf("precondition: worktree must carry no %s (stat err = %v)", claudeacct.BindingFile, err)
	}
	if env := claudeacct.EnvFor(worktree); env != nil {
		t.Fatalf("precondition: EnvFor(worktree) = %v, want nil — the trap this test guards is gone", env)
	}

	got := childConfigDir(t, RunSpec{Prompt: "p", SessionUUID: "acct-worktree", Account: "nabu-org"}, worktree)
	if want := filepath.Join(home, ".claude-nabu-org"); got != want {
		t.Errorf("worktree run CLAUDE_CONFIG_DIR = %q, want %q — the account was resolved from cwd, not from the spec", got, want)
	}
}

// The byte-for-byte half of the "nothing broke" criterion: the exact expression
// the spawn site uses adds nothing for an unbound project.
func TestVerifyUnboundSpawnEnvIsByteIdenticalToOsEnviron(t *testing.T) {
	base := os.Environ()
	got := append(os.Environ(), claudeacct.EnvForAccount("")...) // the spawn line, verbatim
	if len(got) != len(base) {
		t.Fatalf("env length %d, want %d (an unbound spawn must add nothing)", len(got), len(base))
	}
	for i := range base {
		if got[i] != base[i] {
			t.Errorf("env[%d] = %q, want %q", i, got[i], base[i])
		}
	}
}
