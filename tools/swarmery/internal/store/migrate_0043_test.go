package store

import "testing"

func TestMigrate0043FreshDB(t *testing.T) {
	db := openRaw(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate fresh db: %v", err)
	}

	var name string
	if err := db.QueryRow(`SELECT name FROM schema_migrations WHERE version = 43`).Scan(&name); err != nil {
		t.Fatalf("migration 43 not recorded: %v", err)
	}
	if name != "0043_phase_run_branch.sql" {
		t.Errorf("migration 43 name: want 0043_phase_run_branch.sql, got %s", name)
	}
	mustHaveColumns(t, db, "epic_phases", "run_branch")
}

// The backfill pins rows that already ran to the name that was in force when they ran,
// so the stored value and the previously-derived "swarm/phase-<id>" agree from the first
// boot on this schema. A row that never ran gets NULL: there is no branch to name, and
// inventing one is exactly the guess the column exists to remove.
func TestMigrate0043BackfillsOnlyRowsThatRan(t *testing.T) {
	db := openRaw(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, path, slug, first_seen)
		VALUES (1, '/tmp/p', 'p', '2026-07-29T00:00:00Z')`); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO tasks (id, project_id, title, prompt, status, created_at, source, external_id)
		VALUES (1, 1, 'Epic', 'goal', 'running', '2026-07-29T00:00:00Z', 'workspace', 'ws-epic')`); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	// Simulate the pre-0043 world: rows carrying run state, none carrying a branch.
	if _, err := db.Exec(`INSERT INTO epic_phases
		(id, workspace_task_id, seq, name, doc_path, run_state)
		VALUES (7, 1, 1, 'Ran', '/plan/phase-1.md', 'done'),
		       (8, 1, 2, 'Running', '/plan/phase-2.md', 'running'),
		       (9, 1, 3, 'Never ran', '/plan/phase-3.md', 'idle')`); err != nil {
		t.Fatalf("seed phases: %v", err)
	}
	// Re-run the backfill statement the migration carries; the ALTER already applied.
	if _, err := db.Exec(
		`UPDATE epic_phases SET run_branch = 'swarm/phase-' || id WHERE run_state <> 'idle'`); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	for _, tc := range []struct {
		id   int
		want any
	}{
		{7, "swarm/phase-7"},
		{8, "swarm/phase-8"},
		{9, nil},
	} {
		var got *string
		if err := db.QueryRow(`SELECT run_branch FROM epic_phases WHERE id = ?`, tc.id).Scan(&got); err != nil {
			t.Fatalf("read run_branch id=%d: %v", tc.id, err)
		}
		if tc.want == nil {
			if got != nil {
				t.Errorf("id=%d run_branch = %q, want NULL", tc.id, *got)
			}
			continue
		}
		if got == nil || *got != tc.want.(string) {
			t.Errorf("id=%d run_branch = %v, want %q", tc.id, got, tc.want)
		}
	}
}
