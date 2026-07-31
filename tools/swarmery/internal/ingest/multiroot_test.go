package ingest

// Multi-root ingest: one machine can run several Claude Code subscriptions
// side by side (CLAUDE_CONFIG_DIR), each config dir owning its own projects/
// transcript tree. The pipeline scans ALL configured roots, tolerates roots
// that only exist on some machines, and keeps every file tagged with the root
// it came from (the account dimension).

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// rootLine builds one minimal user-prompt JSONL line for an arbitrary
// session/cwd pair (the package-level line() helper hard-codes both).
func rootLine(sessionUUID, cwd, uuid, ts, text string) string {
	return fmt.Sprintf(`{"type":"user","parentUuid":null,"isSidechain":false,"promptId":"p-%s","promptSource":"typed","message":{"role":"user","content":%q},"uuid":"%s","timestamp":"%s","cwd":%q,"sessionId":%q,"version":"2.1.170","gitBranch":"main"}`+"\n",
		uuid, text, uuid, ts, cwd, sessionUUID)
}

// TestBackfillScansEveryProjectsRoot: one Backfill over two roots ingests the
// transcripts of BOTH and attributes each session to the project of its own
// cwd — the multi-subscription case (~/.claude + ~/.claude-<account>). A third
// configured root that does not exist is skipped, not an error.
func TestBackfillScansEveryProjectsRoot(t *testing.T) {
	db := testDB(t)
	rootA, rootB := t.TempDir(), t.TempDir()
	missing := filepath.Join(t.TempDir(), "not-on-this-machine")

	mustWrite(t, filepath.Join(rootA, "-home-dev-alpha", "sess-a.jsonl"),
		rootLine("11111111-0000-4000-8000-00000000000a", "/home/dev/alpha",
			"u-a-1", "2026-07-20T10:00:00Z", "alpha prompt"))
	mustWrite(t, filepath.Join(rootB, "-home-dev-beta", "sess-b.jsonl"),
		rootLine("22222222-0000-4000-8000-00000000000b", "/home/dev/beta",
			"u-b-1", "2026-07-20T11:00:00Z", "beta prompt"))

	p := NewPipeline(db, Config{ProjectsRoots: []string{rootA, missing, rootB}}, nil)
	m := p.Backfill(context.Background())
	if m.Files != 2 || m.Errors != 0 {
		t.Fatalf("backfill metrics = %s, want files=2 errors=0", m)
	}

	for _, c := range []struct{ uuid, project string }{
		{"11111111-0000-4000-8000-00000000000a", "/home/dev/alpha"},
		{"22222222-0000-4000-8000-00000000000b", "/home/dev/beta"},
	} {
		var got string
		if err := db.QueryRow(
			`SELECT p.path FROM sessions s JOIN projects p ON p.id = s.project_id
			 WHERE s.session_uuid = ?`, c.uuid).Scan(&got); err != nil {
			t.Fatalf("session %s not ingested: %v", c.uuid, err)
		}
		if got != c.project {
			t.Errorf("session %s attributed to %q, want %q", c.uuid, got, c.project)
		}
	}
}

// TestDiscoverTagsFilesWithTheirOwnRoot: every discovered transcript carries
// the root it was found under, so a later per-session account stamp can be
// derived from the config dir.
func TestDiscoverTagsFilesWithTheirOwnRoot(t *testing.T) {
	db := testDB(t)
	rootA, rootB := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(rootA, "-home-dev-alpha", "sess-a.jsonl"), "{}\n")
	mustWrite(t, filepath.Join(rootB, "-home-dev-beta", "sess-b.jsonl"), "{}\n")

	p := NewPipeline(db, Config{ProjectsRoots: []string{rootA, rootB}}, nil)
	files := p.discover()
	if len(files) != 2 {
		t.Fatalf("discover = %v, want 2 files", files)
	}
	for _, f := range files {
		if want := p.rootFor(f.path); f.root != want {
			t.Errorf("discover tagged %s with root %q, want %q", f.path, f.root, want)
		}
		if filepath.Dir(filepath.Dir(f.path)) != f.root {
			t.Errorf("file %s is not under its tagged root %s", f.path, f.root)
		}
	}
}

// TestRootForPrefersTheLongestRoot: a root nested inside another one still
// owns its own files, so a "~/.claude/projects" + "~/.claude/projects/extra"
// pair cannot cross-attribute.
func TestRootForPrefersTheLongestRoot(t *testing.T) {
	outer := t.TempDir()
	inner := filepath.Join(outer, "extra")
	p := NewPipeline(testDB(t), Config{ProjectsRoots: []string{outer, inner}}, nil)

	if got := p.rootFor(filepath.Join(inner, "-slug", "s.jsonl")); got != inner {
		t.Errorf("rootFor(inner file) = %q, want %q", got, inner)
	}
	if got := p.rootFor(filepath.Join(outer, "-slug", "s.jsonl")); got != outer {
		t.Errorf("rootFor(outer file) = %q, want %q", got, outer)
	}
	if got := p.rootFor(filepath.Join(t.TempDir(), "elsewhere.jsonl")); got != "" {
		t.Errorf("rootFor(unrelated path) = %q, want \"\"", got)
	}
}

// TestExistingRootsPartitions: the shared tolerate-some / refuse-all input.
func TestExistingRootsPartitions(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(dir, "gone")

	present, missing := ExistingRoots([]string{dir, gone, file})
	if len(present) != 1 || present[0] != dir {
		t.Errorf("present = %v, want [%s]", present, dir)
	}
	// A regular file is not a usable root — it belongs with the missing ones.
	if len(missing) != 2 || missing[0] != gone || missing[1] != file {
		t.Errorf("missing = %v, want [%s %s]", missing, gone, file)
	}
}

// TestRunToleratesMissingRootsButRefusesWhenAllAreGone: a roots list is shared
// across machines, so an absent config dir must only warn — while zero usable
// roots keeps the single-root fatal contract.
func TestRunToleratesMissingRootsButRefusesWhenAllAreGone(t *testing.T) {
	db := testDB(t)
	live := t.TempDir()
	mustWrite(t, filepath.Join(live, "-home-dev-alpha", "sess-a.jsonl"),
		rootLine("33333333-0000-4000-8000-00000000000c", "/home/dev/alpha",
			"u-c-1", "2026-07-20T12:00:00Z", "alpha prompt"))
	gone := filepath.Join(t.TempDir(), "not-on-this-machine")

	// All missing → the ErrNoProjectsRoots refusal.
	p := NewPipeline(db, Config{ProjectsRoots: []string{gone}}, nil)
	if err := p.Run(context.Background()); !errors.Is(err, ErrNoProjectsRoots) {
		t.Fatalf("Run(all roots missing) = %v, want ErrNoProjectsRoots", err)
	}

	// One live + one missing → runs; only ctx cancellation stops it, and the
	// live root's transcript was ingested by the startup backfill.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	p2 := NewPipeline(db, Config{
		ProjectsRoots:  []string{gone, live},
		RescanInterval: 50 * time.Millisecond,
	}, nil)
	if err := p2.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run(one live root) = %v, want the ctx deadline", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM sessions WHERE session_uuid = ?`,
		"33333333-0000-4000-8000-00000000000c"); n != 1 {
		t.Errorf("sessions from the live root = %d, want 1", n)
	}
}

// TestHealStubSessionsSearchesEveryRoot: a stub session whose transcript lives
// under the SECOND config dir is still re-attributed (the heal glob used to
// know one root only).
func TestHealStubSessionsSearchesEveryRoot(t *testing.T) {
	db := testDB(t)
	rootA, rootB := t.TempDir(), t.TempDir()
	const uuid = "44444444-0000-4000-8000-00000000000d"

	// Stub: hook POST beat the tail — '(unknown)' project, empty cwd.
	if _, err := db.Exec(
		`INSERT INTO projects (path, slug, first_seen) VALUES (?, ?, ?)`,
		UnknownProjectPath, "unknown", "2026-07-20T09:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO sessions (project_id, session_uuid, status, cwd, started_at, source)
		 VALUES ((SELECT id FROM projects WHERE path = ?), ?, 'active', '', '', 'hook')`,
		UnknownProjectPath, uuid); err != nil {
		t.Fatal(err)
	}

	// The transcript only exists under the SECOND root.
	mustWrite(t, filepath.Join(rootB, "-home-dev-beta", uuid+".jsonl"),
		rootLine(uuid, "/home/dev/beta", "u-d-1", "2026-07-20T13:00:00Z", "beta prompt"))

	healed, err := HealStubSessions(db, []string{rootA, rootB}, nil)
	if err != nil {
		t.Fatalf("HealStubSessions: %v", err)
	}
	if len(healed) != 1 {
		t.Fatalf("healed = %v, want exactly the one stub", healed)
	}
	var project, cwd string
	if err := db.QueryRow(
		`SELECT p.path, s.cwd FROM sessions s JOIN projects p ON p.id = s.project_id
		 WHERE s.session_uuid = ?`, uuid).Scan(&project, &cwd); err != nil {
		t.Fatal(err)
	}
	if project != "/home/dev/beta" || cwd != "/home/dev/beta" {
		t.Errorf("healed to project %q / cwd %q, want /home/dev/beta for both", project, cwd)
	}
}
