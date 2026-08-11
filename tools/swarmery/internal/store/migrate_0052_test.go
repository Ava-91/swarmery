package store

import "testing"

// TestMigrate0052FreshDB verifies 0052 applies on a brand-new database: the
// spec_criteria table exists with its natural key and index, and epic_phases
// carries the covers column.
func TestMigrate0052FreshDB(t *testing.T) {
	db := openRaw(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate fresh db: %v", err)
	}

	var name string
	if err := db.QueryRow(`SELECT name FROM schema_migrations WHERE version = 52`).Scan(&name); err != nil {
		t.Fatalf("migration 52 not recorded: %v", err)
	}
	if name != "0052_plan_spec.sql" {
		t.Errorf("migration 52 name: want 0052_plan_spec.sql, got %s", name)
	}
	mustHaveColumns(t, db, "spec_criteria",
		"id", "workspace_task_id", "pos", "cid", "text", "done", "line")
	mustHaveIndex(t, db, "idx_spec_criteria_task")
	mustHaveColumns(t, db, "epic_phases", "covers")

	// UNIQUE(workspace_task_id, cid): a second identical key must fail —
	// wsingest's applySpec upsert rides on this constraint.
	const ins = `INSERT INTO spec_criteria (workspace_task_id, pos, cid, text)
		VALUES (1, 0, 'SC-1', 'first')`
	if _, err := db.Exec(ins); err != nil {
		t.Fatalf("insert criterion: %v", err)
	}
	if _, err := db.Exec(ins); err == nil {
		t.Error("duplicate (workspace_task_id, cid) accepted; want UNIQUE violation")
	}

	// Column defaults: done/line/pos land as 0.
	var done, line int
	if err := db.QueryRow(`SELECT done, line FROM spec_criteria WHERE cid = 'SC-1'`).
		Scan(&done, &line); err != nil {
		t.Fatalf("read inserted criterion: %v", err)
	}
	if done != 0 || line != 0 {
		t.Errorf("defaults = (done %d, line %d), want (0, 0)", done, line)
	}
}

// TestMigrate0052OnPopulatedDB: a database created before 0052 gains the table
// and the covers column, and every pre-existing epic_phases row backfills to
// '[]' — a phase indexed before Covers shipped declares nothing, not NULL.
func TestMigrate0052OnPopulatedDB(t *testing.T) {
	db := openRaw(t)
	migrateUpTo(t, db, 51)

	if cols := columnSet(t, db, "epic_phases"); cols["covers"] {
		t.Fatal("epic_phases.covers exists before 0052 — migrateUpTo applied too much")
	}
	if cols := columnSet(t, db, "spec_criteria"); len(cols) != 0 {
		t.Fatal("spec_criteria exists before 0052 — migrateUpTo applied too much")
	}
	if _, err := db.Exec(
		`INSERT INTO epic_phases (workspace_task_id, seq, name, doc_path)
		 VALUES (1, 1, 'pre-0052 phase', '/tmp/plan/phase-1-old.md')`); err != nil {
		t.Fatalf("insert phase: %v", err)
	}

	if err := Migrate(db); err != nil {
		t.Fatalf("migrate populated db: %v", err)
	}
	mustHaveColumns(t, db, "epic_phases", "covers")

	var covers string
	if err := db.QueryRow(`SELECT covers FROM epic_phases WHERE seq = 1`).Scan(&covers); err != nil {
		t.Fatalf("read migrated phase: %v", err)
	}
	if covers != "[]" {
		t.Errorf("covers = %q, want '[]' (pre-0052 rows declare nothing)", covers)
	}

	// Idempotency: a second Migrate run is a no-op.
	if err := Migrate(db); err != nil {
		t.Fatalf("re-run migrate: %v", err)
	}
}
