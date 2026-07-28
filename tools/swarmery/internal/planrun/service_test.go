package planrun

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/worktree"
)

// ── test doubles (shape mirrors phaserun's, which these were derived from) ──

// stubRunner records specs and returns a canned Run. When block is non-nil the
// Start call waits on it (so a test can observe the in-flight state before the
// run completes and releases the slot); a Cancel() unblocks it via ctx.
type stubRunner struct {
	mu       sync.Mutex
	specs    []RunSpec
	block    chan struct{}
	runFn    func(spec RunSpec) (*Run, error)
	startErr error
}

func (s *stubRunner) Start(ctx context.Context, spec RunSpec) (*Run, error) {
	s.mu.Lock()
	s.specs = append(s.specs, spec)
	block := s.block
	fn := s.runFn
	startErr := s.startErr
	s.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done(): // Cancel() aborts the run
			return &Run{SessionUUID: spec.SessionUUID, ExitCode: -1}, nil
		}
	}
	if startErr != nil {
		return &Run{SessionUUID: spec.SessionUUID, ExitCode: -1}, startErr
	}
	if fn != nil {
		return fn(spec)
	}
	return &Run{SessionUUID: spec.SessionUUID, ExitCode: 0}, nil
}

func (s *stubRunner) lastSpec() RunSpec {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.specs) == 0 {
		return RunSpec{}
	}
	return s.specs[len(s.specs)-1]
}

func (s *stubRunner) count() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.specs) }

// stubWt is a scripted WorktreeManager recording Acquire/Remove calls.
type stubWt struct {
	mu         sync.Mutex
	acquired   []string // taskIDs handed to Acquire
	removed    []worktree.Acquired
	keepBranch []bool
	acquireErr error
}

func (w *stubWt) Acquire(repoRoot, projectSlug, taskID string) (worktree.Acquired, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.acquireErr != nil {
		return worktree.Acquired{}, w.acquireErr
	}
	w.acquired = append(w.acquired, taskID)
	return worktree.Acquired{
		Path:   "/wt/" + projectSlug + "/" + taskID,
		Branch: "swarm/" + taskID,
	}, nil
}

func (w *stubWt) Remove(repoRoot string, a worktree.Acquired, keepBranch bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.removed = append(w.removed, a)
	w.keepBranch = append(w.keepBranch, keepBranch)
	return nil
}

func (w *stubWt) acquiredCount() int { w.mu.Lock(); defer w.mu.Unlock(); return len(w.acquired) }
func (w *stubWt) removedCount() int  { w.mu.Lock(); defer w.mu.Unlock(); return len(w.removed) }

// ── harness ──

// fixture builds a DB with one project, one workspace epic task registered as a
// plan artifact, a plan/ dir with README + two phase docs, and two phase rows
// (phase 2 depends on seq 1).
func fixture(t *testing.T) (*sql.DB, int64, string) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "planrun.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	planDir := filepath.Join(t.TempDir(), "ws", "plan")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	doc1 := filepath.Join(planDir, "phase-1-schema.md")
	doc2 := filepath.Join(planDir, "phase-2-ui.md")
	mustWrite(t, filepath.Join(planDir, "README.md"), "# My Epic\n\nObjective: ship it.\n")
	mustWrite(t, doc1, "# Phase 1 — Schema\n\n- [ ] a\n- [ ] b\n")
	mustWrite(t, doc2, "# Phase 2 — UI\n\n- [ ] c\n")

	mustExec(t, db, `INSERT INTO projects(id, path, slug, first_seen) VALUES(1,'/repo/p','p','2026-01-01T00:00:00Z')`)
	res, err := db.Exec(`INSERT INTO tasks (project_id, title, prompt, status, created_at,
		started_at, source, external_id) VALUES (1,'My Epic','goal','running',
		'2026-07-27T00:00:00Z','2026-07-27T00:00:00Z','workspace','2026-07-27-my-epic')`)
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := res.LastInsertId()
	mustExec(t, db, `INSERT INTO task_artifacts (task_id, kind, path, content_hash, parsed_at)
		VALUES (?, 'plan', ?, 'hash', '2026-07-27T00:00:00Z')`, taskID, planDir)

	mustExec(t, db, `INSERT INTO epic_phases
		(workspace_task_id, seq, name, doc_path, depends_on, checkboxes_total, checkboxes_done)
		VALUES (?, 1, 'Phase 1 — Schema', ?, '[]', 2, 0)`, taskID, doc1)
	mustExec(t, db, `INSERT INTO epic_phases
		(workspace_task_id, seq, name, doc_path, depends_on, checkboxes_total, checkboxes_done)
		VALUES (?, 2, 'Phase 2 — UI', ?, '[1]', 1, 0)`, taskID, doc2)
	return db, taskID, planDir
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec %s: %v", q, err)
	}
}

// newTestService builds a service with a SYNC Go seam (Start blocks until the
// run goroutine finishes) — deterministic end-state assertions. Tests that need
// an in-flight run override Go with nil (real goroutine) + a blocking runner.
func newTestService(db *sql.DB, r Runner, wt *stubWt) *Service {
	s := NewService(db, r, wt)
	s.UUID = func() string { return "uuid-1" }
	s.Go = func(fn func()) { fn() }
	return s
}

func planRow(t *testing.T, db *sql.DB, taskID int64) (state string, agent, uuid, startedAt, runErr sql.NullString) {
	t.Helper()
	if err := db.QueryRow(`SELECT run_state, agent, run_session_uuid, run_started_at, run_error
		FROM plan_runs WHERE workspace_task_id=?`, taskID).Scan(&state, &agent, &uuid, &startedAt, &runErr); err != nil {
		t.Fatalf("plan_runs row: %v", err)
	}
	return
}

// waitFor polls until cond() or 2s. For in-flight (async) tests only.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within 2s")
}

// ── tests ──

func TestStartHappyPath(t *testing.T) {
	db, taskID, planDir := fixture(t)
	r := &stubRunner{}
	wt := &stubWt{}
	s := newTestService(db, r, wt)
	var notified []int64
	s.Notify = func(id int64) { notified = append(notified, id) }

	uuid, err := s.Start(taskID, "tech-lead", "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if uuid != "uuid-1" {
		t.Errorf("uuid = %q, want uuid-1", uuid)
	}

	// The sync Go seam means the run already finished: state is terminal.
	state, agent, gotUUID, startedAt, runErr := planRow(t, db, taskID)
	if state != "done" {
		t.Errorf("run_state = %q, want done", state)
	}
	if agent.String != "tech-lead" {
		t.Errorf("agent = %q, want tech-lead", agent.String)
	}
	if gotUUID.String != "uuid-1" || startedAt.String == "" {
		t.Errorf("uuid/startedAt = %q/%q, want both set", gotUUID.String, startedAt.String)
	}
	if runErr.Valid {
		t.Errorf("run_error = %q, want NULL", runErr.String)
	}

	// Worktree acquired under a plan-scoped id and removed keeping the branch.
	if wt.acquiredCount() != 1 || wt.removedCount() != 1 {
		t.Fatalf("worktree acquire/remove = %d/%d, want 1/1", wt.acquiredCount(), wt.removedCount())
	}
	if got := wt.acquired[0]; !strings.HasPrefix(got, "plan-") {
		t.Errorf("worktree task id = %q, want plan-<taskId>", got)
	}
	if !wt.keepBranch[0] {
		t.Error("keepBranch = false, want true (commits must stay reachable)")
	}

	// The spec carries the agent, the worktree cwd, and a prompt that points at
	// the run-plan skill and the plan dir rather than restating the procedure.
	spec := r.lastSpec()
	if spec.Agent != "tech-lead" {
		t.Errorf("spec.Agent = %q, want tech-lead", spec.Agent)
	}
	if spec.Cwd != wt.removed[0].Path {
		t.Errorf("spec.Cwd = %q, want the acquired worktree path", spec.Cwd)
	}
	for _, want := range []string{"run-plan", planDir, "Objective: ship it.", "Phase 1", "Phase 2"} {
		if !strings.Contains(spec.Prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}

	// plan_updated fires on both edges (start + exit) so the page refetches.
	if len(notified) != 2 {
		t.Errorf("notify calls = %d, want 2 (start + exit)", len(notified))
	}
}

func TestStartDefaultsAgent(t *testing.T) {
	db, taskID, _ := fixture(t)
	r := &stubRunner{}
	s := newTestService(db, r, &stubWt{})

	if _, err := s.Start(taskID, "", ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := r.lastSpec().Agent; got != DefaultAgent() {
		t.Errorf("spec.Agent = %q, want the default %q", got, DefaultAgent())
	}
	_, agent, _, _, _ := planRow(t, db, taskID)
	if agent.String != DefaultAgent() {
		t.Errorf("stored agent = %q, want %q", agent.String, DefaultAgent())
	}
}

func TestStartAgentEnvOverride(t *testing.T) {
	t.Setenv(agentEnv, "custom-lead")
	if got := DefaultAgent(); got != "custom-lead" {
		t.Fatalf("DefaultAgent() = %q, want custom-lead", got)
	}
}

func TestStartGates(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(t *testing.T, db *sql.DB, taskID int64, planDir string)
		want    error
	}{
		{
			name: "paused plan",
			arrange: func(t *testing.T, db *sql.DB, taskID int64, _ string) {
				mustExec(t, db, `UPDATE tasks SET status='paused' WHERE id=?`, taskID)
			},
			want: ErrNotActive,
		},
		{
			name: "archived plan",
			arrange: func(t *testing.T, db *sql.DB, taskID int64, _ string) {
				mustExec(t, db, `UPDATE tasks SET archived_at='2026-07-27T00:00:00Z' WHERE id=?`, taskID)
			},
			want: ErrNotActive,
		},
		{
			name: "no phases",
			arrange: func(t *testing.T, db *sql.DB, taskID int64, _ string) {
				mustExec(t, db, `DELETE FROM epic_phases WHERE workspace_task_id=?`, taskID)
			},
			want: ErrNoPhases,
		},
		{
			name: "every phase already done",
			arrange: func(t *testing.T, db *sql.DB, taskID int64, _ string) {
				mustExec(t, db, `UPDATE epic_phases SET checkboxes_done = checkboxes_total
					WHERE workspace_task_id=?`, taskID)
			},
			want: ErrComplete,
		},
		{
			name: "a phase run is in flight",
			arrange: func(t *testing.T, db *sql.DB, taskID int64, _ string) {
				mustExec(t, db, `UPDATE epic_phases SET run_state='running'
					WHERE workspace_task_id=? AND seq=1`, taskID)
			},
			want: ErrPhaseRunning,
		},
		{
			name: "a plan run is already running",
			arrange: func(t *testing.T, db *sql.DB, taskID int64, _ string) {
				mustExec(t, db, `INSERT INTO plan_runs (workspace_task_id, run_state)
					VALUES (?, 'running')`, taskID)
			},
			want: ErrRunning,
		},
		{
			name: "project has no path",
			arrange: func(t *testing.T, db *sql.DB, _ int64, _ string) {
				mustExec(t, db, `UPDATE projects SET path='' WHERE id=1`)
			},
			want: ErrNoPath,
		},
		{
			name: "README missing",
			arrange: func(t *testing.T, _ *sql.DB, _ int64, planDir string) {
				os.Remove(filepath.Join(planDir, "README.md"))
			},
			want: ErrNoDoc,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, taskID, planDir := fixture(t)
			tc.arrange(t, db, taskID, planDir)
			r := &stubRunner{}
			wt := &stubWt{}
			s := newTestService(db, r, wt)

			_, err := s.Start(taskID, "", "")
			if !errors.Is(err, tc.want) {
				t.Fatalf("Start error = %v, want %v", err, tc.want)
			}
			if r.count() != 0 {
				t.Error("a gated Start must not spawn")
			}
			if wt.acquiredCount() != 0 {
				t.Error("a gated Start must not acquire a worktree")
			}
		})
	}
}

func TestStartUnknownPlan(t *testing.T) {
	db, _, _ := fixture(t)
	s := newTestService(db, &stubRunner{}, &stubWt{})
	if _, err := s.Start(9999, "", ""); !errors.Is(err, ErrPlanNotFound) {
		t.Fatalf("Start error = %v, want ErrPlanNotFound", err)
	}
}

func TestStartWorktreeFailureReleasesSlot(t *testing.T) {
	db, taskID, _ := fixture(t)
	wt := &stubWt{acquireErr: errors.New("boom")}
	s := newTestService(db, &stubRunner{}, wt)

	if _, err := s.Start(taskID, "", ""); err == nil {
		t.Fatal("Start: want an error when Acquire fails")
	}
	// No row was stamped, and the single-flight slot is free for a retry.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM plan_runs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("plan_runs rows = %d, want 0 (nothing stamped)", n)
	}
	s.mu.Lock()
	inFlight := len(s.active)
	s.mu.Unlock()
	if inFlight != 0 {
		t.Errorf("in-flight slots = %d, want 0", inFlight)
	}
}

func TestRunOutcomesAreStamped(t *testing.T) {
	cases := []struct {
		name      string
		runner    *stubRunner
		wantState string
		wantErr   string
	}{
		{
			name:      "nonzero exit",
			runner:    &stubRunner{runFn: func(RunSpec) (*Run, error) { return &Run{ExitCode: 2, Stderr: "kaboom"}, nil }},
			wantState: "failed",
			wantErr:   "kaboom",
		},
		{
			name:      "timeout",
			runner:    &stubRunner{runFn: func(RunSpec) (*Run, error) { return &Run{TimedOut: true, ExitCode: -1}, nil }},
			wantState: "failed",
			wantErr:   "timeout",
		},
		{
			name:      "could not start",
			runner:    &stubRunner{startErr: errors.New("no claude binary")},
			wantState: "failed",
			wantErr:   "no claude binary",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, taskID, _ := fixture(t)
			wt := &stubWt{}
			s := newTestService(db, tc.runner, wt)
			if _, err := s.Start(taskID, "", ""); err != nil {
				t.Fatalf("Start: %v", err)
			}
			state, _, _, _, runErr := planRow(t, db, taskID)
			if state != tc.wantState {
				t.Errorf("run_state = %q, want %q", state, tc.wantState)
			}
			if !strings.Contains(runErr.String, tc.wantErr) {
				t.Errorf("run_error = %q, want it to contain %q", runErr.String, tc.wantErr)
			}
			// The worktree is released whatever the outcome.
			if wt.removedCount() != 1 {
				t.Errorf("worktree removals = %d, want 1", wt.removedCount())
			}
		})
	}
}

func TestCancelStampsCancelled(t *testing.T) {
	db, taskID, _ := fixture(t)
	r := &stubRunner{block: make(chan struct{})}
	wt := &stubWt{}
	s := NewService(db, r, wt) // real goroutine — the run must be observable in flight
	s.UUID = func() string { return "uuid-1" }

	if _, err := s.Start(taskID, "", ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, func() bool {
		state, _, _, _, _ := planRow(t, db, taskID)
		return state == "running"
	})
	// A second Start while one is in flight is refused.
	if _, err := s.Start(taskID, "", ""); !errors.Is(err, ErrRunning) {
		t.Errorf("second Start error = %v, want ErrRunning", err)
	}

	if !s.Cancel(taskID) {
		t.Fatal("Cancel returned false for an in-flight run")
	}
	waitFor(t, func() bool {
		state, _, _, _, _ := planRow(t, db, taskID)
		return state == "failed"
	})
	_, _, _, _, runErr := planRow(t, db, taskID)
	if runErr.String != "cancelled" {
		t.Errorf("run_error = %q, want cancelled", runErr.String)
	}
	if s.Cancel(taskID) {
		t.Error("Cancel on an idle plan returned true")
	}
}

func TestHealStale(t *testing.T) {
	db, taskID, _ := fixture(t)
	mustExec(t, db, `INSERT INTO plan_runs (workspace_task_id, run_state, run_session_uuid)
		VALUES (?, 'running', 'orphan')`, taskID)
	s := newTestService(db, &stubRunner{}, &stubWt{})

	if err := s.HealStale(); err != nil {
		t.Fatalf("HealStale: %v", err)
	}
	state, _, _, _, runErr := planRow(t, db, taskID)
	if state != "failed" || runErr.String != "daemon restart" {
		t.Errorf("healed row = (%q, %q), want (failed, daemon restart)", state, runErr.String)
	}
}

func TestStartAfterFailedRunIsAllowed(t *testing.T) {
	db, taskID, _ := fixture(t)
	mustExec(t, db, `INSERT INTO plan_runs (workspace_task_id, run_state, run_error)
		VALUES (?, 'failed', 'earlier boom')`, taskID)
	r := &stubRunner{}
	s := newTestService(db, r, &stubWt{})

	if _, err := s.Start(taskID, "retry-agent", ""); err != nil {
		t.Fatalf("Start after a failed run: %v", err)
	}
	state, agent, _, _, runErr := planRow(t, db, taskID)
	if state != "done" || agent.String != "retry-agent" {
		t.Errorf("row = (%q, %q), want (done, retry-agent)", state, agent.String)
	}
	if runErr.Valid {
		t.Errorf("run_error = %q, want the earlier error cleared", runErr.String)
	}
}

func TestPhaseRunStateIsNeverTouched(t *testing.T) {
	// A plan run is ONE session for the whole plan; claiming per-phase run rows
	// would make the two mechanisms lie about each other.
	db, taskID, _ := fixture(t)
	s := newTestService(db, &stubRunner{}, &stubWt{})
	if _, err := s.Start(taskID, "", ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	rows, err := db.Query(`SELECT run_state FROM epic_phases WHERE workspace_task_id=?`, taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		if err := rows.Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state != "idle" {
			t.Errorf("phase run_state = %q, want idle (untouched by a plan run)", state)
		}
	}
}
