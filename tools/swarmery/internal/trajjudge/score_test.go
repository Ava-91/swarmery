package trajjudge

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
)

// openMigratedDB opens a fresh migrated SQLite DB in a temp dir, following
// the exact open pattern from internal/trajeval/compute_test.go.
func openMigratedDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "trajjudge.db"))
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

type mockRunner struct {
	out string
	err error
}

func (m mockRunner) Run(_ context.Context, _ string) (string, error) { return m.out, m.err }

func TestScorePersistsFlaggedCandidate(t *testing.T) {
	db := openMigratedDB(t)
	// One session, agent 'tech-lead', with a deterministic finding (=> flagged).
	mustExec(t, db, `INSERT INTO projects(id,name,path,slug,first_seen) VALUES (1,'p','/p','p','2026-07-25T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO sessions(id,project_id,session_uuid,started_at) VALUES (1,1,'u1','2026-07-25T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO turns(id,session_id,seq,role,started_at,agent_name) VALUES (1,1,1,'assistant','2026-07-25T00:00:00Z','tech-lead')`)
	mustExec(t, db, `INSERT INTO events(id,session_id,turn_id,ts,type,tool_name) VALUES (1,1,1,'2026-07-25T00:00:00Z','file_change',NULL)`)
	mustExec(t, db, `INSERT INTO trajectory_scores(id,session_id,agent,first_pass,computed_at) VALUES (1,1,'tech-lead',1,'2026-07-25T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO trajectory_findings(score_id,kind,severity,evidence_turn_ids) VALUES (1,'verify-skip','warn','[1]')`)

	runner := mockRunner{out: `{"end_result":4,"instruction_compliance":5,"pitfalls":2,"tool_calls":4,"review":"skipped tests [t1]"}`}
	if err := Score(db, runner, "sonnet", time.Now(), 10); err != nil {
		t.Fatalf("Score: %v", err)
	}

	var agent, model string
	var overall float64
	if err := db.QueryRow(`SELECT agent, model, overall FROM trajectory_judgments WHERE session_id=1`).
		Scan(&agent, &model, &overall); err != nil {
		t.Fatalf("judgment row: %v", err)
	}
	if agent != "tech-lead" || model != "sonnet" || overall < 3.7 || overall > 3.8 { // (4+5+2+4)/4=3.75
		t.Errorf("got (%s,%s,%v)", agent, model, overall)
	}

	// Idempotent: same (session,agent,model) not re-judged / not duplicated.
	if err := Score(db, runner, "sonnet", time.Now(), 10); err != nil {
		t.Fatal(err)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM trajectory_judgments`).Scan(&n)
	if n != 1 {
		t.Errorf("judgments = %d, want 1 (idempotent)", n)
	}
}

func TestScoreSkipsOnRunnerError(t *testing.T) {
	db := openMigratedDB(t)
	mustExec(t, db, `INSERT INTO projects(id,name,path,slug,first_seen) VALUES (1,'p','/p','p','2026-07-25T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO sessions(id,project_id,session_uuid,started_at) VALUES (1,1,'u1','2026-07-25T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO turns(id,session_id,seq,role,started_at,agent_name) VALUES (1,1,1,'assistant','2026-07-25T00:00:00Z','tech-lead')`)
	mustExec(t, db, `INSERT INTO events(id,session_id,turn_id,ts,type,tool_name) VALUES (1,1,1,'2026-07-25T00:00:00Z','file_change',NULL)`)
	mustExec(t, db, `INSERT INTO trajectory_scores(id,session_id,agent,first_pass,computed_at) VALUES (1,1,'tech-lead',1,'2026-07-25T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO trajectory_findings(score_id,kind,severity,evidence_turn_ids) VALUES (1,'verify-skip','warn','[1]')`)

	if err := Score(db, mockRunner{err: context.DeadlineExceeded}, "sonnet", time.Now(), 10); err != nil {
		t.Fatalf("Score should swallow candidate errors: %v", err)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM trajectory_judgments`).Scan(&n)
	if n != 0 {
		t.Errorf("judgments = %d, want 0 (runner failed => skip)", n)
	}
}

func TestScoreSkipsWhenBatchInFlight(t *testing.T) {
	db := openMigratedDB(t)
	mustExec(t, db, `INSERT INTO projects(id,name,path,slug,first_seen) VALUES (1,'p','/p','p','2026-07-25T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO sessions(id,project_id,session_uuid,started_at) VALUES (1,1,'u1','2026-07-25T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO turns(id,session_id,seq,role,started_at,agent_name) VALUES (1,1,1,'assistant','2026-07-25T00:00:00Z','tech-lead')`)
	mustExec(t, db, `INSERT INTO events(id,session_id,turn_id,ts,type,tool_name) VALUES (1,1,1,'2026-07-25T00:00:00Z','file_change',NULL)`)
	mustExec(t, db, `INSERT INTO trajectory_scores(id,session_id,agent,first_pass,computed_at) VALUES (1,1,'tech-lead',1,'2026-07-25T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO trajectory_findings(score_id,kind,severity,evidence_turn_ids) VALUES (1,'verify-skip','warn','[1]')`)

	if !inFlight.CompareAndSwap(false, true) {
		t.Fatal("inFlight unexpectedly set")
	}
	defer inFlight.Store(false)

	runner := mockRunner{out: `{"end_result":4,"instruction_compliance":5,"pitfalls":2,"tool_calls":4,"review":"skipped tests [t1]"}`}
	if err := Score(db, runner, "sonnet", time.Now(), 10); err != nil {
		t.Fatalf("Score during in-flight batch should be a quiet no-op: %v", err)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM trajectory_judgments`).Scan(&n)
	if n != 0 {
		t.Errorf("judgments = %d, want 0 (overlapping batch must be skipped)", n)
	}
}

// seedScoredSession inserts a project-1 session with a trajectory score and
// one event, at the given started_at. flagged adds a deterministic finding.
func seedScoredSession(t *testing.T, db *sql.DB, id int64, startedAt string, flagged bool) {
	t.Helper()
	mustExec(t, db, `INSERT INTO sessions(id,project_id,session_uuid,started_at) VALUES (?,1,?,?)`,
		id, "u"+startedAt+string(rune('0'+id%10)), startedAt)
	mustExec(t, db, `INSERT INTO turns(id,session_id,seq,role,started_at,agent_name) VALUES (?,?,1,'assistant',?,'tech-lead')`,
		id, id, startedAt)
	mustExec(t, db, `INSERT INTO events(id,session_id,turn_id,ts,type,tool_name) VALUES (?,?,?,?,'file_change',NULL)`,
		id, id, id, startedAt)
	mustExec(t, db, `INSERT INTO trajectory_scores(id,session_id,agent,first_pass,computed_at) VALUES (?,?,'tech-lead',1,?)`,
		id, id, startedAt)
	if flagged {
		mustExec(t, db, `INSERT INTO trajectory_findings(score_id,kind,severity,evidence_turn_ids) VALUES (?,'verify-skip','warn','[1]')`, id)
	}
}

// Recency contract: among unflagged candidates the judge works newest-first,
// so verdicts surface in Retro's recent-sessions window instead of draining
// the pool oldest-first. Flagged candidates still take absolute priority.
func TestSelectCandidatesPrefersRecentSessions(t *testing.T) {
	db := openMigratedDB(t)
	mustExec(t, db, `INSERT INTO projects(id,name,path,slug,first_seen) VALUES (1,'p','/p','p','2026-07-01T00:00:00Z')`)
	seedScoredSession(t, db, 1, "2026-06-01T00:00:00Z", false) // oldest
	seedScoredSession(t, db, 2, "2026-07-20T00:00:00Z", false) // newest
	seedScoredSession(t, db, 3, "2026-07-01T00:00:00Z", false) // middle

	cands, err := selectCandidates(db, "sonnet", 3)
	if err != nil {
		t.Fatal(err)
	}
	var order []int64
	for _, c := range cands {
		order = append(order, c.sessionID)
	}
	if len(order) != 3 || order[0] != 2 || order[1] != 3 || order[2] != 1 {
		t.Errorf("candidate order = %v, want [2 3 1] (newest session first)", order)
	}
}

func TestSelectCandidatesFlaggedBeatsRecency(t *testing.T) {
	db := openMigratedDB(t)
	mustExec(t, db, `INSERT INTO projects(id,name,path,slug,first_seen) VALUES (1,'p','/p','p','2026-07-01T00:00:00Z')`)
	seedScoredSession(t, db, 1, "2026-06-01T00:00:00Z", true)  // old but flagged
	seedScoredSession(t, db, 2, "2026-07-20T00:00:00Z", false) // new, clean

	cands, err := selectCandidates(db, "sonnet", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].sessionID != 1 {
		t.Errorf("cands = %+v, want the old flagged session 1 first", cands)
	}
}

// TestJudgedWithin locks the startup-cooldown gate: the daemon restarts many
// times on an active dev day (every `make install`), and each restart used to
// fire a full capN judge batch. JudgedWithin lets main.go skip the batch when
// a judgment for this model was persisted recently; errors and empty tables
// fail open (false) so a fresh install still judges.
func TestJudgedWithin(t *testing.T) {
	db := openMigratedDB(t)
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)

	if JudgedWithin(db, "sonnet", now, 6*time.Hour) {
		t.Error("empty table: want false (fail open)")
	}

	mustExec(t, db, `INSERT INTO projects(id,name,path,slug,first_seen) VALUES (1,'p','/p','p','2026-07-27T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO sessions(id,project_id,session_uuid,started_at) VALUES (1,1,'u1','2026-07-27T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO trajectory_judgments(session_id,agent,model,judged_at,end_result,instruction_compliance,pitfalls,tool_calls,overall,review)
	                 VALUES (1,'main','sonnet','2026-07-27T11:00:00Z',4,4,4,4,4.0,'ok')`)

	if !JudgedWithin(db, "sonnet", now, 6*time.Hour) {
		t.Error("judgment 1h old, window 6h: want true")
	}
	if JudgedWithin(db, "sonnet", now, 30*time.Minute) {
		t.Error("judgment 1h old, window 30m: want false")
	}
	if JudgedWithin(db, "opus", now, 6*time.Hour) {
		t.Error("other model only: want false")
	}
}
