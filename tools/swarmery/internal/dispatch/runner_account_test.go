package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/claudeacct"
)

// unsetMarker is what the fake claude prints when CLAUDE_CONFIG_DIR is not in
// its environment AT ALL. `${VAR-default}` (no colon) distinguishes "unset" from
// "set to empty", which matters here: the whole "nothing broke" claim is that an
// unbound project's child has no such variable, not that it has an empty one.
const unsetMarker = "__UNSET__"

// unsetConfigDir removes CLAUDE_CONFIG_DIR from the TEST process's environment
// for the duration of the test. Without it these tests would inherit whatever
// the developer's shell (or a daemon under a non-default account) exports and
// report a false positive/negative — os.Environ() is the base every spawn site
// appends to.
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

// childConfigDir spawns the run through the REAL ClaudeRunner.Start against a
// fake `claude` that reports its own CLAUDE_CONFIG_DIR into cwd, and returns
// what the CHILD saw (unsetMarker when the variable is absent).
//
// Observing the child rather than cmd.Env is deliberate: cmd.Env is not readable
// from the test, and the contract this phase ships is about the environment the
// spawned process actually runs with.
func childConfigDir(t *testing.T, spec RunSpec, cwd string) string {
	t.Helper()
	fakeClaude(t, `printf '%s\n' "${CLAUDE_CONFIG_DIR-`+unsetMarker+`}" > "$PWD/acct.txt"; exit 0`)
	spec.Cwd = cwd
	if _, err := (ClaudeRunner{}).Start(context.Background(), spec); err != nil {
		t.Fatalf("Start: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(cwd, "acct.txt"))
	if err != nil {
		t.Fatalf("read acct.txt: %v", err)
	}
	return strings.TrimSpace(string(b))
}

// The "nothing broke" criterion: a task whose project has no account binding
// spawns with no CLAUDE_CONFIG_DIR — the process environment is what it was
// before this feature existed.
func TestSpawnUnboundAccountLeavesChildEnvUntouched(t *testing.T) {
	unsetConfigDir(t)
	t.Setenv("HOME", t.TempDir())
	got := childConfigDir(t, RunSpec{Prompt: "p", SessionUUID: "acct-unbound"}, t.TempDir())
	if got != unsetMarker {
		t.Errorf("child saw CLAUDE_CONFIG_DIR=%q, want it absent", got)
	}
}

// "No binding ≡ bound to default" — decided by the KEY being the default one,
// never by an empty ConfigDir (claudeacct.Account.ConfigDir IS populated for the
// default account, unlike usage.Source.ConfigDir).
func TestSpawnDefaultAccountKeyProducesNoDelta(t *testing.T) {
	unsetConfigDir(t)
	t.Setenv("HOME", t.TempDir())
	got := childConfigDir(t, RunSpec{Prompt: "p", SessionUUID: "acct-default", Account: "default"}, t.TempDir())
	if got != unsetMarker {
		t.Errorf("default account produced CLAUDE_CONFIG_DIR=%q, want no variable at all", got)
	}
}

// A bound project's run reaches the CLI with the account's config dir.
func TestSpawnBoundAccountSetsConfigDir(t *testing.T) {
	unsetConfigDir(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	got := childConfigDir(t, RunSpec{Prompt: "p", SessionUUID: "acct-bound", Account: "nabu-org"}, t.TempDir())
	if want := filepath.Join(home, ".claude-nabu-org"); got != want {
		t.Errorf("child CLAUDE_CONFIG_DIR = %q, want %q", got, want)
	}
}

// Direct test of trap A3: Cwd is a worktree with NO .claude/settings.local.json,
// so a cwd-side resolve yields nothing (asserted as a precondition, so this test
// fails loudly if that ever stops being true) — yet the account still reaches the
// child, because it travels in the spec instead.
func TestSpawnAppliesAccountWhenCwdHasNoProjectSettings(t *testing.T) {
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

// The byte-for-byte half of the "nothing broke" criterion. The child test above
// proves the observable consequence (no variable); this pins the exact expression
// the spawn site uses, which the child cannot show — a shell adds PWD/SHLVL/_ of
// its own, so the full environment can only be compared here.
func TestUnboundSpawnEnvIsByteIdenticalToOsEnviron(t *testing.T) {
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
