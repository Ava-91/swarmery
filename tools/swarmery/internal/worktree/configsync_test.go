package worktree

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyMissing(t *testing.T) {
	tests := []struct {
		name string
		// setup receives (src, dst) paths under a fresh temp dir and prepares
		// whatever files the case needs before copyMissing runs.
		setup     func(t *testing.T, src, dst string)
		wantCopy  bool
		wantErr   bool
		wantDstEq string // checked when non-empty
	}{
		{
			name: "source missing → no-op, not an error",
			setup: func(t *testing.T, src, dst string) {
				// Neither file exists.
			},
			wantCopy: false,
			wantErr:  false,
		},
		{
			name: "source present, dst missing → copied",
			setup: func(t *testing.T, src, dst string) {
				mustMkdir(t, filepath.Dir(src))
				mustWrite(t, src, `{"enabledPlugins":["core@swarmery"]}`)
			},
			wantCopy:  true,
			wantErr:   false,
			wantDstEq: `{"enabledPlugins":["core@swarmery"]}`,
		},
		{
			name: "source present, dst already there (git materialized a tracked file) → left alone",
			setup: func(t *testing.T, src, dst string) {
				mustMkdir(t, filepath.Dir(src))
				mustWrite(t, src, "source version")
				mustMkdir(t, filepath.Dir(dst))
				mustWrite(t, dst, "worktree's own tracked version")
			},
			wantCopy:  false,
			wantErr:   false,
			wantDstEq: "worktree's own tracked version",
		},
		{
			name: "source is a directory, not a file → no-op",
			setup: func(t *testing.T, src, dst string) {
				mustMkdir(t, src)
			},
			wantCopy: false,
			wantErr:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			src := filepath.Join(root, "repo", ".claude", "settings.json")
			dst := filepath.Join(root, "worktree", ".claude", "settings.json")
			tc.setup(t, src, dst)

			copied, err := copyMissing(src, dst)
			if (err != nil) != tc.wantErr {
				t.Fatalf("copyMissing err = %v, wantErr %v", err, tc.wantErr)
			}
			if copied != tc.wantCopy {
				t.Fatalf("copyMissing copied = %v, want %v", copied, tc.wantCopy)
			}
			if tc.wantDstEq != "" {
				got, err := os.ReadFile(dst)
				if err != nil {
					t.Fatalf("read dst: %v", err)
				}
				if string(got) != tc.wantDstEq {
					t.Fatalf("dst content = %q, want %q", got, tc.wantDstEq)
				}
			}
		})
	}
}

// TestCopyMissingPreservesPermissions confirms a copied file keeps the
// source's mode bits rather than always landing at a fixed 0644 — mostly so a
// future refactor notices if it starts hardcoding a mode.
func TestCopyMissingPreservesPermissions(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "repo", ".claude", "settings.local.json")
	dst := filepath.Join(root, "worktree", ".claude", "settings.local.json")
	mustMkdir(t, filepath.Dir(src))
	if err := os.WriteFile(src, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}

	copied, err := copyMissing(src, dst)
	if err != nil || !copied {
		t.Fatalf("copyMissing = (%v, %v), want (true, nil)", copied, err)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat dst: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("dst perm = %v, want 0600", info.Mode().Perm())
	}
}

// TestSyncUntrackedConfig exercises the three configFilesToSync names
// together: settings.json and project.json are untracked-but-present (the
// issue #192 repro), settings.local.json is absent entirely (a project that
// never had one), and one destination is pre-populated to prove sync never
// overwrites a file the worktree already has.
func TestSyncUntrackedConfig(t *testing.T) {
	root := t.TempDir()
	repoRoot := filepath.Join(root, "repo")
	worktreePath := filepath.Join(root, "worktree")

	mustMkdir(t, filepath.Join(repoRoot, ".claude"))
	mustWrite(t, filepath.Join(repoRoot, ".claude", "settings.json"), `{"enabledPlugins":{"core":"swarmery"}}`)
	mustWrite(t, filepath.Join(repoRoot, ".claude", "project.json"), `{"mainApp":"swarmery"}`)
	// No settings.local.json in the source repo at all.

	// The worktree already has its OWN project.json (e.g. a tracked one from a
	// prior partial commit) — sync must not clobber it.
	mustMkdir(t, filepath.Join(worktreePath, ".claude"))
	mustWrite(t, filepath.Join(worktreePath, ".claude", "project.json"), `{"mainApp":"worktree-own-copy"}`)

	syncUntrackedConfig(repoRoot, worktreePath)

	got, err := os.ReadFile(filepath.Join(worktreePath, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("settings.json was not copied into the worktree: %v", err)
	}
	if string(got) != `{"enabledPlugins":{"core":"swarmery"}}` {
		t.Fatalf("settings.json content = %q, want the source's content", got)
	}

	got, err = os.ReadFile(filepath.Join(worktreePath, ".claude", "project.json"))
	if err != nil {
		t.Fatalf("read worktree project.json: %v", err)
	}
	if string(got) != `{"mainApp":"worktree-own-copy"}` {
		t.Fatalf("project.json was overwritten: got %q, want the worktree's own copy preserved", got)
	}

	if fileExists(filepath.Join(worktreePath, ".claude", "settings.local.json")) {
		t.Fatal("settings.local.json should not exist — the source repo never had one")
	}
}
