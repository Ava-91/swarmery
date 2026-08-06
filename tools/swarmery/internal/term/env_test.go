package term

import (
	"os"
	"testing"
)

// Start must hand the caller's environment delta down to the starter unchanged —
// that is the whole plumbing between the HTTP handler's claudeacct.EnvFor(cwd)
// and the shell the operator types into.
func TestStartDeliversEnvToSpawn(t *testing.T) {
	st := &stubStarter{exitOnSIGHUP: true}
	m := NewManager(Config{starter: st, Shell: "/stub"})

	want := []string{"CLAUDE_CONFIG_DIR=/home/u/.claude-nabu-org"}
	s, err := m.Start("/tmp", want, 80, 24)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close()

	got := st.lastEnv()
	if len(got) != len(want) {
		t.Fatalf("starter got env %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("env[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// An unbound project passes nil, and nothing about the child's environment
// changes — the dock session behaves exactly as it did before this parameter
// existed.
func TestStartWithEmptyEnvLeavesEnvUnchanged(t *testing.T) {
	st := &stubStarter{exitOnSIGHUP: true}
	m := NewManager(Config{starter: st, Shell: "/stub"})

	s, err := m.Start("/tmp", nil, 80, 24)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close()

	if got := st.lastEnv(); len(got) != 0 {
		t.Errorf("starter got env %v, want no delta", got)
	}
}

// ptyEnv with no delta must reproduce the pre-feature line byte for byte:
// append(os.Environ(), "TERM=xterm-256color").
func TestPtyEnvNilDeltaMatchesLegacyEnvironment(t *testing.T) {
	want := append(os.Environ(), "TERM=xterm-256color")
	got := ptyEnv(nil)
	if len(got) != len(want) {
		t.Fatalf("ptyEnv(nil) length %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("env[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// The delta lands LAST, after TERM and after everything inherited: os/exec keeps
// the last occurrence of a duplicated key, so this ordering is what makes an
// account binding win over a CLAUDE_CONFIG_DIR the daemon itself inherited.
func TestPtyEnvAppendsDeltaLast(t *testing.T) {
	delta := []string{"CLAUDE_CONFIG_DIR=/home/u/.claude-nabu-org"}
	got := ptyEnv(delta)

	if n := len(got); n != len(os.Environ())+2 {
		t.Fatalf("ptyEnv length %d, want os.Environ()+TERM+delta", n)
	}
	if last := got[len(got)-1]; last != delta[0] {
		t.Errorf("last env entry = %q, want the delta %q", last, delta[0])
	}
	if term := got[len(got)-2]; term != "TERM=xterm-256color" {
		t.Errorf("entry before the delta = %q, want TERM=xterm-256color", term)
	}
}
