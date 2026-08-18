package verify

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/runcore"
)

// ── keys ──

func TestTargetKeys(t *testing.T) {
	if got := TaskKey(42); got != "task:42" {
		t.Errorf("TaskKey(42) = %q, want task:42", got)
	}
	if got := PhaseKey(7); got != "phase:7" {
		t.Errorf("PhaseKey(7) = %q, want phase:7", got)
	}
}

func TestSplitKey(t *testing.T) {
	cases := []struct {
		key   string
		kind  string
		id    int64
		ok    bool
		about string
	}{
		{"task:42", KindTask, 42, true, "a board card"},
		{"phase:7", KindPhase, 7, true, "a plan phase"},
		{"", "", 0, false, "empty"},
		{"42", "", 0, false, "no kind prefix"},
		{"epic:3", "", 0, false, "a kind this package does not grade"},
		{"task:abc", "", 0, false, "unparseable id"},
		{"task:0", "", 0, false, "row ids start at 1 — 0 is corruption, not a row"},
		{"task:-3", "", 0, false, "negative id"},
	}
	for _, c := range cases {
		kind, id, ok := SplitKey(c.key)
		if ok != c.ok || kind != c.kind || id != c.id {
			t.Errorf("SplitKey(%q) = (%q, %d, %v), want (%q, %d, %v) — %s",
				c.key, kind, id, ok, c.kind, c.id, c.ok, c.about)
		}
	}
}

func TestStrictnessFromMode(t *testing.T) {
	cases := map[string]Strictness{
		"strict":  StrictnessStrict,
		"STRICT":  StrictnessStrict,
		"normal":  StrictnessNormal,
		" normal": StrictnessNormal,
		"off":     strictnessOff,
		"":        strictnessOff,
		"sure":    strictnessOff, // unrecognized ⇒ off, never a grader nobody asked for
	}
	for mode, want := range cases {
		if got := StrictnessFromMode(mode); got != want {
			t.Errorf("StrictnessFromMode(%q) = %q, want %q", mode, got, want)
		}
	}
}

// ── phase targets ──

// insertPhase seeds an epic (workspace task) with one phase row and returns both
// ids. The phase is what a Target stamps; the epic is what a plan_updated nudge
// names.
func insertPhase(t *testing.T, db *sql.DB, verifyMode string) (phaseID, epicID int64) {
	t.Helper()
	res, err := db.Exec(`
		INSERT INTO tasks(project_id, title, prompt, priority, status, created_at,
		                  source, origin, external_id, board_column, file_scope,
		                  dependencies)
		VALUES(1, 'epic', 'plan', 5, 'running', '2026-08-01T00:00:00.000Z',
		       'workspace', 'manual', 'W-epic1', 'todo', '[]', '[]')`)
	if err != nil {
		t.Fatalf("insert epic: %v", err)
	}
	epicID, _ = res.LastInsertId()
	res, err = db.Exec(`
		INSERT INTO epic_phases(workspace_task_id, seq, name, doc_path, depends_on,
		                        checkboxes_done, checkboxes_total, run_state, verify_mode)
		VALUES(?, 1, 'Verify for phases', '/plan/phase-5.md', '[]', 0, 3, 'done', ?)`,
		epicID, verifyMode)
	if err != nil {
		t.Fatalf("insert phase: %v", err)
	}
	phaseID, _ = res.LastInsertId()
	return phaseID, epicID
}

func phaseVerdict(t *testing.T, db *sql.DB, phaseID int64) (verdict, detail string) {
	t.Helper()
	var v, d sql.NullString
	if err := db.QueryRow(
		`SELECT verify_verdict, verify_detail FROM epic_phases WHERE id=?`, phaseID).Scan(&v, &d); err != nil {
		t.Fatalf("read phase verdict: %v", err)
	}
	return v.String, d.String
}

func phaseReq(phaseID, epicID int64, mode string) runcore.PhaseVerifyRequest {
	return runcore.PhaseVerifyRequest{
		PhaseID:         phaseID,
		WorkspaceTaskID: epicID,
		Mode:            mode,
		WorktreePath:    "/wt/p/phase-1",
		Branch:          "swarm/phase-1",
		StartPoint:      "phasebase00",
		Title:           "Verify for phases",
		Prompt:          "- [ ] the verdict is an input, not a status",
		ProjectPath:     "/repo/p",
	}
}

func TestVerifyPhase_StampsThePhaseRow(t *testing.T) {
	db := testDB(t)
	runner := &stubRunner{out: "all good\nVERDICT: PASS"}
	s := newTestService(t, db, runner, stubTrees{hash: "phasetree"})
	phaseID, epicID := insertPhase(t, db, "normal")

	var nudged []int64
	s.NotifyPlan = func(id int64) { nudged = append(nudged, id) }
	s.Notify = func(int64) { t.Error("a phase verdict must not emit task_updated — the board shows no phase") }

	if err := s.VerifyPhase(context.Background(), phaseReq(phaseID, epicID, "normal")); err != nil {
		t.Fatalf("VerifyPhase: %v", err)
	}

	v, d := phaseVerdict(t, db, phaseID)
	if v != string(VerdictPass) {
		t.Errorf("epic_phases.verify_verdict = %q, want pass", v)
	}
	if !strings.Contains(d, "all good") {
		t.Errorf("verify_detail = %q, want the verifier's reasons", d)
	}
	if len(nudged) != 1 || nudged[0] != epicID {
		t.Errorf("plan_updated nudges = %v, want [%d]", nudged, epicID)
	}
	// The run row carries the phase key and NO task_id: the FK exists for board
	// cards, and a phase id parked there would point at an unrelated task.
	var key string
	var taskID sql.NullInt64
	if err := db.QueryRow(
		`SELECT target_key, task_id FROM verification_runs WHERE status='pass'`).Scan(&key, &taskID); err != nil {
		t.Fatalf("read run row: %v", err)
	}
	if key != PhaseKey(phaseID) {
		t.Errorf("target_key = %q, want %q", key, PhaseKey(phaseID))
	}
	if taskID.Valid {
		t.Errorf("task_id = %d, want NULL for a phase target", taskID.Int64)
	}
	// The prompt grades the real interval, base...HEAD — not the branch against
	// itself, which is always empty.
	if p := runner.lastPrompt(); !strings.Contains(p, "phasebase00") {
		t.Errorf("prompt does not name the start point as the diff base:\n%s", p)
	}
}

func TestVerifyPhase_FailStampsButSpawnsNoFixTask(t *testing.T) {
	db := testDB(t)
	s := newTestService(t, db, &stubRunner{out: "criterion 2 is not met\nVERDICT: FAIL"},
		stubTrees{hash: "phasetree"})
	phaseID, epicID := insertPhase(t, db, "strict")

	var tasksBefore int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&tasksBefore); err != nil {
		t.Fatal(err)
	}

	if err := s.VerifyPhase(context.Background(), phaseReq(phaseID, epicID, "strict")); err != nil {
		t.Fatalf("VerifyPhase: %v", err)
	}

	v, d := phaseVerdict(t, db, phaseID)
	if v != string(VerdictFail) {
		t.Errorf("verify_verdict = %q, want fail", v)
	}
	if !strings.Contains(d, "criterion 2") {
		t.Errorf("verify_detail = %q, want the failing reasons", d)
	}
	// The fix-task chain is board-only: a failed phase verify stamps and stops.
	var tasksAfter, fixes int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&tasksAfter); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE origin='verify-fix'`).Scan(&fixes); err != nil {
		t.Fatal(err)
	}
	if tasksAfter != tasksBefore || fixes != 0 {
		t.Errorf("phase fail created %d task(s) (%d fix) — the fix chain must stay board-only",
			tasksAfter-tasksBefore, fixes)
	}
}

func TestVerifyPhase_StrictTightensTheBar(t *testing.T) {
	db := testDB(t)
	runner := &stubRunner{out: "VERDICT: PASS"}
	s := newTestService(t, db, runner, stubTrees{hash: "h"})
	phaseID, epicID := insertPhase(t, db, "strict")

	if err := s.VerifyPhase(context.Background(), phaseReq(phaseID, epicID, "strict")); err != nil {
		t.Fatalf("VerifyPhase: %v", err)
	}
	if p := runner.lastPrompt(); !strings.Contains(p, "STRICT REVIEW") {
		t.Errorf("strict mode did not tighten the prompt:\n%s", p)
	}
}

func TestVerifyPhase_OffGradesNothing(t *testing.T) {
	db := testDB(t)
	runner := &stubRunner{out: "VERDICT: FAIL"}
	s := newTestService(t, db, runner, stubTrees{hash: "h"})
	phaseID, epicID := insertPhase(t, db, "off")

	// `off` is the default for every plan that does not ask, so this is the path that
	// keeps existing plans behaving exactly as before: no run row, no verdict.
	for _, mode := range []string{"off", "", "nonsense"} {
		if err := s.VerifyPhase(context.Background(), phaseReq(phaseID, epicID, mode)); err != nil {
			t.Fatalf("VerifyPhase(mode=%q): %v", mode, err)
		}
	}
	if runner.count() != 0 {
		t.Errorf("verifier spawned %d time(s) for a phase that never opted in", runner.count())
	}
	if v, _ := phaseVerdict(t, db, phaseID); v != "" {
		t.Errorf("verify_verdict = %q, want unset", v)
	}
	var runs int
	if err := db.QueryRow(`SELECT COUNT(*) FROM verification_runs`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 0 {
		t.Errorf("%d verification_runs row(s) for an opted-out phase, want 0", runs)
	}
}

func TestVerifyPhase_SingleFlightIsPerTarget(t *testing.T) {
	db := testDB(t)
	s := newTestService(t, db, &stubRunner{out: "VERDICT: PASS"}, stubTrees{hash: "h"})
	phaseID, epicID := insertPhase(t, db, "normal")
	taskID := insertTask(t, db, taskOpts{})

	// An in-flight run for THIS phase bounces a second one...
	if _, err := db.Exec(
		`INSERT INTO verification_runs(target_key, status, started_at) VALUES(?, 'running', ?)`,
		PhaseKey(phaseID), s.ts()); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyPhase(context.Background(), phaseReq(phaseID, epicID, "normal")); err != ErrAlreadyRunning {
		t.Errorf("VerifyPhase = %v, want ErrAlreadyRunning", err)
	}
	// ...while a board card with the same numeric id is a DIFFERENT target and is
	// unaffected: the key namespaces them apart.
	if err := s.VerifyTask(context.Background(), taskID); err != nil {
		t.Errorf("VerifyTask blocked by a phase's in-flight run: %v", err)
	}
}

func TestVerifyPhase_NoWorktreeIsNotAFailingPhase(t *testing.T) {
	db := testDB(t)
	s := newTestService(t, db, &stubRunner{out: "VERDICT: PASS"}, stubTrees{hash: "h"})
	phaseID, epicID := insertPhase(t, db, "normal")

	req := phaseReq(phaseID, epicID, "normal")
	req.WorktreePath = "" // already reclaimed — nothing to grade
	if err := s.VerifyPhase(context.Background(), req); err != ErrNoWorktree {
		t.Errorf("VerifyPhase = %v, want ErrNoWorktree", err)
	}
	if v, _ := phaseVerdict(t, db, phaseID); v != "" {
		t.Errorf("verify_verdict = %q — an absent worktree must not be graded as anything", v)
	}
}

func TestVerifyPhase_CacheHitDoesNotSpawn(t *testing.T) {
	db := testDB(t)
	runner := &stubRunner{out: "VERDICT: PASS"}
	s := newTestService(t, db, runner, stubTrees{hash: "sametree"})
	phaseID, epicID := insertPhase(t, db, "normal")

	if err := s.VerifyPhase(context.Background(), phaseReq(phaseID, epicID, "normal")); err != nil {
		t.Fatalf("first VerifyPhase: %v", err)
	}
	if err := s.VerifyPhase(context.Background(), phaseReq(phaseID, epicID, "normal")); err != nil {
		t.Fatalf("second VerifyPhase: %v", err)
	}
	if runner.count() != 1 {
		t.Errorf("verifier spawned %d times for an unchanged tree, want 1", runner.count())
	}
	if _, d := phaseVerdict(t, db, phaseID); !strings.Contains(d, "cache") {
		t.Errorf("verify_detail = %q, want the cache note", d)
	}
	// The memo is keyed by target, so a card with the same tree is NOT pre-answered.
	var cached string
	if err := db.QueryRow(
		`SELECT target_key FROM verification_cache WHERE tree_hash='sametree'`).Scan(&cached); err != nil {
		t.Fatal(err)
	}
	if cached != PhaseKey(phaseID) {
		t.Errorf("cache target_key = %q, want %q", cached, PhaseKey(phaseID))
	}
}

// ── out-of-band stamping: the reaper and the startup heal have no Target ──

func TestReapAndHeal_RoutePhaseVerdictsToEpicPhases(t *testing.T) {
	db := testDB(t)
	s := newTestService(t, db, &stubRunner{}, stubTrees{hash: "h"})
	phaseID, epicID := insertPhase(t, db, "normal")

	var nudged []int64
	s.NotifyPlan = func(id int64) { nudged = append(nudged, id) }

	// A stalled phase run, older than the window.
	stale := s.clock().Add(-3 * s.Cfg.StaleAfter).UTC().Format(tsFormat)
	if _, err := db.Exec(
		`INSERT INTO verification_runs(target_key, status, started_at) VALUES(?, 'running', ?)`,
		PhaseKey(phaseID), stale); err != nil {
		t.Fatal(err)
	}
	n, err := s.Reap()
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}
	if n != 1 {
		t.Fatalf("reaped %d, want 1", n)
	}
	v, d := phaseVerdict(t, db, phaseID)
	if v != string(VerdictInconclusive) {
		t.Errorf("reaped phase verdict = %q, want inconclusive", v)
	}
	if !strings.Contains(d, "reaped") {
		t.Errorf("reaped phase detail = %q, want the reap note", d)
	}
	if len(nudged) != 1 || nudged[0] != epicID {
		t.Errorf("reap nudges = %v, want [%d] — the epic resolved from the phase", nudged, epicID)
	}

	// HealStale must NOT overwrite a verdict a later run already reached.
	if _, err := db.Exec(
		`UPDATE epic_phases SET verify_verdict='pass', verify_detail='graded later' WHERE id=?`,
		phaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO verification_runs(target_key, status, started_at) VALUES(?, 'running', ?)`,
		PhaseKey(phaseID), s.ts()); err != nil {
		t.Fatal(err)
	}
	if err := s.HealStale(); err != nil {
		t.Fatalf("HealStale: %v", err)
	}
	if v, d := phaseVerdict(t, db, phaseID); v != "pass" || d != "graded later" {
		t.Errorf("heal overwrote a settled verdict: (%q, %q)", v, d)
	}
}

func TestStampByKey_UnroutableKeyIsSkipped(t *testing.T) {
	db := testDB(t)
	s := newTestService(t, db, &stubRunner{}, stubTrees{hash: "h"})
	taskID := insertTask(t, db, taskOpts{})

	// A run row whose key names a kind this build does not grade must not fall back
	// to "task" — that would stamp a verdict onto an unrelated row.
	if _, err := db.Exec(
		`INSERT INTO verification_runs(target_key, status, started_at) VALUES('epic:1', 'running', ?)`,
		s.clock().Add(-3*s.Cfg.StaleAfter).UTC().Format(tsFormat)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reap(); err != nil {
		t.Fatalf("Reap: %v", err)
	}
	var v sql.NullString
	if err := db.QueryRow(`SELECT verify_verdict FROM tasks WHERE id=?`, taskID).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v.Valid {
		t.Errorf("task %d was stamped %q from an unroutable key", taskID, v.String)
	}
	// The run row is still finalized — a stuck 'running' row must never survive a reap.
	var status string
	if err := db.QueryRow(
		`SELECT status FROM verification_runs WHERE target_key='epic:1'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "error" {
		t.Errorf("run status = %q, want error", status)
	}
}

func TestVerifyTarget_RefusesATargetWithNowhereToStamp(t *testing.T) {
	db := testDB(t)
	s := newTestService(t, db, &stubRunner{out: "VERDICT: PASS"}, stubTrees{hash: "h"})

	err := s.VerifyTarget(context.Background(), Target{
		Key:          PhaseKey(1),
		WorktreePath: "/wt/p/phase-1",
		Strictness:   StrictnessNormal,
	})
	if err == nil || !strings.Contains(err.Error(), "no Stamp") {
		t.Errorf("VerifyTarget = %v, want a refusal naming the missing Stamp", err)
	}
	var runs int
	if err := db.QueryRow(`SELECT COUNT(*) FROM verification_runs`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 0 {
		t.Errorf("%d run row(s) opened for an unstampable target, want 0", runs)
	}
}
