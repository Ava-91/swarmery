package store

import "testing"

func TestMigrate0033FreshDB(t *testing.T) {
	db := openRaw(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate fresh db: %v", err)
	}

	var name string
	if err := db.QueryRow(`SELECT name FROM schema_migrations WHERE version = 33`).Scan(&name); err != nil {
		t.Fatalf("migration 33 not recorded: %v", err)
	}
	if name != "0033_trajectory_judgments.sql" {
		t.Errorf("migration 33 name: want 0033_trajectory_judgments.sql, got %s", name)
	}

	mustHaveColumns(t, db, "trajectory_judgments",
		"id", "session_id", "agent", "model", "judged_at",
		"end_result", "instruction_compliance", "pitfalls", "tool_calls",
		"overall", "review")
	mustHaveIndex(t, db, "idx_trajectory_judgments_agent")

	// Seed the FK chain: project → session (no seedProjectSession helper in this package).
	if _, err := db.Exec(`INSERT INTO projects (id, path, slug, first_seen)
		VALUES (1, '/tmp/p', 'p', '2026-07-25T00:00:00Z')`); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (id, project_id, session_uuid, started_at)
		VALUES (1, 1, 'u1', '2026-07-25T00:00:00Z')`); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	// UNIQUE(session_id, agent, model) is enforced.
	ins := `INSERT INTO trajectory_judgments
		(session_id, agent, model, judged_at, end_result, instruction_compliance, pitfalls, tool_calls, overall, review)
		VALUES (1,'tech-lead','sonnet','2026-07-25T00:00:00Z',4,4,5,4,4.25,'ok')`
	if _, err := db.Exec(ins); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if _, err := db.Exec(ins); err == nil {
		t.Fatal("duplicate (session,agent,model) should violate UNIQUE, but insert succeeded")
	}

	// Idempotency: a second Migrate run is a no-op.
	if err := Migrate(db); err != nil {
		t.Fatalf("re-run migrate: %v", err)
	}
}
