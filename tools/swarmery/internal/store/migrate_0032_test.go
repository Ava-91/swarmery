package store

import "testing"

// TestMigrate0032FreshDB verifies 0032 creates trajectory_scores and
// trajectory_findings with the expected columns, the UNIQUE(session_id, agent)
// constraint, the ON DELETE CASCADE FK behavior, and the
// idx_trajectory_scores_agent index — on a brand-new database.
func TestMigrate0032FreshDB(t *testing.T) {
	db := openRaw(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate fresh db: %v", err)
	}

	var name string
	if err := db.QueryRow(`SELECT name FROM schema_migrations WHERE version = 32`).Scan(&name); err != nil {
		t.Fatalf("migration 32 not recorded: %v", err)
	}
	if name != "0032_trajectory_scores.sql" {
		t.Errorf("migration 32 name: want 0032_trajectory_scores.sql, got %s", name)
	}

	mustHaveColumns(t, db, "trajectory_scores",
		"id", "session_id", "agent", "first_pass", "computed_at")
	mustHaveColumns(t, db, "trajectory_findings",
		"id", "score_id", "kind", "severity", "evidence_turn_ids")
	mustHaveIndex(t, db, "idx_trajectory_scores_agent")

	// Seed a project and session required by the FK chain.
	if _, err := db.Exec(`INSERT INTO projects (id, path, slug, first_seen)
		VALUES (1, '/tmp/p', 'p', '2026-07-25T00:00:00Z')`); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (id, project_id, session_uuid, started_at)
		VALUES (1, 1, 'u1', '2026-07-25T00:00:00Z')`); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	// Insert a score row and verify it lands with expected values.
	if _, err := db.Exec(`INSERT INTO trajectory_scores (id, session_id, agent, first_pass, computed_at)
		VALUES (1, 1, 'tech-lead', 1, '2026-07-25T00:00:00Z')`); err != nil {
		t.Fatalf("insert trajectory_score: %v", err)
	}
	var agent string
	var fp int
	if err := db.QueryRow(`SELECT agent, first_pass FROM trajectory_scores WHERE id = 1`).
		Scan(&agent, &fp); err != nil {
		t.Fatalf("read trajectory_score: %v", err)
	}
	if agent != "tech-lead" || fp != 1 {
		t.Errorf("score = (%q, %d), want (tech-lead, 1)", agent, fp)
	}

	// UNIQUE(session_id, agent): a duplicate must be rejected.
	if _, err := db.Exec(`INSERT INTO trajectory_scores (session_id, agent, first_pass, computed_at)
		VALUES (1, 'tech-lead', 0, '2026-07-25T00:00:01Z')`); err == nil {
		t.Error("expected UNIQUE violation for duplicate (session_id, agent), got nil")
	}

	// Insert a finding linked to the score.
	if _, err := db.Exec(`INSERT INTO trajectory_findings (score_id, kind, severity, evidence_turn_ids)
		VALUES (1, 'search-loop', 'warn', '[1,2,3,4]')`); err != nil {
		t.Fatalf("insert trajectory_finding: %v", err)
	}
	var findingCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM trajectory_findings WHERE score_id = 1`).
		Scan(&findingCount); err != nil {
		t.Fatalf("count findings: %v", err)
	}
	if findingCount != 1 {
		t.Errorf("findings count = %d, want 1", findingCount)
	}

	// ON DELETE CASCADE: deleting the score must cascade to its findings.
	if _, err := db.Exec(`DELETE FROM trajectory_scores WHERE id = 1`); err != nil {
		t.Fatalf("delete trajectory_score: %v", err)
	}
	var orphans int
	if err := db.QueryRow(`SELECT COUNT(*) FROM trajectory_findings`).Scan(&orphans); err != nil {
		t.Fatalf("count findings after cascade delete: %v", err)
	}
	if orphans != 0 {
		t.Errorf("findings after CASCADE delete = %d, want 0 (ON DELETE CASCADE)", orphans)
	}

	// Idempotency: a second Migrate run is a no-op.
	if err := Migrate(db); err != nil {
		t.Fatalf("re-run migrate: %v", err)
	}
}
