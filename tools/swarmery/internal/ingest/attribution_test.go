package ingest

// Project-attribution regressions (live bug: every dispatcher worktree cwd
// and every in-repo subdirectory cwd minted its own phantom project, so the
// Projects page drifted away from the real workspace):
//   - a cwd under <worktreeRoot>/<parentSlug>/<task> attributes to the parent
//     project whose slug is the path segment, never minting a new row;
//   - a cwd inside a registered project's tree attributes to that project
//     (deepest registered ancestor wins);
//   - unknown standalone paths still mint their own project (no invention);
//   - HealProjectAttribution re-points pre-existing phantom rows and drops
//     the phantom project.

import (
	"testing"
)

func seedProject(t *testing.T, db dbtx, path string) int64 {
	t.Helper()
	id, _, err := UpsertProject(db, path, "2026-07-01T00:00:00.000Z", "")
	if err != nil {
		t.Fatalf("seed project %s: %v", path, err)
	}
	return id
}

func TestUpsertProjectWorktreeCwdAttributesToParent(t *testing.T) {
	db := testDB(t)
	old := worktreeRootOverride
	worktreeRootOverride = "/home/dev/.swarmery/worktrees"
	t.Cleanup(func() { worktreeRootOverride = old })

	parent := seedProject(t, db, "/Volumes/Work/Nytka")

	id, created, err := UpsertProject(db,
		"/home/dev/.swarmery/worktrees/-Volumes-Work-Nytka/T-5n66kj",
		"2026-07-26T00:00:00.000Z", "")
	if err != nil {
		t.Fatal(err)
	}
	if created || id != parent {
		t.Errorf("worktree cwd: id=%d created=%v, want parent id=%d created=false", id, created, parent)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM projects`); n != 1 {
		t.Errorf("projects = %d, want 1 (no phantom minted)", n)
	}
}

func TestUpsertProjectSubdirCwdAttributesToAncestor(t *testing.T) {
	db := testDB(t)
	parent := seedProject(t, db, "/Volumes/Work/swarmery")

	id, created, err := UpsertProject(db,
		"/Volumes/Work/swarmery/tools/swarmery/internal/api",
		"2026-07-26T00:00:00.000Z", "")
	if err != nil {
		t.Fatal(err)
	}
	if created || id != parent {
		t.Errorf("subdir cwd: id=%d created=%v, want ancestor id=%d created=false", id, created, parent)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM projects`); n != 1 {
		t.Errorf("projects = %d, want 1", n)
	}
}

func TestUpsertProjectDeepestAncestorWins(t *testing.T) {
	db := testDB(t)
	seedProject(t, db, "/Volumes/Work")
	inner := seedProject(t, db, "/Volumes/Work/swarmery")

	id, _, err := UpsertProject(db, "/Volumes/Work/swarmery/web", "2026-07-26T00:00:00.000Z", "")
	if err != nil {
		t.Fatal(err)
	}
	if id != inner {
		t.Errorf("id=%d, want deepest ancestor %d", id, inner)
	}
}

func TestUpsertProjectUnknownPathsStillMint(t *testing.T) {
	db := testDB(t)
	seedProject(t, db, "/Volumes/Work/swarmery")

	// Standalone path outside any registered tree → its own project.
	id, created, err := UpsertProject(db, "/Volumes/Work/Fusion", "2026-07-26T00:00:00.000Z", "")
	if err != nil {
		t.Fatal(err)
	}
	if !created || id == 0 {
		t.Errorf("standalone path: created=%v id=%d, want fresh row", created, id)
	}
	// An existing exact row keeps resolving to itself.
	again, created, err := UpsertProject(db, "/Volumes/Work/Fusion", "2026-07-27T00:00:00.000Z", "")
	if err != nil || created || again != id {
		t.Errorf("re-upsert: id=%d created=%v err=%v, want id=%d created=false", again, created, err, id)
	}
}

func TestUpsertProjectRootAttributesToSystem(t *testing.T) {
	db := testDB(t)
	old := systemBaseOverride
	systemBaseOverride = "/home/dev/.swarmery"
	t.Cleanup(func() { systemBaseOverride = old })

	// cwd "/" (daemon-spawned headless runs under launchd) mints the System
	// project on first sight…
	id, created, err := UpsertProject(db, "/", "2026-07-26T00:00:00.000Z", "")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("first root upsert should mint the System project")
	}
	var path, name string
	if err := db.QueryRow(`SELECT path, name FROM projects WHERE id = ?`, id).Scan(&path, &name); err != nil {
		t.Fatal(err)
	}
	if path != "/home/dev/.swarmery" || name != "System" {
		t.Errorf("system row = (%q, %q), want (/home/dev/.swarmery, System)", path, name)
	}
	// …and both "/" and the system dir itself resolve to it afterwards.
	if id2, created, err := UpsertProject(db, "/", "2026-07-27T00:00:00.000Z", ""); err != nil || created || id2 != id {
		t.Errorf("second root upsert: id=%d created=%v err=%v, want id=%d created=false", id2, created, err, id)
	}
	if id3, created, err := UpsertProject(db, "/home/dev/.swarmery", "2026-07-27T00:00:00.000Z", ""); err != nil || created || id3 != id {
		t.Errorf("system-dir upsert: id=%d created=%v err=%v, want id=%d created=false", id3, created, err, id)
	}
}

func TestHealProjectAttributionMergesRootIntoSystem(t *testing.T) {
	db := testDB(t)
	old := systemBaseOverride
	systemBaseOverride = "/home/dev/.swarmery"
	t.Cleanup(func() { systemBaseOverride = old })

	// Legacy "/" row with sessions, minted before the system rule existed.
	mustExecT(t, db, `INSERT INTO projects (path, slug, name, first_seen) VALUES ('/', '-', '/', '2026-07-01T00:00:00.000Z')`)
	var rootID int64
	if err := db.QueryRow(`SELECT id FROM projects WHERE path = '/'`).Scan(&rootID); err != nil {
		t.Fatal(err)
	}
	mustExecT(t, db, `INSERT INTO sessions (project_id, session_uuid, started_at) VALUES (?, 'u-root', '2026-07-20T00:00:00.000Z')`, rootID)

	moved, err := HealProjectAttribution(db)
	if err != nil {
		t.Fatal(err)
	}
	if moved != 1 {
		t.Errorf("moved = %d, want 1", moved)
	}
	var sysID int64
	if err := db.QueryRow(`SELECT id FROM projects WHERE path = '/home/dev/.swarmery'`).Scan(&sysID); err != nil {
		t.Fatalf("system project not minted by heal: %v", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM sessions WHERE project_id = ?`, sysID); n != 1 {
		t.Errorf("system sessions = %d, want 1 (re-pointed from /)", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM projects WHERE path = '/'`); n != 0 {
		t.Errorf("legacy / row still present")
	}
}

func TestHealProjectAttributionMergesPhantoms(t *testing.T) {
	db := testDB(t)
	old := worktreeRootOverride
	worktreeRootOverride = "/home/dev/.swarmery/worktrees"
	t.Cleanup(func() { worktreeRootOverride = old })

	parent := seedProject(t, db, "/Volumes/Work/swarmery")

	// Phantom rows minted by the pre-fix code path: insert directly.
	mustExecT(t, db, `INSERT INTO projects (path, slug, name, first_seen) VALUES (?, ?, ?, ?)`,
		"/Volumes/Work/swarmery/tools/swarmery/internal/api",
		"-Volumes-Work-swarmery-tools-swarmery-internal-api", "api", "2026-07-20T00:00:00.000Z")
	mustExecT(t, db, `INSERT INTO projects (path, slug, name, first_seen) VALUES (?, ?, ?, ?)`,
		"/home/dev/.swarmery/worktrees/-Volumes-Work-swarmery/T-abc123",
		"-home-dev-.swarmery-worktrees--Volumes-Work-swarmery-T-abc123", "T-abc123", "2026-07-24T00:00:00.000Z")
	var subdirID, wtID int64
	if err := db.QueryRow(`SELECT id FROM projects WHERE name = 'api'`).Scan(&subdirID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT id FROM projects WHERE name = 'T-abc123'`).Scan(&wtID); err != nil {
		t.Fatal(err)
	}
	mustExecT(t, db, `INSERT INTO sessions (project_id, session_uuid, started_at) VALUES (?, 'u-sub', '2026-07-20T00:00:00.000Z')`, subdirID)
	mustExecT(t, db, `INSERT INTO sessions (project_id, session_uuid, started_at) VALUES (?, 'u-wt', '2026-07-24T00:00:00.000Z')`, wtID)

	moved, err := HealProjectAttribution(db)
	if err != nil {
		t.Fatal(err)
	}
	if moved != 2 {
		t.Errorf("moved = %d, want 2", moved)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM projects`); n != 1 {
		t.Errorf("projects = %d, want 1 (phantoms dropped)", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM sessions WHERE project_id = ?`, parent); n != 2 {
		t.Errorf("parent sessions = %d, want 2 (re-pointed)", n)
	}
}

func mustExecT(t *testing.T, db dbtx, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec: %v\n%s", err, q)
	}
}
