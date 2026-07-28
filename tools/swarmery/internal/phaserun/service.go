// Package phaserun executes ONE plan phase directly from its phase doc
// (interactive planning v2 phase 5): a headless `claude -p` run in an isolated
// git worktree, state tracked on epic_phases (run_state / run_session_uuid /
// run_started_at / run_error). No `tasks` row, no board involvement — progress
// (checkbox ticks) keeps flowing through wsingest as the executor edits the
// phase docs in the private workspace.
//
// Modeled on internal/planning's service (single-flight map + spawn seam +
// Notify) and reusing internal/dispatch's WorktreeManager + internal/worktree
// mechanics — nothing is duplicated.
package phaserun

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/dispatch"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/worktree"
)

// Sentinel errors mapped to HTTP statuses by the api layer.
var (
	// ErrPhaseNotFound: no epic_phases row for the given id (404).
	ErrPhaseNotFound = errors.New("phase not found")
	// ErrRunning: this phase already has a run in flight (409).
	ErrRunning = errors.New("a run is already active for this phase")
	// ErrDepsUnmet: one or more dependsOn phases are not complete (409).
	// Returned as a *DepsUnmetError, which errors.Is-matches this sentinel.
	ErrDepsUnmet = errors.New("phase has unmet dependencies")
	// ErrNoDoc: the phase doc could not be read (409).
	ErrNoDoc = errors.New("phase doc is unreadable")
	// ErrNoPath: the project has no filesystem path to run in (409).
	ErrNoPath = errors.New("project has no known path to run in")
)

// DepsUnmetError carries WHICH dependency seqs are unmet so the api's 409 body
// can name them. errors.Is(err, ErrDepsUnmet) matches.
type DepsUnmetError struct {
	Unmet []int // dependency seq numbers not yet satisfied
}

func (e *DepsUnmetError) Error() string {
	return fmt.Sprintf("phase has unmet dependencies: phases %v", e.Unmet)
}

func (e *DepsUnmetError) Is(target error) bool { return target == ErrDepsUnmet }

// run is one in-flight phase run: its cancel (aborts the child claude) and the
// pre-generated session uuid.
type run struct {
	cancel context.CancelFunc
	uuid   string
}

// Service owns the phase-run lifecycle: gate checks, worktree acquisition,
// spawn, exit stamping, and startup heal. Notify (wired to the api layer's
// plan_updated publisher) is keyed by the WORKSPACE task id so the Plans page
// refetches on run edges.
type Service struct {
	DB   *sql.DB
	Wt   dispatch.WorktreeManager // shared worktree mechanics (dispatch's seam)
	Run  Runner
	UUID func() string    // session-uuid generator (test seam; default newUUID)
	now  func() time.Time // clock (test seam; default time.Now)
	Go   func(func())     // async-spawn seam (nil ⇒ real `go`); mirrors planning.Go
	// Notify emits plan_updated for the phase's workspace task at run edges.
	// nil ⇒ no live nudge (guarded).
	Notify func(taskID int64)

	mu     sync.Mutex
	active map[int64]run // epic_phases.id → in-flight run
}

// NewService builds a phase-run service. The caller wires DB + Run
// (ClaudeRunner) + Wt (the shared worktree.Manager); UUID/now/Go default to
// production impls.
func NewService(db *sql.DB, r Runner, wt dispatch.WorktreeManager) *Service {
	return &Service{
		DB:     db,
		Run:    r,
		Wt:     wt,
		UUID:   newUUID,
		now:    time.Now,
		active: make(map[int64]run),
	}
}

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Service) ts() string { return s.clock().UTC().Format(time.RFC3339) }

func (s *Service) spawn(fn func()) {
	wrapped := func() {
		// A panic in a run goroutine must never take the daemon down — recover +
		// log (mirrors planning.spawn / dispatch.spawn).
		defer func() {
			if r := recover(); r != nil {
				log.Printf("error: phaserun: goroutine panic recovered: %v", r)
			}
		}()
		fn()
	}
	if s.Go != nil {
		s.Go(wrapped)
		return
	}
	go wrapped()
}

func (s *Service) notify(taskID int64) {
	if s.Notify != nil {
		s.Notify(taskID)
	}
}

// phaseInfo is the Start admission read: the phase joined to its epic task and
// project.
type phaseInfo struct {
	WorkspaceTaskID int64
	Seq             int
	DocPath         string
	DependsOn       []int
	RunState        string
	ProjectPath     string
	ProjectSlug     string
}

// Start admits a run for a phase: gates (single-flight, deps, doc, path), then
// acquires a worktree, stamps run_state='running', and spawns the headless
// executor. Returns the pre-generated session uuid so the caller answers 202
// immediately. The run's own goroutine owns exit stamping, worktree removal
// (branch kept), and slot release.
func (s *Service) Start(phaseID int64) (sessionUUID string, err error) {
	info, err := s.loadPhase(phaseID)
	if err != nil {
		return "", err
	}
	if info.RunState == "running" {
		return "", ErrRunning
	}
	if info.ProjectPath == "" {
		return "", ErrNoPath
	}
	if unmet, err := s.unmetDeps(info); err != nil {
		return "", err
	} else if len(unmet) > 0 {
		return "", &DepsUnmetError{Unmet: unmet}
	}
	doc, err := os.ReadFile(info.DocPath)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrNoDoc, info.DocPath)
	}

	// Single-flight slot BEFORE the (slow, git-touching) Acquire so a concurrent
	// Start cannot double-spawn; released on any admission failure below.
	s.mu.Lock()
	if _, busy := s.active[phaseID]; busy {
		s.mu.Unlock()
		return "", ErrRunning
	}
	uuid := s.UUID()
	ctx, cancel := context.WithCancel(context.Background())
	s.active[phaseID] = run{cancel: cancel, uuid: uuid}
	s.mu.Unlock()

	release := func() {
		cancel()
		s.mu.Lock()
		delete(s.active, phaseID)
		s.mu.Unlock()
	}

	acq, err := s.Wt.Acquire(info.ProjectPath, info.ProjectSlug, "phase-"+strconv.FormatInt(phaseID, 10))
	if err != nil {
		release()
		return "", fmt.Errorf("worktree acquire: %w", err)
	}

	if _, err := s.DB.Exec(`
		UPDATE epic_phases
		   SET run_state='running', run_session_uuid=?, run_started_at=?, run_error=NULL
		 WHERE id=?`, uuid, s.ts(), phaseID); err != nil {
		release()
		s.removeWorktree(info.ProjectPath, acq)
		return "", err
	}

	log.Printf("phaserun: start phase=%d task=%d uuid=%s worktree=%q", phaseID, info.WorkspaceTaskID, uuid, acq.Path)
	s.notify(info.WorkspaceTaskID)

	prompt := BuildPrompt(info.DocPath, filepath.Base(info.DocPath), string(doc))
	spec := RunSpec{Prompt: prompt, SessionUUID: uuid, Cwd: acq.Path}
	s.spawn(func() { s.runAndHandle(ctx, cancel, phaseID, info, acq, spec) })
	return uuid, nil
}

// runAndHandle executes the run to completion, stamps the exit state, removes
// the worktree (branch kept), and always releases the slot.
func (s *Service) runAndHandle(ctx context.Context, cancel context.CancelFunc, phaseID int64, info phaseInfo, acq worktree.Acquired, spec RunSpec) {
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.active, phaseID)
		s.mu.Unlock()
		s.removeWorktree(info.ProjectPath, acq)
		s.notify(info.WorkspaceTaskID)
	}()

	res, err := s.Run.Start(ctx, spec)
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		// Cancel() beat the exit — whatever the child reported, the outcome is a
		// user cancellation.
		log.Printf("phaserun: phase=%d uuid=%s cancelled", phaseID, spec.SessionUUID)
		s.stamp(phaseID, "failed", "cancelled")
	case err != nil:
		log.Printf("error: phaserun: phase=%d uuid=%s could not start: %v", phaseID, spec.SessionUUID, err)
		s.stamp(phaseID, "failed", err.Error())
	case res.TimedOut:
		log.Printf("warning: phaserun: phase=%d uuid=%s timed out", phaseID, spec.SessionUUID)
		s.stamp(phaseID, "failed", "timeout")
	case res.ExitCode != 0:
		log.Printf("warning: phaserun: phase=%d uuid=%s exited %d: %s", phaseID, spec.SessionUUID, res.ExitCode, res.Stderr)
		msg := res.Stderr
		if msg == "" {
			msg = fmt.Sprintf("exit %d", res.ExitCode)
		}
		s.stamp(phaseID, "failed", msg)
	default:
		log.Printf("phaserun: phase=%d uuid=%s completed in %s", phaseID, spec.SessionUUID, res.Duration)
		s.stamp(phaseID, "done", "")
	}
}

// stamp writes the terminal run state; runError "" ⇒ NULL.
func (s *Service) stamp(phaseID int64, state, runError string) {
	var re any
	if runError != "" {
		re = runError
	}
	if _, err := s.DB.Exec(
		`UPDATE epic_phases SET run_state=?, run_error=? WHERE id=?`,
		state, re, phaseID); err != nil {
		log.Printf("error: phaserun: stamp phase=%d state=%s: %v", phaseID, state, err)
	}
}

// removeWorktree best-effort removes the run's worktree, KEEPING the branch
// (its commits stay reachable for the user — mirrors dispatch.removeWorktree).
func (s *Service) removeWorktree(repoRoot string, acq worktree.Acquired) {
	if s.Wt == nil {
		return
	}
	if err := s.Wt.Remove(repoRoot, acq, true /* keepBranch */); err != nil {
		log.Printf("warning: phaserun: remove worktree %s: %v", acq.Path, err)
	}
}

// Cancel aborts an in-flight run: the context cancel kills the child claude,
// and the run goroutine's exit path stamps failed/cancelled. Returns whether a
// run was actually in flight.
func (s *Service) Cancel(phaseID int64) bool {
	s.mu.Lock()
	r, ok := s.active[phaseID]
	s.mu.Unlock()
	if ok {
		r.cancel()
	}
	return ok
}

// HealStale fails any epic_phases row left 'running' by a crashed/restarted
// daemon (there can be no live run in THIS process — we just started). Mirrors
// dispatch.HealStale's posture. Called from cmd/swarmery before serving.
func (s *Service) HealStale() error {
	res, err := s.DB.Exec(`
		UPDATE epic_phases SET run_state='failed', run_error='daemon restart'
		 WHERE run_state='running'`)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("swarmery phaserun: healed %d orphaned running phase(s) to failed", n)
	}
	return nil
}

// loadPhase reads the phase + its epic task + project for admission.
func (s *Service) loadPhase(phaseID int64) (phaseInfo, error) {
	var (
		info     phaseInfo
		depsJSON string
		path     sql.NullString
	)
	err := s.DB.QueryRow(`
		SELECT e.workspace_task_id, e.seq, e.doc_path, e.depends_on, e.run_state,
		       p.path, p.slug
		  FROM epic_phases e
		  JOIN tasks t ON t.id = e.workspace_task_id
		  JOIN projects p ON p.id = t.project_id
		 WHERE e.id = ?`, phaseID).Scan(
		&info.WorkspaceTaskID, &info.Seq, &info.DocPath, &depsJSON,
		&info.RunState, &path, &info.ProjectSlug)
	if errors.Is(err, sql.ErrNoRows) {
		return info, ErrPhaseNotFound
	}
	if err != nil {
		return info, err
	}
	info.ProjectPath = path.String
	if err := json.Unmarshal([]byte(depsJSON), &info.DependsOn); err != nil {
		info.DependsOn = nil // garbage depends_on ⇒ no gate (same posture as epics.go decodeIntList)
	}
	return info, nil
}

// unmetDeps returns the dependency seqs of info that are NOT yet satisfied. A
// dep seq is satisfied when a sibling phase row with that seq is complete via
// any of the three paths: run_state='done'; all checkboxes ticked (total>0);
// or a legacy activated board task that is done/archived.
func (s *Service) unmetDeps(info phaseInfo) ([]int, error) {
	var unmet []int
	for _, dep := range info.DependsOn {
		ok, err := s.depSatisfied(info.WorkspaceTaskID, dep)
		if err != nil {
			return nil, err
		}
		if !ok {
			unmet = append(unmet, dep)
		}
	}
	return unmet, nil
}

func (s *Service) depSatisfied(taskID int64, seq int) (bool, error) {
	rows, err := s.DB.Query(`
		SELECT e.run_state, e.checkboxes_done, e.checkboxes_total,
		       COALESCE(bt.board_column,''), bt.archived_at IS NOT NULL
		  FROM epic_phases e
		  LEFT JOIN tasks bt ON bt.id = e.activated_board_task_id
		 WHERE e.workspace_task_id = ? AND e.seq = ?`, taskID, seq)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			runState, boardCol string
			done, total        int
			archived           bool
		)
		if err := rows.Scan(&runState, &done, &total, &boardCol, &archived); err != nil {
			return false, err
		}
		if runState == "done" ||
			(total > 0 && done == total) ||
			boardCol == "done" || archived {
			return true, nil
		}
	}
	return false, rows.Err()
}
