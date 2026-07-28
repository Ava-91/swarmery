// Package planrun executes a WHOLE plan by handing it to one agent: a single
// headless `claude -p --agent <a>` run in one isolated git worktree, state
// tracked on plan_runs (migration 0040). Where internal/phaserun runs ONE phase
// per session, this hands the plan README plus a manifest of phase docs to a
// single orchestrating session that works the phases in order itself — so the
// phases share context instead of each starting cold.
//
// Progress is NOT re-invented: the executor ticks each phase doc's checkboxes as
// it goes, wsingest rescans, and the Plans page's existing per-phase rollup
// moves. Nothing here writes epic_phases.run_state — those belong to phase runs,
// and claiming them would make the two mechanisms lie about each other.
//
// Modeled on internal/phaserun (single-flight map + spawn seam + Notify) and
// reusing internal/dispatch's WorktreeManager — nothing is duplicated.
package planrun

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/dispatch"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/worktree"
)

// Sentinel errors mapped to HTTP statuses by the api layer.
var (
	// ErrPlanNotFound: no workspace task with phases for the given id (404).
	ErrPlanNotFound = errors.New("plan not found")
	// ErrRunning: this plan already has a run in flight (409).
	ErrRunning = errors.New("a run is already active for this plan")
	// ErrPhaseRunning: a per-phase run is in flight for this plan; the two would
	// fight over the same phase docs (409).
	ErrPhaseRunning = errors.New("a phase run is active for this plan")
	// ErrNotActive: the plan is paused or archived (409).
	ErrNotActive = errors.New("plan is not active")
	// ErrNoPhases: the plan has no phases to execute (409).
	ErrNoPhases = errors.New("plan has no phases")
	// ErrNoDoc: the plan README could not be read (409).
	ErrNoDoc = errors.New("plan README is unreadable")
	// ErrNoPath: the project has no filesystem path to run in (409).
	ErrNoPath = errors.New("project has no known path to run in")
	// ErrComplete: every phase is already done — nothing to run (409).
	ErrComplete = errors.New("every phase of this plan is already done")
)

// run is one in-flight plan run: its cancel (aborts the child claude) and the
// pre-generated session uuid.
type run struct {
	cancel context.CancelFunc
	uuid   string
}

// Service owns the plan-run lifecycle: gate checks, worktree acquisition,
// spawn, exit stamping, and startup heal. Notify (wired to the api layer's
// plan_updated publisher) is keyed by the workspace task id so the Plans page
// refetches on run edges.
type Service struct {
	DB   *sql.DB
	Wt   dispatch.WorktreeManager // shared worktree mechanics (dispatch's seam)
	Run  Runner
	UUID func() string    // session-uuid generator (test seam; default newUUID)
	now  func() time.Time // clock (test seam; default time.Now)
	Go   func(func())     // async-spawn seam (nil ⇒ real `go`); mirrors phaserun.Go
	// Notify emits plan_updated for the plan at run edges. nil ⇒ no live nudge.
	Notify func(taskID int64)

	mu     sync.Mutex
	active map[int64]run // workspace task id → in-flight run
}

// NewService builds a plan-run service. The caller wires DB + Run
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
		// log (mirrors phaserun.spawn / dispatch.spawn).
		defer func() {
			if r := recover(); r != nil {
				log.Printf("error: planrun: goroutine panic recovered: %v", r)
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

// planInfo is the Start admission read: the plan task joined to its project.
type planInfo struct {
	TaskID      int64
	PlanDir     string
	Status      string
	Archived    bool
	ProjectPath string
	ProjectSlug string
	Phases      []Phase
}

// Start admits a run for a whole plan: gates (single-flight, lifecycle, phase
// runs, phases, README, path), then acquires a worktree, stamps
// run_state='running', and spawns the headless executor. `agent` names the
// agent the plan is handed to ("" ⇒ DefaultAgent()); `mode` is how it executes
// the phases ("" / unknown ⇒ ModeAuto, i.e. the skill's own triage). Returns the
// pre-generated session uuid so the caller answers 202 immediately. The run's
// own goroutine owns exit stamping, worktree removal (branch kept), and slot
// release.
func (s *Service) Start(taskID int64, agent, mode string) (sessionUUID string, err error) {
	info, err := s.loadPlan(taskID)
	if err != nil {
		return "", err
	}
	if info.Archived || info.Status == "paused" {
		return "", ErrNotActive
	}
	if info.ProjectPath == "" {
		return "", ErrNoPath
	}
	if len(info.Phases) == 0 {
		return "", ErrNoPhases
	}
	if allComplete(info.Phases) {
		return "", ErrComplete
	}
	busy, err := s.phaseRunActive(taskID)
	if err != nil {
		return "", err
	}
	if busy {
		return "", ErrPhaseRunning
	}
	state, err := s.runState(taskID)
	if err != nil {
		return "", err
	}
	if state == "running" {
		return "", ErrRunning
	}
	readme, err := os.ReadFile(filepath.Join(info.PlanDir, "README.md"))
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrNoDoc, filepath.Join(info.PlanDir, "README.md"))
	}
	if agent == "" {
		agent = DefaultAgent()
	}
	runMode := ValidMode(mode)

	// Single-flight slot BEFORE the (slow, git-touching) Acquire so a concurrent
	// Start cannot double-spawn; released on any admission failure below.
	s.mu.Lock()
	if _, running := s.active[taskID]; running {
		s.mu.Unlock()
		return "", ErrRunning
	}
	uuid := s.UUID()
	ctx, cancel := context.WithCancel(context.Background())
	s.active[taskID] = run{cancel: cancel, uuid: uuid}
	s.mu.Unlock()

	release := func() {
		cancel()
		s.mu.Lock()
		delete(s.active, taskID)
		s.mu.Unlock()
	}

	acq, err := s.Wt.Acquire(info.ProjectPath, info.ProjectSlug, "plan-"+strconv.FormatInt(taskID, 10))
	if err != nil {
		release()
		return "", fmt.Errorf("worktree acquire: %w", err)
	}

	if err := s.stampStart(taskID, agent, string(runMode), uuid); err != nil {
		release()
		s.removeWorktree(info.ProjectPath, acq)
		return "", err
	}

	log.Printf("planrun: start plan=%d agent=%s mode=%s uuid=%s worktree=%q phases=%d",
		taskID, agent, runMode, uuid, acq.Path, len(info.Phases))
	s.notify(taskID)

	spec := RunSpec{
		Prompt:      BuildPrompt(info.PlanDir, string(readme), info.Phases, runMode),
		SessionUUID: uuid,
		Cwd:         acq.Path,
		Agent:       agent,
	}
	s.spawn(func() { s.runAndHandle(ctx, cancel, info, acq, spec) })
	return uuid, nil
}

// allComplete reports whether every phase already has all its criteria ticked.
func allComplete(phases []Phase) bool {
	for _, p := range phases {
		if !p.complete() {
			return false
		}
	}
	return len(phases) > 0
}

// runAndHandle executes the run to completion, stamps the exit state, removes
// the worktree (branch kept), and always releases the slot.
func (s *Service) runAndHandle(ctx context.Context, cancel context.CancelFunc, info planInfo, acq worktree.Acquired, spec RunSpec) {
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.active, info.TaskID)
		s.mu.Unlock()
		s.removeWorktree(info.ProjectPath, acq)
		s.notify(info.TaskID)
	}()

	res, err := s.Run.Start(ctx, spec)
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		// Cancel() beat the exit — whatever the child reported, the outcome is a
		// user cancellation.
		log.Printf("planrun: plan=%d uuid=%s cancelled", info.TaskID, spec.SessionUUID)
		s.stamp(info.TaskID, "failed", "cancelled")
	case err != nil:
		log.Printf("error: planrun: plan=%d uuid=%s could not start: %v", info.TaskID, spec.SessionUUID, err)
		s.stamp(info.TaskID, "failed", err.Error())
	case res.TimedOut:
		log.Printf("warning: planrun: plan=%d uuid=%s timed out", info.TaskID, spec.SessionUUID)
		s.stamp(info.TaskID, "failed", "timeout")
	case res.ExitCode != 0:
		log.Printf("warning: planrun: plan=%d uuid=%s exited %d: %s", info.TaskID, spec.SessionUUID, res.ExitCode, res.Stderr)
		msg := res.Stderr
		if msg == "" {
			msg = fmt.Sprintf("exit %d", res.ExitCode)
		}
		s.stamp(info.TaskID, "failed", msg)
	default:
		log.Printf("planrun: plan=%d uuid=%s completed in %s", info.TaskID, spec.SessionUUID, res.Duration)
		s.stamp(info.TaskID, "done", "")
	}
}

// stampStart upserts the plan_runs row into the running state.
func (s *Service) stampStart(taskID int64, agent, mode, uuid string) error {
	_, err := s.DB.Exec(`
		INSERT INTO plan_runs (workspace_task_id, agent, mode, run_state, run_session_uuid, run_started_at, run_error)
		VALUES (?, ?, ?, 'running', ?, ?, NULL)
		ON CONFLICT(workspace_task_id) DO UPDATE SET
			agent            = excluded.agent,
			mode             = excluded.mode,
			run_state        = 'running',
			run_session_uuid = excluded.run_session_uuid,
			run_started_at   = excluded.run_started_at,
			run_error        = NULL`, taskID, agent, mode, uuid, s.ts())
	return err
}

// stamp writes the terminal run state; runError "" ⇒ NULL.
func (s *Service) stamp(taskID int64, state, runError string) {
	var re any
	if runError != "" {
		re = runError
	}
	if _, err := s.DB.Exec(
		`UPDATE plan_runs SET run_state=?, run_error=? WHERE workspace_task_id=?`,
		state, re, taskID); err != nil {
		log.Printf("error: planrun: stamp plan=%d state=%s: %v", taskID, state, err)
	}
}

// removeWorktree best-effort removes the run's worktree, KEEPING the branch
// (its commits stay reachable for the user — mirrors phaserun.removeWorktree).
func (s *Service) removeWorktree(repoRoot string, acq worktree.Acquired) {
	if s.Wt == nil {
		return
	}
	if err := s.Wt.Remove(repoRoot, acq, true /* keepBranch */); err != nil {
		log.Printf("warning: planrun: remove worktree %s: %v", acq.Path, err)
	}
}

// Cancel aborts an in-flight run: the context cancel kills the child claude,
// and the run goroutine's exit path stamps failed/cancelled. Returns whether a
// run was actually in flight.
func (s *Service) Cancel(taskID int64) bool {
	s.mu.Lock()
	r, ok := s.active[taskID]
	s.mu.Unlock()
	if ok {
		r.cancel()
	}
	return ok
}

// HealStale fails any plan_runs row left 'running' by a crashed/restarted
// daemon (there can be no live run in THIS process — we just started). Mirrors
// phaserun.HealStale's posture. Called from cmd/swarmery before serving.
func (s *Service) HealStale() error {
	res, err := s.DB.Exec(`
		UPDATE plan_runs SET run_state='failed', run_error='daemon restart'
		 WHERE run_state='running'`)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("swarmery planrun: healed %d orphaned running plan(s) to failed", n)
	}
	return nil
}

// runState reads the plan's stored run state ("" when it has never run).
func (s *Service) runState(taskID int64) (string, error) {
	var state string
	err := s.DB.QueryRow(
		`SELECT run_state FROM plan_runs WHERE workspace_task_id = ?`, taskID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return state, err
}

// phaseRunActive reports whether any phase of this plan has a per-phase run in
// flight — the two mechanisms would edit the same docs from two worktrees.
func (s *Service) phaseRunActive(taskID int64) (bool, error) {
	var n int
	err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM epic_phases WHERE workspace_task_id = ? AND run_state = 'running'`,
		taskID).Scan(&n)
	return n > 0, err
}

// loadPlan reads the plan task + its project + its phases for admission.
func (s *Service) loadPlan(taskID int64) (planInfo, error) {
	var (
		info    planInfo
		planDir sql.NullString
		path    sql.NullString
	)
	err := s.DB.QueryRow(`
		SELECT t.id, t.status, t.archived_at IS NOT NULL, p.path, p.slug,
		       (SELECT path FROM task_artifacts WHERE task_id = t.id AND kind = 'plan')
		  FROM tasks t
		  JOIN projects p ON p.id = t.project_id
		 WHERE t.id = ? AND t.source = 'workspace'`, taskID).Scan(
		&info.TaskID, &info.Status, &info.Archived, &path, &info.ProjectSlug, &planDir)
	if errors.Is(err, sql.ErrNoRows) {
		return info, ErrPlanNotFound
	}
	if err != nil {
		return info, err
	}
	info.ProjectPath = path.String
	info.PlanDir = planDir.String
	if info.PlanDir == "" {
		return info, ErrPlanNotFound
	}
	phases, err := s.loadPhases(taskID)
	if err != nil {
		return info, err
	}
	info.Phases = phases
	return info, nil
}

// loadPhases reads the plan's phases in execution order.
func (s *Service) loadPhases(taskID int64) ([]Phase, error) {
	rows, err := s.DB.Query(`
		SELECT seq, name, doc_path, depends_on, checkboxes_done, checkboxes_total
		  FROM epic_phases
		 WHERE workspace_task_id = ?
		 ORDER BY seq, id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Phase
	for rows.Next() {
		var (
			p        Phase
			depsJSON string
		)
		if err := rows.Scan(&p.Seq, &p.Name, &p.DocPath, &depsJSON, &p.Done, &p.Total); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(depsJSON), &p.DependsOn); err != nil {
			p.DependsOn = nil // garbage depends_on ⇒ no ordering hint (epics.go posture)
		}
		sort.Ints(p.DependsOn)
		out = append(out, p)
	}
	return out, rows.Err()
}
