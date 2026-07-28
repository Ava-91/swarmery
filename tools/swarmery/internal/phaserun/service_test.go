package phaserun

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/worktree"
)

// ── test doubles ──

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

// stubWt is a scripted WorktreeManager recording Acquire/Remove calls.
type stubWt struct {
	mu         sync.Mutex
	acquired   []string // taskIDs handed to Acquire
	removed    []worktree.Acquired
	keepBranch []bool
	acquireErr error
	onRemove   func() // observed inside Remove, before it returns
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
	w.removed = append(w.removed, a)
	w.keepBranch = append(w.keepBranch, keepBranch)
	hook := w.onRemove
	w.mu.Unlock()
	if hook != nil {
		hook()
	}
	return nil
}

func (w *stubWt) acquiredCount() int { w.mu.Lock(); defer w.mu.Unlock(); return len(w.acquired) }
func (w *stubWt) removedCount() int  { w.mu.Lock(); defer w.mu.Unlock(); return len(w.removed) }

// ── harness ──

// fixture builds a DB with one project, one workspace epic task, and two
// phases on disk: phase 1 (no deps) and phase 2 (depends on seq 1). Returns
// the db, the workspace task id, and the two phase ids.
func fixture(t *testing.T) (*sql.DB, int64, int64, int64) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "phaserun.db"))
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
	os.WriteFile(doc1, []byte("# Phase 1 — Schema\n\n- [ ] a\n- [ ] b\n"), 0o644)
	os.WriteFile(doc2, []byte("# Phase 2 — UI\n\n- [ ] c\n"), 0o644)

	mustExec(t, db, `INSERT INTO projects(id, path, slug, first_seen) VALUES(1,'/repo/p','p','2026-01-01T00:00:00Z')`)
	res, err := db.Exec(`INSERT INTO tasks (project_id, title, prompt, status, created_at,
		started_at, source, external_id) VALUES (1,'My Epic','goal','running',
		'2026-07-27T00:00:00Z','2026-07-27T00:00:00Z','workspace','2026-07-27-my-epic')`)
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := res.LastInsertId()

	r1, err := db.Exec(`INSERT INTO epic_phases
		(workspace_task_id, seq, name, doc_path, depends_on, checkboxes_total, checkboxes_done)
		VALUES (?, 1, 'Phase 1 — Schema', ?, '[]', 2, 0)`, taskID, doc1)
	if err != nil {
		t.Fatal(err)
	}
	p1, _ := r1.LastInsertId()
	r2, err := db.Exec(`INSERT INTO epic_phases
		(workspace_task_id, seq, name, doc_path, depends_on, checkboxes_total, checkboxes_done)
		VALUES (?, 2, 'Phase 2 — UI', ?, '[1]', 1, 0)`, taskID, doc2)
	if err != nil {
		t.Fatal(err)
	}
	p2, _ := r2.LastInsertId()
	return db, taskID, p1, p2
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec %s: %v", q, err)
	}
}

// newTestService builds a service with a SYNC Go seam (Start blocks until the
// run goroutine finishes) — deterministic end-state assertions. Tests that
// need an in-flight run override Go with nil (real goroutine) + a blocking
// runner.
func newTestService(db *sql.DB, r Runner, wt *stubWt) *Service {
	s := NewService(db, r, wt)
	s.UUID = func() string { return "uuid-1" }
	s.Go = func(fn func()) { fn() }
	return s
}

func phaseRow(t *testing.T, db *sql.DB, id int64) (state string, uuid, startedAt, runErr sql.NullString) {
	t.Helper()
	if err := db.QueryRow(`SELECT run_state, run_session_uuid, run_started_at, run_error
		FROM epic_phases WHERE id=?`, id).Scan(&state, &uuid, &startedAt, &runErr); err != nil {
		t.Fatalf("phase row: %v", err)
	}
	return
}

// phaseOutcome reads the run-outcome measurement columns (migration 0041):
// the ticked-criteria baseline snapshotted at spawn and the terminal timestamp.
func phaseOutcome(t *testing.T, db *sql.DB, id int64) (before sql.NullInt64, endedAt sql.NullString) {
	t.Helper()
	if err := db.QueryRow(`SELECT run_checkboxes_before, run_ended_at
		FROM epic_phases WHERE id=?`, id).Scan(&before, &endedAt); err != nil {
		t.Fatalf("phase outcome row: %v", err)
	}
	return
}

func taskCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
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
	t.Fatal("condition not reached in 2s")
}

// ── tests ──

func TestStart_HappyPath(t *testing.T) {
	db, taskID, p1, _ := fixture(t)
	r := &stubRunner{}
	wt := &stubWt{}
	s := newTestService(db, r, wt)
	var notified []int64
	s.Notify = func(id int64) { notified = append(notified, id) }
	before := taskCount(t, db)

	uuid, err := s.Start(p1)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if uuid != "uuid-1" {
		t.Errorf("uuid = %q", uuid)
	}

	state, u, started, runErr := phaseRow(t, db, p1)
	if state != "done" {
		t.Errorf("run_state = %q, want done", state)
	}
	if u.String != "uuid-1" {
		t.Errorf("run_session_uuid = %q", u.String)
	}
	if !started.Valid || started.String == "" {
		t.Error("run_started_at not stamped")
	}
	if runErr.Valid {
		t.Errorf("run_error = %q, want NULL", runErr.String)
	}

	// Worktree acquired under phase-<id> and removed with the branch kept.
	if wt.acquiredCount() != 1 || wt.acquired[0] != "phase-"+itoa64(p1) {
		t.Errorf("acquired = %v", wt.acquired)
	}
	if wt.removedCount() != 1 || !wt.keepBranch[0] {
		t.Errorf("removed = %v keepBranch = %v, want 1 removal with keepBranch=true", wt.removed, wt.keepBranch)
	}

	// The spawned run: cwd is the worktree, prompt embeds the phase doc.
	spec := r.lastSpec()
	if spec.Cwd != wt.removed[0].Path {
		t.Errorf("spec.Cwd = %q, want the acquired worktree %q", spec.Cwd, wt.removed[0].Path)
	}
	if !strings.Contains(spec.Prompt, "# Phase 1 — Schema") ||
		!strings.Contains(spec.Prompt, "phase-1-schema.md") {
		t.Errorf("prompt does not embed the phase doc:\n%s", spec.Prompt)
	}
	if spec.SessionUUID != "uuid-1" {
		t.Errorf("spec.SessionUUID = %q", spec.SessionUUID)
	}

	// Notify fired at both edges, keyed by the WORKSPACE task id.
	if len(notified) < 2 || notified[0] != taskID || notified[len(notified)-1] != taskID {
		t.Errorf("notified = %v, want ≥2 notifications for task %d", notified, taskID)
	}

	// No tasks rows minted anywhere in the flow.
	if after := taskCount(t, db); after != before {
		t.Errorf("tasks count changed %d → %d; phase runs must not create tasks", before, after)
	}
}

// TestStart_SnapshotsCheckboxesBefore: run_state records how the PROCESS ended,
// never whether work landed. The baseline snapshot taken at spawn is what makes
// the delta across a run measurable — a 'done' run with a zero delta produced
// nothing.
func TestStart_SnapshotsCheckboxesBefore(t *testing.T) {
	db, _, p1, _ := fixture(t)
	mustExec(t, db, `UPDATE epic_phases SET checkboxes_total=8, checkboxes_done=3 WHERE id=?`, p1)
	s := newTestService(db, &stubRunner{}, &stubWt{})

	if _, err := s.Start(p1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	before, _ := phaseOutcome(t, db, p1)
	if !before.Valid || before.Int64 != 3 {
		t.Errorf("run_checkboxes_before = %v (valid=%v), want 3 — the ticked count at spawn time",
			before.Int64, before.Valid)
	}
}

// phaseAfter reads run_checkboxes_after (migration 0042) — the right edge of the
// run's measurement interval, stamped at exit.
func phaseAfter(t *testing.T, db *sql.DB, id int64) sql.NullInt64 {
	t.Helper()
	var after sql.NullInt64
	if err := db.QueryRow(`SELECT run_checkboxes_after FROM epic_phases WHERE id=?`, id).
		Scan(&after); err != nil {
		t.Fatalf("phase after row: %v", err)
	}
	return after
}

// TestStamp_ClosesCheckboxInterval: the exit stamp pins the ticked count as it was
// when the run ended. checkboxes_done keeps moving afterwards (the wsingest rescan
// and TickPhaseChecklist both write it) — the stamped edge must not follow it, or
// another writer's ticks get attributed to this run.
func TestStamp_ClosesCheckboxInterval(t *testing.T) {
	db, _, p1, _ := fixture(t)
	mustExec(t, db, `UPDATE epic_phases SET checkboxes_total=8, checkboxes_done=1 WHERE id=?`, p1)
	// The run itself ticks two more boxes before it exits.
	r := &stubRunner{runFn: func(spec RunSpec) (*Run, error) {
		mustExec(t, db, `UPDATE epic_phases SET checkboxes_done=3 WHERE id=?`, p1)
		return &Run{SessionUUID: spec.SessionUUID, ExitCode: 0}, nil
	}}
	s := newTestService(db, r, &stubWt{})

	if _, err := s.Start(p1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	before, _ := phaseOutcome(t, db, p1)
	if !before.Valid || before.Int64 != 1 {
		t.Errorf("run_checkboxes_before = %v, want 1", before)
	}
	after := phaseAfter(t, db, p1)
	if !after.Valid || after.Int64 != 3 {
		t.Fatalf("run_checkboxes_after = %v, want 3 — the count at exit", after)
	}

	// A later writer moves the live count; the closed interval must not budge.
	mustExec(t, db, `UPDATE epic_phases SET checkboxes_done=8 WHERE id=?`, p1)
	if got := phaseAfter(t, db, p1); !got.Valid || got.Int64 != 3 {
		t.Errorf("run_checkboxes_after = %v after a later tick, want a frozen 3", got)
	}
}

// TestRunTeardown_WorktreeRemovedBeforeSlotRelease: the single-flight slot is the
// LAST thing the teardown releases. stamp() has already opened the DB gate, so a
// slot released before the git shell-out lets a re-Start re-acquire the same
// deterministic worktree path — which this defer would then delete underneath it.
func TestRunTeardown_WorktreeRemovedBeforeSlotRelease(t *testing.T) {
	db, _, p1, _ := fixture(t)
	wt := &stubWt{}
	var (
		s          *Service
		slotHeld   bool
		hookCalled bool
	)
	wt.onRemove = func() {
		hookCalled = true
		s.mu.Lock()
		_, slotHeld = s.active[p1]
		s.mu.Unlock()
	}
	s = newTestService(db, &stubRunner{}, wt)

	if _, err := s.Start(p1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !hookCalled {
		t.Fatal("worktree was never removed")
	}
	if !slotHeld {
		t.Error("single-flight slot was already released when the worktree was removed — " +
			"a concurrent re-Start could reuse and then lose that worktree")
	}
	if _, busy := s.active[p1]; busy {
		t.Error("slot still held after the run finished")
	}
}

// TestStamp_RowVanished: an UPDATE that matches nothing is the exact shape the
// delete+reinsert defect took — it must be loud, not silent, and never panic.
func TestStamp_RowVanished(t *testing.T) {
	db, _, p1, _ := fixture(t)
	s := newTestService(db, &stubRunner{}, &stubWt{})
	mustExec(t, db, `DELETE FROM epic_phases WHERE id=?`, p1)

	var buf strings.Builder
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	s.stamp(p1, "done", "") // must not panic
	if !strings.Contains(buf.String(), "row vanished mid-run") {
		t.Errorf("stamp against a deleted row logged %q, want a 'row vanished mid-run' error", buf.String())
	}
}

// TestStart_StampsRunEndedAt: every terminal transition records an end
// timestamp so a run's duration is derivable (only run_started_at was persisted
// before).
func TestStart_StampsRunEndedAt(t *testing.T) {
	for _, tc := range []struct {
		name  string
		runFn func(spec RunSpec) (*Run, error)
		want  string
	}{
		{"completed", nil, "done"},
		{"failed", func(spec RunSpec) (*Run, error) {
			return &Run{SessionUUID: spec.SessionUUID, ExitCode: 3, Stderr: "boom"}, nil
		}, "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, _, p1, _ := fixture(t)
			s := newTestService(db, &stubRunner{runFn: tc.runFn}, &stubWt{})
			if _, err := s.Start(p1); err != nil {
				t.Fatalf("Start: %v", err)
			}
			if state, _, _, _ := phaseRow(t, db, p1); state != tc.want {
				t.Fatalf("run_state = %q, want %q", state, tc.want)
			}
			_, endedAt := phaseOutcome(t, db, p1)
			if !endedAt.Valid || endedAt.String == "" {
				t.Errorf("run_ended_at = %q (valid=%v), want a timestamp on the terminal transition",
					endedAt.String, endedAt.Valid)
			}
		})
	}
}

// TestStart_ClearsPriorEndedAt: a re-run must not carry the previous run's end
// stamp while it is in flight.
func TestStart_ClearsPriorEndedAt(t *testing.T) {
	db, _, p1, _ := fixture(t)
	r := &stubRunner{block: make(chan struct{})}
	s := NewService(db, r, &stubWt{}) // real goroutine — run stays in flight
	s.UUID = func() string { return "uuid-1" }
	mustExec(t, db, `UPDATE epic_phases SET run_state='failed', run_ended_at='2026-01-01T00:00:00Z' WHERE id=?`, p1)

	if _, err := s.Start(p1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, func() bool {
		state, _, _, _ := phaseRow(t, db, p1)
		return state == "running"
	})
	if _, endedAt := phaseOutcome(t, db, p1); endedAt.Valid {
		t.Errorf("run_ended_at = %q while running, want NULL", endedAt.String)
	}
	close(r.block)
	waitFor(t, func() bool {
		state, _, _, _ := phaseRow(t, db, p1)
		return state == "done"
	})
}

func TestStart_NonzeroExit_Failed(t *testing.T) {
	db, _, p1, _ := fixture(t)
	r := &stubRunner{runFn: func(spec RunSpec) (*Run, error) {
		return &Run{SessionUUID: spec.SessionUUID, ExitCode: 3, Stderr: "boom"}, nil
	}}
	wt := &stubWt{}
	s := newTestService(db, r, wt)

	if _, err := s.Start(p1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	state, _, _, runErr := phaseRow(t, db, p1)
	if state != "failed" {
		t.Errorf("run_state = %q, want failed", state)
	}
	if !strings.Contains(runErr.String, "boom") {
		t.Errorf("run_error = %q, want the stderr tail", runErr.String)
	}
	if wt.removedCount() != 1 {
		t.Error("worktree not removed on failure")
	}
}

func TestStart_Timeout_Failed(t *testing.T) {
	db, _, p1, _ := fixture(t)
	r := &stubRunner{runFn: func(spec RunSpec) (*Run, error) {
		return &Run{SessionUUID: spec.SessionUUID, ExitCode: -1, TimedOut: true}, nil
	}}
	s := newTestService(db, r, &stubWt{})

	if _, err := s.Start(p1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	state, _, _, runErr := phaseRow(t, db, p1)
	if state != "failed" || runErr.String != "timeout" {
		t.Errorf("state=%q run_error=%q, want failed/timeout", state, runErr.String)
	}
}

func TestStart_RunnerStartError_Failed(t *testing.T) {
	db, _, p1, _ := fixture(t)
	r := &stubRunner{startErr: errors.New("fork: claude not found")}
	s := newTestService(db, r, &stubWt{})

	if _, err := s.Start(p1); err != nil {
		t.Fatalf("Start (admission) should succeed; spawn failure is stamped: %v", err)
	}
	state, _, _, runErr := phaseRow(t, db, p1)
	if state != "failed" || !strings.Contains(runErr.String, "claude not found") {
		t.Errorf("state=%q run_error=%q", state, runErr.String)
	}
}

func TestStart_DepsGate(t *testing.T) {
	t.Run("unmet dep blocks", func(t *testing.T) {
		db, _, _, p2 := fixture(t)
		s := newTestService(db, &stubRunner{}, &stubWt{})
		_, err := s.Start(p2)
		if !errors.Is(err, ErrDepsUnmet) {
			t.Fatalf("err = %v, want ErrDepsUnmet", err)
		}
		var de *DepsUnmetError
		if !errors.As(err, &de) || len(de.Unmet) != 1 || de.Unmet[0] != 1 {
			t.Errorf("unmet = %+v, want [1]", de)
		}
	})

	// The live incident: a headless run that exits 0 without ticking anything
	// (failed precondition, refused work) is NOT a completed phase. run_state
	// answers "how did the process end", never "did work land" — and treating
	// 'done' as completion let phases start on top of an empty dependency.
	t.Run("run_state done with zero ticks does not satisfy", func(t *testing.T) {
		db, _, p1, p2 := fixture(t)
		mustExec(t, db, `UPDATE epic_phases
			SET run_state='done', checkboxes_total=7, checkboxes_done=0 WHERE id=?`, p1)
		s := newTestService(db, &stubRunner{}, &stubWt{})
		_, err := s.Start(p2)
		if !errors.Is(err, ErrDepsUnmet) {
			t.Fatalf("err = %v, want ErrDepsUnmet (a 0/7 'done' run is not a completed phase)", err)
		}
		var de *DepsUnmetError
		if !errors.As(err, &de) || len(de.Unmet) != 1 || de.Unmet[0] != 1 {
			t.Errorf("unmet = %+v, want [1]", de)
		}
	})

	t.Run("met via full checkboxes", func(t *testing.T) {
		db, _, p1, p2 := fixture(t)
		mustExec(t, db, `UPDATE epic_phases SET checkboxes_done=2 WHERE id=?`, p1)
		s := newTestService(db, &stubRunner{}, &stubWt{})
		if _, err := s.Start(p2); err != nil {
			t.Fatalf("Start: %v", err)
		}
	})

	// Same dependency as the incident case, fully ticked: completion is proven
	// by the criteria, with run_state playing no part either way.
	t.Run("met via full checkboxes regardless of run_state", func(t *testing.T) {
		db, _, p1, p2 := fixture(t)
		mustExec(t, db, `UPDATE epic_phases
			SET run_state='failed', checkboxes_total=7, checkboxes_done=7 WHERE id=?`, p1)
		s := newTestService(db, &stubRunner{}, &stubWt{})
		if _, err := s.Start(p2); err != nil {
			t.Fatalf("Start: %v", err)
		}
	})

	t.Run("zero checkboxes is not complete", func(t *testing.T) {
		db, _, p1, p2 := fixture(t)
		mustExec(t, db, `UPDATE epic_phases SET checkboxes_total=0, checkboxes_done=0 WHERE id=?`, p1)
		s := newTestService(db, &stubRunner{}, &stubWt{})
		if _, err := s.Start(p2); !errors.Is(err, ErrDepsUnmet) {
			t.Fatalf("err = %v, want ErrDepsUnmet (0/0 checkboxes must not satisfy)", err)
		}
	})

	t.Run("met via legacy done board task", func(t *testing.T) {
		db, _, p1, p2 := fixture(t)
		res, err := db.Exec(`INSERT INTO tasks (project_id, title, prompt, status, created_at,
			source, board_column) VALUES (1,'bt','p','done','2026-07-27T00:00:00Z','queue','done')`)
		if err != nil {
			t.Fatal(err)
		}
		btID, _ := res.LastInsertId()
		mustExec(t, db, `UPDATE epic_phases SET activated_board_task_id=? WHERE id=?`, btID, p1)
		s := newTestService(db, &stubRunner{}, &stubWt{})
		if _, err := s.Start(p2); err != nil {
			t.Fatalf("Start: %v", err)
		}
	})

	t.Run("met via legacy archived board task", func(t *testing.T) {
		db, _, p1, p2 := fixture(t)
		res, err := db.Exec(`INSERT INTO tasks (project_id, title, prompt, status, created_at,
			source, board_column, archived_at) VALUES (1,'bt','p','done','2026-07-27T00:00:00Z','queue','todo','2026-07-27T01:00:00Z')`)
		if err != nil {
			t.Fatal(err)
		}
		btID, _ := res.LastInsertId()
		mustExec(t, db, `UPDATE epic_phases SET activated_board_task_id=? WHERE id=?`, btID, p1)
		s := newTestService(db, &stubRunner{}, &stubWt{})
		if _, err := s.Start(p2); err != nil {
			t.Fatalf("Start: %v", err)
		}
	})

	t.Run("legacy board task not done blocks", func(t *testing.T) {
		db, _, p1, p2 := fixture(t)
		res, err := db.Exec(`INSERT INTO tasks (project_id, title, prompt, status, created_at,
			source, board_column) VALUES (1,'bt','p','running','2026-07-27T00:00:00Z','queue','in_progress')`)
		if err != nil {
			t.Fatal(err)
		}
		btID, _ := res.LastInsertId()
		mustExec(t, db, `UPDATE epic_phases SET activated_board_task_id=? WHERE id=?`, btID, p1)
		s := newTestService(db, &stubRunner{}, &stubWt{})
		if _, err := s.Start(p2); !errors.Is(err, ErrDepsUnmet) {
			t.Fatalf("err = %v, want ErrDepsUnmet", err)
		}
	})

	t.Run("dep seq with no sibling row blocks", func(t *testing.T) {
		db, _, _, p2 := fixture(t)
		mustExec(t, db, `UPDATE epic_phases SET depends_on='[7]' WHERE id=?`, p2)
		s := newTestService(db, &stubRunner{}, &stubWt{})
		if _, err := s.Start(p2); !errors.Is(err, ErrDepsUnmet) {
			t.Fatalf("err = %v, want ErrDepsUnmet", err)
		}
	})
}

func TestStart_DoubleStart_ErrRunning(t *testing.T) {
	db, _, p1, _ := fixture(t)
	r := &stubRunner{block: make(chan struct{})}
	s := NewService(db, r, &stubWt{}) // real goroutine — run stays in flight
	s.UUID = func() string { return "uuid-1" }

	if _, err := s.Start(p1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, func() bool {
		state, _, _, _ := phaseRow(t, db, p1)
		return state == "running"
	})
	if _, err := s.Start(p1); !errors.Is(err, ErrRunning) {
		t.Fatalf("second Start err = %v, want ErrRunning", err)
	}
	close(r.block)
	waitFor(t, func() bool {
		state, _, _, _ := phaseRow(t, db, p1)
		return state == "done"
	})
}

func TestStart_RunningRowWithoutSlot_ErrRunning(t *testing.T) {
	// A row stuck 'running' (e.g. concurrent daemon) blocks even with an empty
	// in-memory map.
	db, _, p1, _ := fixture(t)
	mustExec(t, db, `UPDATE epic_phases SET run_state='running' WHERE id=?`, p1)
	s := newTestService(db, &stubRunner{}, &stubWt{})
	if _, err := s.Start(p1); !errors.Is(err, ErrRunning) {
		t.Fatalf("err = %v, want ErrRunning", err)
	}
}

func TestStart_UnknownPhase(t *testing.T) {
	db, _, _, _ := fixture(t)
	s := newTestService(db, &stubRunner{}, &stubWt{})
	if _, err := s.Start(9999); !errors.Is(err, ErrPhaseNotFound) {
		t.Fatalf("err = %v, want ErrPhaseNotFound", err)
	}
}

func TestStart_NoDoc(t *testing.T) {
	db, _, p1, _ := fixture(t)
	mustExec(t, db, `UPDATE epic_phases SET doc_path='/nope/missing.md' WHERE id=?`, p1)
	s := newTestService(db, &stubRunner{}, &stubWt{})
	if _, err := s.Start(p1); !errors.Is(err, ErrNoDoc) {
		t.Fatalf("err = %v, want ErrNoDoc", err)
	}
}

func TestStart_NoProjectPath(t *testing.T) {
	db, _, p1, _ := fixture(t)
	mustExec(t, db, `UPDATE projects SET path='' WHERE id=1`)
	s := newTestService(db, &stubRunner{}, &stubWt{})
	if _, err := s.Start(p1); !errors.Is(err, ErrNoPath) {
		t.Fatalf("err = %v, want ErrNoPath", err)
	}
}

func TestStart_AcquireFailure_StampsFailed(t *testing.T) {
	db, _, p1, _ := fixture(t)
	wt := &stubWt{acquireErr: errors.New("branch busy")}
	s := newTestService(db, &stubRunner{}, wt)
	if _, err := s.Start(p1); err == nil || !strings.Contains(err.Error(), "branch busy") {
		t.Fatalf("err = %v, want the acquire error surfaced", err)
	}
	// Slot released — a retry is admitted.
	wt.acquireErr = nil
	if _, err := s.Start(p1); err != nil {
		t.Fatalf("retry after acquire failure: %v", err)
	}
}

func TestCancel(t *testing.T) {
	db, _, p1, _ := fixture(t)
	r := &stubRunner{block: make(chan struct{})}
	wt := &stubWt{}
	s := NewService(db, r, wt) // real goroutine
	s.UUID = func() string { return "uuid-1" }

	if _, err := s.Start(p1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, func() bool {
		state, _, _, _ := phaseRow(t, db, p1)
		return state == "running"
	})
	if !s.Cancel(p1) {
		t.Fatal("Cancel = false, want true for an in-flight run")
	}
	waitFor(t, func() bool {
		state, _, _, _ := phaseRow(t, db, p1)
		return state == "failed"
	})
	_, _, _, runErr := phaseRow(t, db, p1)
	if runErr.String != "cancelled" {
		t.Errorf("run_error = %q, want cancelled", runErr.String)
	}
	waitFor(t, func() bool { return wt.removedCount() == 1 })

	if s.Cancel(p1) {
		t.Error("Cancel with nothing running = true, want false")
	}
}

func TestHealStale(t *testing.T) {
	db, _, p1, p2 := fixture(t)
	mustExec(t, db, `UPDATE epic_phases SET run_state='running' WHERE id=?`, p1)
	mustExec(t, db, `UPDATE epic_phases SET run_state='done' WHERE id=?`, p2)
	s := newTestService(db, &stubRunner{}, &stubWt{})

	if err := s.HealStale(); err != nil {
		t.Fatalf("HealStale: %v", err)
	}
	state, _, _, runErr := phaseRow(t, db, p1)
	if state != "failed" || runErr.String != "daemon restart" {
		t.Errorf("healed row: state=%q run_error=%q", state, runErr.String)
	}
	// A crash-orphaned row is a terminal transition too — it gets an end stamp.
	if _, endedAt := phaseOutcome(t, db, p1); !endedAt.Valid || endedAt.String == "" {
		t.Errorf("healed row run_ended_at = %q (valid=%v), want a timestamp", endedAt.String, endedAt.Valid)
	}
	state, _, _, _ = phaseRow(t, db, p2)
	if state != "done" {
		t.Errorf("done row touched by heal: state=%q", state)
	}
	if _, endedAt := phaseOutcome(t, db, p2); endedAt.Valid {
		t.Errorf("done row run_ended_at = %q, want untouched by heal", endedAt.String)
	}
}

func TestNewUUID(t *testing.T) {
	a, b := newUUID(), newUUID()
	if a == b {
		t.Error("newUUID returned identical values")
	}
	if len(a) != 36 || a[14] != '4' {
		t.Errorf("newUUID = %q, not a v4 uuid", a)
	}
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
