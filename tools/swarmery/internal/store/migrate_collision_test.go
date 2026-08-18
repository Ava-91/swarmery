package store

import (
	"strings"
	"testing"
	"time"
)

// THE incident this guard exists for: two branches each numbered a migration
// 0056. Whichever binary ran first recorded version 56 under ITS name, and the
// other file — `0056_task_workspace_dir.sql` — was then skipped forever because
// the ledger was keyed on the NUMBER alone. The daemon came up "successfully"
// serving a schema with no `tasks.workspace_dir`, and every Plans page 500'd on
// "no such column" with nothing in the log pointing at migrations.
//
// Startup must refuse instead, naming both files.
func TestMigrateRefusesAVersionClaimedByAnotherFile(t *testing.T) {
	db := openRaw(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate fresh db: %v", err)
	}

	// Impersonate the incident: rewrite one applied row's name so the real file's
	// number is held by a stranger. Version 56 is a real migration on main, so
	// this reproduces the exact shape rather than inventing a synthetic one.
	const version = 56
	var realName string
	if err := db.QueryRow(
		`SELECT name FROM schema_migrations WHERE version = ?`, version).Scan(&realName); err != nil {
		t.Fatalf("read the applied row: %v", err)
	}
	if _, err := db.Exec(
		`UPDATE schema_migrations SET name = ? WHERE version = ?`,
		"0056_someone_elses_branch.sql", version); err != nil {
		t.Fatalf("rewrite the applied row: %v", err)
	}

	err := Migrate(db)
	if err == nil {
		t.Fatal("Migrate accepted a version claimed by a different file — the silent skip is back")
	}
	// The message must carry BOTH names: the whole failure mode was not knowing
	// which migration went missing.
	for _, want := range []string{realName, "0056_someone_elses_branch.sql", "renumber"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// The happy path stays untouched: a ledger whose names match the files is
// idempotent, and re-running Migrate is a no-op rather than an error.
func TestMigrateIsIdempotentWhenNamesMatch(t *testing.T) {
	db := openRaw(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	var before int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	var after int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("ledger rows = %d after a second migrate, want %d", after, before)
	}
}

// A partially-migrated database still catches up: the guard must reject only a
// NAME mismatch, never a version that simply has not been applied yet.
func TestMigrateCatchesUpFromAnOlderVersion(t *testing.T) {
	db := openRaw(t)
	migrateUpTo(t, db, 50)
	if _, err := db.Exec(
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (9999, '9999_not_a_file.sql', ?)`,
		time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	// A ledger row with no file on disk is NOT a collision — nothing claims 9999.
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate after partial state: %v", err)
	}
	mustHaveColumns(t, db, "epic_phases", "verify_verdict") // 0057 landed
}
