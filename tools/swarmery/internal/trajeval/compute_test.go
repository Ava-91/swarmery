package trajeval

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
)

// openMigratedDB opens a fresh migrated SQLite DB in a temp dir, following
// the exact open pattern from internal/evals/evals_test.go.
func openMigratedDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "trajeval.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// mustExec is a thin db.Exec wrapper that t.Fatal on error.
func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec: %v\n%s", err, q)
	}
}

func TestComputePersistsScoreAndFindings(t *testing.T) {
	db := openMigratedDB(t)
	// One session, its turns tagged agent_name='tech-lead', a search-loop +
	// verify-skip event stream and no error.
	mustExec(t, db, `INSERT INTO projects(id, name, path, slug, first_seen) VALUES (1,'p','/p','p','2026-07-25T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO sessions(id, project_id, session_uuid, started_at)
	                 VALUES (1, 1, 'u1', '2026-07-25T00:00:00Z')`)
	// Turns carry the agent grain (turns.agent_name, migration 0010).
	for i := 1; i <= 6; i++ {
		mustExec(t, db, `INSERT INTO turns(id, session_id, seq, role, started_at, agent_name)
		                 VALUES (?, 1, ?, 'assistant', '2026-07-25T00:00:00Z', 'tech-lead')`, i, i)
	}
	seed := []struct {
		turn      int64
		typ, tool string
	}{
		{1, "tool_call", "Grep"}, {2, "tool_call", "Grep"},
		{3, "tool_call", "Grep"}, {4, "tool_call", "Grep"},
		{5, "file_change", ""}, {6, "session_end", ""},
	}
	for i, e := range seed {
		mustExec(t, db, `INSERT INTO events(id, session_id, turn_id, ts, type, tool_name)
		                 VALUES (?, 1, ?, '2026-07-25T00:00:00Z', ?, ?)`, i+1, e.turn, e.typ, e.tool)
	}

	if err := Compute(db, time.Now()); err != nil {
		t.Fatalf("Compute: %v", err)
	}

	var agent string
	var fp int
	if err := db.QueryRow(`SELECT agent, first_pass FROM trajectory_scores WHERE session_id=1`).Scan(&agent, &fp); err != nil {
		t.Fatalf("score row: %v", err)
	}
	if agent != "tech-lead" || fp != 1 {
		t.Errorf("score = (%s, %d), want (tech-lead, 1)", agent, fp)
	}
	var kinds int
	if err := db.QueryRow(`SELECT COUNT(*) FROM trajectory_findings`).Scan(&kinds); err != nil {
		t.Fatalf("count findings: %v", err)
	}
	if kinds != 2 { // search-loop + verify-skip
		t.Errorf("findings = %d, want 2", kinds)
	}

	// Idempotent: second run does not duplicate.
	if err := Compute(db, time.Now()); err != nil {
		t.Fatal(err)
	}
	var scores int
	if err := db.QueryRow(`SELECT COUNT(*) FROM trajectory_scores`).Scan(&scores); err != nil {
		t.Fatalf("count scores after recompute: %v", err)
	}
	if scores != 1 {
		t.Errorf("scores after recompute = %d, want 1 (idempotent)", scores)
	}

	var findingsAfter int
	if err := db.QueryRow(`SELECT COUNT(*) FROM trajectory_findings`).Scan(&findingsAfter); err != nil {
		t.Fatalf("count findings after recompute: %v", err)
	}
	if findingsAfter != 2 {
		t.Errorf("findings after recompute = %d, want 2", findingsAfter)
	}
}
