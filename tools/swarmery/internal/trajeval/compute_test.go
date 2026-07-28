package trajeval

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/ingest"
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

// TestComputeSkipsAndPrunesSystemSessions locks the feedback-loop guard:
// sessions attributed to the System project (cwd = ingest.SystemDir(), plus
// legacy cwd "/") are daemon-spawned headless runs — trajjudge's own judge
// sessions among them. Scoring them turns every judge run into a fresh judge
// candidate and the pool never drains. Compute must never score them and must
// prune any score rows accumulated before this guard existed.
func TestComputeSkipsAndPrunesSystemSessions(t *testing.T) {
	db := openMigratedDB(t)
	sysDir := ingest.SystemDir()
	if sysDir == "" {
		t.Skip("home dir unresolvable")
	}

	mustExec(t, db, `INSERT INTO projects(id, name, path, slug, first_seen) VALUES (1,'System',?, 'system','2026-07-27T00:00:00Z')`, sysDir)
	mustExec(t, db, `INSERT INTO projects(id, name, path, slug, first_seen) VALUES (2,'p','/p','p','2026-07-27T00:00:00Z')`)
	// Session 1: System dir cwd. Session 2: legacy "/" cwd. Session 3: normal.
	mustExec(t, db, `INSERT INTO sessions(id, project_id, session_uuid, cwd, started_at) VALUES (1, 1, 'sys1', ?, '2026-07-27T00:00:00Z')`, sysDir)
	mustExec(t, db, `INSERT INTO sessions(id, project_id, session_uuid, cwd, started_at) VALUES (2, 1, 'sys2', '/', '2026-07-27T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO sessions(id, project_id, session_uuid, cwd, started_at) VALUES (3, 2, 'u1', '/p', '2026-07-27T00:00:00Z')`)
	for sid := 1; sid <= 3; sid++ {
		mustExec(t, db, `INSERT INTO turns(id, session_id, seq, role, started_at, agent_name)
		                 VALUES (?, ?, 1, 'assistant', '2026-07-27T00:00:00Z', 'main')`, sid, sid)
		mustExec(t, db, `INSERT INTO events(id, session_id, turn_id, ts, type, tool_name)
		                 VALUES (?, ?, ?, '2026-07-27T00:00:00Z', 'file_change', '')`, sid, sid, sid)
	}
	// Pre-guard pollution: session 1 was already scored and flagged.
	mustExec(t, db, `INSERT INTO trajectory_scores(id, session_id, agent, first_pass, computed_at) VALUES (99, 1, 'main', 1, '2026-07-27T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO trajectory_findings(score_id, kind, severity, evidence_turn_ids) VALUES (99, 'verify-skip', 'warn', '[1]')`)

	if err := Compute(db, time.Now()); err != nil {
		t.Fatalf("Compute: %v", err)
	}

	var sysScores int
	if err := db.QueryRow(`SELECT COUNT(*) FROM trajectory_scores WHERE session_id IN (1,2)`).Scan(&sysScores); err != nil {
		t.Fatal(err)
	}
	if sysScores != 0 {
		t.Errorf("system-session scores = %d, want 0 (skipped and pruned)", sysScores)
	}
	var orphanFindings int
	if err := db.QueryRow(`SELECT COUNT(*) FROM trajectory_findings WHERE score_id = 99`).Scan(&orphanFindings); err != nil {
		t.Fatal(err)
	}
	if orphanFindings != 0 {
		t.Errorf("pruned score's findings = %d, want 0 (cascade)", orphanFindings)
	}
	var normal int
	if err := db.QueryRow(`SELECT COUNT(*) FROM trajectory_scores WHERE session_id = 3`).Scan(&normal); err != nil {
		t.Fatal(err)
	}
	if normal != 1 {
		t.Errorf("normal-session scores = %d, want 1", normal)
	}
}

// TestComputeNormalizesAgentName proves that a raw turns.agent_name like
// "core:tech-lead" is stored as "tech-lead" (prefix-stripped + lowercased),
// matching the key the API and Retro layer use.
func TestComputeNormalizesAgentName(t *testing.T) {
	db := openMigratedDB(t)

	mustExec(t, db, `INSERT INTO projects(id, name, path, slug, first_seen) VALUES (1,'p','/p','p','2026-07-25T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO sessions(id, project_id, session_uuid, started_at)
	                 VALUES (1, 1, 'u2', '2026-07-25T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO turns(id, session_id, seq, role, started_at, agent_name)
	                 VALUES (1, 1, 1, 'assistant', '2026-07-25T00:00:00Z', 'core:tech-lead')`)
	mustExec(t, db, `INSERT INTO events(id, session_id, turn_id, ts, type, tool_name)
	                 VALUES (1, 1, 1, '2026-07-25T00:00:00Z', 'file_change', '')`)

	if err := Compute(db, time.Now()); err != nil {
		t.Fatalf("Compute: %v", err)
	}

	var agent string
	if err := db.QueryRow(`SELECT agent FROM trajectory_scores WHERE session_id=1`).Scan(&agent); err != nil {
		t.Fatalf("score row: %v", err)
	}
	if agent != "tech-lead" {
		t.Errorf("agent = %q, want \"tech-lead\" (prefix stripped + lowercased)", agent)
	}
}

// TestComputeNormalizesCapitalizedAgentName proves that a capitalized
// turns.agent_name without a namespace prefix (e.g. "Explore", the Claude
// built-in) is also stored lowercased. This locks the invariant that the
// Retro trajectory-chip lookup can safely lower-fold row.agent on the client
// side and always match the stored key.
func TestComputeNormalizesCapitalizedAgentName(t *testing.T) {
	db := openMigratedDB(t)

	mustExec(t, db, `INSERT INTO projects(id, name, path, slug, first_seen) VALUES (1,'p','/p','p','2026-07-25T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO sessions(id, project_id, session_uuid, started_at)
	                 VALUES (1, 1, 'u3', '2026-07-25T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO turns(id, session_id, seq, role, started_at, agent_name)
	                 VALUES (1, 1, 1, 'assistant', '2026-07-25T00:00:00Z', 'Explore')`)
	mustExec(t, db, `INSERT INTO events(id, session_id, turn_id, ts, type, tool_name)
	                 VALUES (1, 1, 1, '2026-07-25T00:00:00Z', 'file_change', '')`)

	if err := Compute(db, time.Now()); err != nil {
		t.Fatalf("Compute: %v", err)
	}

	var agent string
	if err := db.QueryRow(`SELECT agent FROM trajectory_scores WHERE session_id=1`).Scan(&agent); err != nil {
		t.Fatalf("score row: %v", err)
	}
	if agent != "explore" {
		t.Errorf("agent = %q, want \"explore\" (capitalized name lowercased)", agent)
	}
}
