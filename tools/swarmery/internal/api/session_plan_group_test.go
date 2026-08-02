package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
)

// planGroupJSON mirrors sessionPlanDTO.
type planGroupJSON struct {
	TaskID    int64   `json:"taskId"`
	Title     string  `json:"title"`
	Role      string  `json:"role"`
	PhaseID   *int64  `json:"phaseId"`
	PhaseSeq  *int    `json:"phaseSeq"`
	PhaseName *string `json:"phaseName"`
}

type planGroupSession struct {
	SessionUUID string         `json:"sessionUuid"`
	PlanGroup   *planGroupJSON `json:"planGroup"`
}

type planGroupEnvelope struct {
	Sessions []planGroupSession `json:"sessions"`
}

// planGroupServer plants one workspace plan (task 72, phases 1279/1280) and one
// session per shape the resolver has to tell apart:
//
//	ctl      swarm/plan-72     — the plan-run controller
//	phase    swarm/phase-1280  — one phase of that same plan
//	subagent (no git_branch)   — a subagent, located only by its worktree cwd
//	inter    dev               — an ordinary interactive session
//	garbage  swarm/plan-abc    — a swarm branch with a non-numeric suffix
func planGroupServer(t *testing.T) *httptest.Server {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "plangroup.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("exec: %v\n%s", err, q)
		}
	}
	mustExec(`INSERT INTO projects (id, path, slug, name, first_seen) VALUES
		(1, '/work/repo', '-work-repo', 'repo', '2026-07-01T00:00:00Z')`)
	// The workspace task that HOLDS the plan — swarm/plan-<id> points here.
	mustExec(`INSERT INTO tasks (id, project_id, title, prompt, created_at, source)
		VALUES (72, 1, 'Plan-run recovery & honest phase status', '', '2026-07-29T09:00:00Z', 'workspace')`)
	// A decoy task whose id collides with a phase id: proves the phase form is
	// resolved through epic_phases and not read as a plan id.
	mustExec(`INSERT INTO tasks (id, project_id, title, prompt, created_at, source)
		VALUES (1280, 1, 'unrelated board task', '', '2026-07-29T09:00:00Z', 'queue')`)
	mustExec(`INSERT INTO epic_phases (id, workspace_task_id, seq, name, doc_path) VALUES
		(1279, 72, 1, 'Preserve run state across plan re-index', '/w/plan/phase-1-preserve-run-state.md'),
		(1280, 72, 5, 'Group sessions by the plan that spawned them', '/w/plan/phase-5-group-sessions-by-plan.md')`)

	const wt = "/Users/dev/.swarmery/worktrees/-work-repo"
	mustExec(`INSERT INTO sessions (id, project_id, session_uuid, status, started_at, git_branch, cwd) VALUES
		(1, 1, 'ctl',      'completed', '2026-07-29T10:00:00.000Z', 'swarm/plan-72',    ?),
		(2, 1, 'phase',    'idle',      '2026-07-29T10:01:00.000Z', 'swarm/phase-1280', ?),
		(3, 1, 'subagent', 'completed', '2026-07-29T10:02:00.000Z', NULL,               ?),
		(4, 1, 'inter',    'active',    '2026-07-29T10:03:00.000Z', 'dev',              '/work/repo'),
		(5, 1, 'garbage',  'completed', '2026-07-29T10:04:00.000Z', 'swarm/plan-abc',   '/work/repo')`,
		wt+"/plan-72", wt+"/phase-1280", wt+"/phase-1280/tools/swarmery")

	h, err := NewServer(db, false)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// planGroupOf fetches the list and returns one session's group descriptor.
func planGroupOf(t *testing.T, srv *httptest.Server, uuid string) *planGroupJSON {
	t.Helper()
	var page planGroupEnvelope
	getJSON(t, srv.URL+"/api/sessions", &page)
	for _, s := range page.Sessions {
		if s.SessionUUID == uuid {
			return s.PlanGroup
		}
	}
	t.Fatalf("session %q missing from the list", uuid)
	return nil
}

// TestSessionPlanGroupController: swarm/plan-<taskId> groups under that plan as
// the controller, with no phase fields.
func TestSessionPlanGroupController(t *testing.T) {
	g := planGroupOf(t, planGroupServer(t), "ctl")
	if g == nil {
		t.Fatal("planGroup = null, want the plan-72 group")
	}
	if g.TaskID != 72 {
		t.Errorf("taskId = %d, want 72", g.TaskID)
	}
	if g.Role != "controller" {
		t.Errorf("role = %q, want controller", g.Role)
	}
	if g.Title != "Plan-run recovery & honest phase status" {
		t.Errorf("title = %q, want the plan title", g.Title)
	}
	if g.PhaseID != nil || g.PhaseSeq != nil || g.PhaseName != nil {
		t.Errorf("controller carries phase fields: %+v", g)
	}
}

// TestSessionPlanGroupPhase: swarm/phase-<phaseId> groups under the SAME plan
// as its controller and carries phaseSeq/phaseName for the row label.
func TestSessionPlanGroupPhase(t *testing.T) {
	g := planGroupOf(t, planGroupServer(t), "phase")
	if g == nil {
		t.Fatal("planGroup = null, want the plan-72 group")
	}
	if g.TaskID != 72 {
		t.Errorf("taskId = %d, want 72 (the same plan as the controller)", g.TaskID)
	}
	if g.Role != "phase" {
		t.Errorf("role = %q, want phase", g.Role)
	}
	if g.PhaseID == nil || *g.PhaseID != 1280 {
		t.Errorf("phaseId = %v, want 1280", g.PhaseID)
	}
	if g.PhaseSeq == nil || *g.PhaseSeq != 5 {
		t.Errorf("phaseSeq = %v, want 5", g.PhaseSeq)
	}
	if g.PhaseName == nil || *g.PhaseName != "Group sessions by the plan that spawned them" {
		t.Errorf("phaseName = %v, want the phase name", g.PhaseName)
	}
}

// TestSessionPlanGroupSubagentCWDFallback: a subagent has no git_branch of its
// own, so the run is read back out of the worktree path it ran in.
func TestSessionPlanGroupSubagentCWDFallback(t *testing.T) {
	g := planGroupOf(t, planGroupServer(t), "subagent")
	if g == nil {
		t.Fatal("planGroup = null, want the cwd fallback to find plan-72")
	}
	if g.TaskID != 72 || g.Role != "phase" {
		t.Errorf("taskId/role = %d/%q, want 72/phase", g.TaskID, g.Role)
	}
	if g.PhaseID == nil || *g.PhaseID != 1280 {
		t.Errorf("phaseId = %v, want 1280", g.PhaseID)
	}
}

// TestSessionPlanGroupInteractiveIsNull: an ordinary session on a normal branch
// belongs to no plan run.
func TestSessionPlanGroupInteractiveIsNull(t *testing.T) {
	if g := planGroupOf(t, planGroupServer(t), "inter"); g != nil {
		t.Errorf("planGroup = %+v, want null for an interactive session", g)
	}
}

// TestSessionPlanGroupNonNumericSuffixIsNull: swarm/plan-abc is malformed, not
// a server error — it resolves to no group and the request still succeeds.
func TestSessionPlanGroupNonNumericSuffixIsNull(t *testing.T) {
	if g := planGroupOf(t, planGroupServer(t), "garbage"); g != nil {
		t.Errorf("planGroup = %+v, want null for a non-numeric branch suffix", g)
	}
}

// THE case the run_branch join exists for. A plan re-index replaces a phase row and
// mints a new id (epic_phases identity is doc_path), so the branch the session ran on
// no longer matches 'swarm/phase-' || id. Observed live: the sessions for
// swarm/phase-1279/1280 resolved to nothing once task 72's rows became 1290–1295.
// Matching the STAMPED run_branch survives that.
func TestSessionPlanGroupPhaseSurvivesRowIDChange(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "moved.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("exec: %v\n%s", err, q)
		}
	}
	mustExec(`INSERT INTO projects (id, path, slug, name, first_seen) VALUES
		(1, '/work/repo', '-work-repo', 'repo', '2026-07-01T00:00:00Z')`)
	mustExec(`INSERT INTO tasks (id, project_id, title, prompt, created_at, source)
		VALUES (72, 1, 'Usage modal live quotas', '', '2026-07-29T09:00:00Z', 'workspace')`)
	// The row the run happened on is GONE. Its replacement carries a new id and the
	// branch that run committed to, exactly as wsingest.applyEpics hands it over.
	mustExec(`INSERT INTO epic_phases (id, workspace_task_id, seq, name, doc_path, run_state, run_branch)
		VALUES (1292, 72, 3, 'Consolidate and land', '/w/plan/phase-3-new-name.md', 'done', 'swarm/phase-1280')`)
	mustExec(`INSERT INTO sessions (id, project_id, session_uuid, status, started_at, git_branch, cwd)
		VALUES (1, 1, 'moved', 'completed', '2026-07-29T10:00:00.000Z', 'swarm/phase-1280',
		        '/Users/dev/.swarmery/worktrees/-work-repo/phase-1280')`)

	h, err := NewServer(db, false)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	g := planGroupOf(t, srv, "moved")
	if g == nil {
		t.Fatal("planGroup = null — the session was orphaned by the row-id change, which is the whole defect")
	}
	if g.TaskID != 72 || g.Role != "phase" {
		t.Errorf("taskId/role = %d/%q, want 72/phase", g.TaskID, g.Role)
	}
	if g.PhaseID == nil || *g.PhaseID != 1292 {
		t.Errorf("phaseId = %v, want the REPLACEMENT row 1292", g.PhaseID)
	}
	if g.PhaseSeq == nil || *g.PhaseSeq != 3 {
		t.Errorf("phaseSeq = %v, want 3", g.PhaseSeq)
	}
}

// planTaskUUIDs fetches ?planTask=<id> and returns the session uuids it kept,
// in list order.
func planTaskUUIDs(t *testing.T, srv *httptest.Server, planTask string) []string {
	t.Helper()
	var page planGroupEnvelope
	getJSON(t, srv.URL+"/api/sessions?planTask="+planTask, &page)
	out := make([]string, 0, len(page.Sessions))
	for _, s := range page.Sessions {
		out = append(out, s.SessionUUID)
	}
	return out
}

// TestSessionsFilterByPlanTask: the plan panel asks for one plan's sessions and
// gets exactly the three the run produced — controller, phase and subagent —
// while the interactive and malformed-branch rows stay out.
func TestSessionsFilterByPlanTask(t *testing.T) {
	got := planTaskUUIDs(t, planGroupServer(t), "72")
	want := map[string]bool{"ctl": true, "phase": true, "subagent": true}
	if len(got) != len(want) {
		t.Fatalf("sessions = %v, want the 3 sessions of plan 72", got)
	}
	for _, uuid := range got {
		if !want[uuid] {
			t.Errorf("session %q leaked into the plan-72 list", uuid)
		}
	}
}

// A plan nobody ran filters to an empty list rather than to everything — the
// predicate must not be dropped when it matches no row.
func TestSessionsFilterByPlanTaskUnknownIsEmpty(t *testing.T) {
	if got := planTaskUUIDs(t, planGroupServer(t), "999"); len(got) != 0 {
		t.Errorf("sessions = %v, want none for a plan with no runs", got)
	}
}

// A non-numeric planTask is a client error, never a 500 and never an unfiltered
// list (which would read as "this plan spawned every session on the machine").
func TestSessionsFilterByPlanTaskInvalid(t *testing.T) {
	srv := planGroupServer(t)
	res, err := http.Get(srv.URL + "/api/sessions?planTask=abc")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", res.StatusCode)
	}
}
