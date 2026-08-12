package store

import (
	"testing"
	"time"
)

// TestMigrate0054FreshDB verifies 0054 applies on a brand-new database: the
// account_runnable table exists with the documented columns and the migration
// is recorded under the slot it claimed.
func TestMigrate0054FreshDB(t *testing.T) {
	db := openRaw(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate fresh db: %v", err)
	}

	var name string
	if err := db.QueryRow(`SELECT name FROM schema_migrations WHERE version = 54`).Scan(&name); err != nil {
		t.Fatalf("migration 54 not recorded: %v", err)
	}
	if name != "0054_account_runnable.sql" {
		t.Errorf("migration 54 name: want 0054_account_runnable.sql, got %s", name)
	}
	mustHaveColumns(t, db, "account_runnable",
		"account", "status", "reason", "checked_at", "source")

	// The documented defaults: reason defaults to '', everything else is
	// required. A minimal insert must succeed and read back the default.
	if _, err := db.Exec(
		`INSERT INTO account_runnable (account, status, checked_at, source)
		 VALUES ('nabu-org', 'ready', 1765000000, 'probe')`); err != nil {
		t.Fatalf("minimal insert: %v", err)
	}
	var reason string
	if err := db.QueryRow(
		`SELECT reason FROM account_runnable WHERE account = 'nabu-org'`).Scan(&reason); err != nil {
		t.Fatalf("read default reason: %v", err)
	}
	if reason != "" {
		t.Errorf("reason default = %q, want ''", reason)
	}

	// account is the PRIMARY KEY: a second bare insert for the same account
	// must fail — one row per account, the upsert is the writer's job.
	if _, err := db.Exec(
		`INSERT INTO account_runnable (account, status, checked_at, source)
		 VALUES ('nabu-org', 'no-login', 1765000001, 'probe')`); err == nil {
		t.Error("duplicate account accepted; want PRIMARY KEY violation")
	}
}

// TestMigrate0054OnPopulatedDB: an older database (here: the last slot before
// this one that exists on this branch) migrates forward, and the new table is
// usable through the store functions afterwards.
func TestMigrate0054OnPopulatedDB(t *testing.T) {
	db := openRaw(t)
	migrateUpTo(t, db, 53)

	if cols := columnSet(t, db, "account_runnable"); len(cols) != 0 {
		t.Fatal("account_runnable exists before 0054 — migrateUpTo applied too much")
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate populated db: %v", err)
	}
	mustHaveColumns(t, db, "account_runnable",
		"account", "status", "reason", "checked_at", "source")

	// The store functions round-trip on the migrated schema.
	checked := time.Date(2026, 8, 12, 10, 30, 0, 0, time.UTC)
	if err := PutAccountRunnable(db, "default", "no-login",
		"Claude login required for this account", "probe", checked); err != nil {
		t.Fatalf("PutAccountRunnable: %v", err)
	}
	row, ok, err := GetAccountRunnable(db, "default")
	if err != nil || !ok {
		t.Fatalf("GetAccountRunnable = (%v, %v), want a row", ok, err)
	}
	if row.Status != "no-login" || row.Source != "probe" || !row.CheckedAt.Equal(checked) {
		t.Errorf("row = %+v, want status=no-login source=probe checkedAt=%v", row, checked)
	}

	// The upsert replaces, never duplicates.
	later := checked.Add(time.Hour)
	if err := PutAccountRunnable(db, "default", "ready", "", "probe", later); err != nil {
		t.Fatalf("PutAccountRunnable (upsert): %v", err)
	}
	all, err := AllAccountRunnable(db)
	if err != nil {
		t.Fatalf("AllAccountRunnable: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d rows, want 1 (upsert must replace)", len(all))
	}
	if got := all["default"]; got.Status != "ready" || got.Reason != "" || !got.CheckedAt.Equal(later) {
		t.Errorf("upserted row = %+v, want ready/'' at %v", got, later)
	}

	// Absence of a row is (zero, false, nil) — never probed, not an error.
	if _, ok, err := GetAccountRunnable(db, "never-probed"); ok || err != nil {
		t.Errorf("GetAccountRunnable(never-probed) = (%v, %v), want (false, nil)", ok, err)
	}

	// Idempotency: a second Migrate run is a no-op.
	if err := Migrate(db); err != nil {
		t.Fatalf("re-run migrate: %v", err)
	}
}
