package findings

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
)

// setup opens a migrated temp DB — config_lint_findings is the only table
// these tests touch, so no fixture rows are needed.
func setup(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func count(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", q, err)
	}
	return n
}

// activeCount counts unresolved rows for (target, rule).
func activeCount(t *testing.T, db *sql.DB, target, rule string) int {
	t.Helper()
	return count(t, db,
		`SELECT COUNT(*) FROM config_lint_findings WHERE target = ? AND rule = ? AND resolved_at IS NULL`,
		target, rule)
}

func mustUpsert(t *testing.T, db *sql.DB, target, rule, severity, message string) {
	t.Helper()
	if err := Upsert(db, target, rule, severity, message); err != nil {
		t.Fatalf("upsert %s/%s: %v", target, rule, err)
	}
}

func mustSync(t *testing.T, db *sql.DB, rule string, items []Item) (int, int) {
	t.Helper()
	detected, resolved, err := Sync(db, rule, items)
	if err != nil {
		t.Fatalf("sync %s: %v", rule, err)
	}
	return detected, resolved
}

// TestUpsertInsertsThenUpdatesInPlace covers (a) the first Upsert INSERTs and
// (b) a second Upsert for the same (target, rule) refreshes the same row
// instead of piling up a second active one.
func TestUpsertInsertsThenUpdatesInPlace(t *testing.T) {
	db := setup(t)

	mustUpsert(t, db, "agent:1", "rule_a", "warn", "first")
	if n := activeCount(t, db, "agent:1", "rule_a"); n != 1 {
		t.Fatalf("after first upsert: active=%d, want 1", n)
	}

	mustUpsert(t, db, "agent:1", "rule_a", "info", "second")
	if n := count(t, db, `SELECT COUNT(*) FROM config_lint_findings WHERE target = 'agent:1' AND rule = 'rule_a'`); n != 1 {
		t.Fatalf("after second upsert: rows=%d, want 1 (updated in place)", n)
	}

	var severity, message string
	if err := db.QueryRow(
		`SELECT severity, message FROM config_lint_findings WHERE target = 'agent:1' AND rule = 'rule_a'`).
		Scan(&severity, &message); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if severity != "info" || message != "second" {
		t.Fatalf("row not refreshed: severity=%q message=%q", severity, message)
	}
}

// TestResolveClosesActiveRow covers the single-target Resolve path used by the
// scanner's parse_error rule.
func TestResolveClosesActiveRow(t *testing.T) {
	db := setup(t)
	mustUpsert(t, db, "agent:1", "rule_a", "warn", "boom")

	if err := Resolve(db, "agent:1", "rule_a"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if n := activeCount(t, db, "agent:1", "rule_a"); n != 0 {
		t.Fatalf("after resolve: active=%d, want 0", n)
	}

	// Resolving again is a no-op, not an error.
	if err := Resolve(db, "agent:1", "rule_a"); err != nil {
		t.Fatalf("second resolve: %v", err)
	}
}

// TestSyncResolvesOmittedTarget: a target that stops firing gets resolved_at
// while the still-firing targets of the same rule stay active.
func TestSyncResolvesOmittedTarget(t *testing.T) {
	db := setup(t)

	detected, resolved := mustSync(t, db, "rule_a", []Item{
		{Target: "agent:1", Severity: "warn", Message: "one"},
		{Target: "agent:2", Severity: "warn", Message: "two"},
	})
	if detected != 2 || resolved != 0 {
		t.Fatalf("first sync: detected=%d resolved=%d, want 2/0", detected, resolved)
	}

	detected, resolved = mustSync(t, db, "rule_a", []Item{
		{Target: "agent:1", Severity: "warn", Message: "one again"},
	})
	if detected != 1 || resolved != 1 {
		t.Fatalf("second sync: detected=%d resolved=%d, want 1/1", detected, resolved)
	}
	if n := activeCount(t, db, "agent:1", "rule_a"); n != 1 {
		t.Fatalf("agent:1 active=%d, want 1", n)
	}
	if n := activeCount(t, db, "agent:2", "rule_a"); n != 0 {
		t.Fatalf("agent:2 active=%d, want 0 (resolved)", n)
	}
}

// TestSyncEmptyResolvesRuleOnly: an empty item set resolves every active row
// of that rule and leaves other rules' rows untouched — the property that
// lets two writers share the table.
func TestSyncEmptyResolvesRuleOnly(t *testing.T) {
	db := setup(t)

	mustSync(t, db, "rule_a", []Item{
		{Target: "agent:1", Severity: "warn", Message: "one"},
		{Target: "agent:2", Severity: "warn", Message: "two"},
	})
	mustSync(t, db, "rule_b", []Item{
		{Target: "agent:1", Severity: "info", Message: "other rule"},
	})

	detected, resolved := mustSync(t, db, "rule_a", nil)
	if detected != 0 || resolved != 2 {
		t.Fatalf("empty sync: detected=%d resolved=%d, want 0/2", detected, resolved)
	}
	if n := count(t, db,
		`SELECT COUNT(*) FROM config_lint_findings WHERE rule = 'rule_a' AND resolved_at IS NULL`); n != 0 {
		t.Fatalf("rule_a active=%d, want 0", n)
	}
	if n := activeCount(t, db, "agent:1", "rule_b"); n != 1 {
		t.Fatalf("rule_b active=%d, want 1 (untouched)", n)
	}
}

// TestSyncReDetectInsertsNewRow: after a resolve, re-detecting the same target
// INSERTs a NEW row so history is preserved.
func TestSyncReDetectInsertsNewRow(t *testing.T) {
	db := setup(t)

	mustSync(t, db, "rule_a", []Item{{Target: "agent:1", Severity: "warn", Message: "one"}})
	mustSync(t, db, "rule_a", nil)
	mustSync(t, db, "rule_a", []Item{{Target: "agent:1", Severity: "warn", Message: "back"}})

	total := count(t, db,
		`SELECT COUNT(*) FROM config_lint_findings WHERE target = 'agent:1' AND rule = 'rule_a'`)
	if total != 2 {
		t.Fatalf("total rows=%d, want 2 (history preserved)", total)
	}
	if n := count(t, db,
		`SELECT COUNT(*) FROM config_lint_findings WHERE target = 'agent:1' AND rule = 'rule_a' AND resolved_at IS NOT NULL`); n != 1 {
		t.Fatalf("resolved rows=%d, want 1", n)
	}
	if n := activeCount(t, db, "agent:1", "rule_a"); n != 1 {
		t.Fatalf("active rows=%d, want 1", n)
	}
}
