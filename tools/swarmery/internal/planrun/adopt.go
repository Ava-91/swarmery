package planrun

// Adoption — a plan run that outlived the daemon. Same contract as
// phaserun/adopt.go, and for the same reason: the run is spawned in its own
// process group, so a daemon restart no longer kills it. A row left 'running'
// therefore has to be PROBED, not assumed dead — condemning a live orchestrator
// to 'failed / daemon restart' both lies on the Plans page and frees the
// single-flight slot, so a Retry would put a second orchestrator into the same
// worktree while the first is still committing.
//
// The exit status of an orphan is unrecoverable (it is not our child), so the
// terminal stamp is 'done' with an explicit note; what the run actually achieved
// is visible where it always is — the plan's phase checkboxes. An operator Stop
// is the one outcome adoption can know, and stays failed/cancelled.

import (
	"log"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/procfind"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/procgroup"
)

// adoptPollInterval is how often an adopted run's pid is probed — coarse, since
// a plan run lasts hours and a few seconds of a stale chip costs nothing.
const adoptPollInterval = 5 * time.Second

// adoptedExitNote is stamped as run_error when an adopted run's process exits:
// the state is 'done', but nobody may later read that as a reaped clean exit.
const adoptedExitNote = "adopted after a daemon restart — exit status unknown"

func (s *Service) findRun(sessionUUID string) (int, bool) {
	if s.FindRun != nil {
		return s.FindRun(sessionUUID)
	}
	return procfind.BySessionUUID(sessionUUID)
}

func (s *Service) procAlive(pid int) bool {
	if s.ProcAlive != nil {
		return s.ProcAlive(pid)
	}
	return syscall.Kill(pid, 0) == nil
}

func (s *Service) pollInterval() time.Duration {
	if s.adoptPoll > 0 {
		return s.adoptPoll
	}
	return adoptPollInterval
}

// adoptSurvivors probes every 'running' plan run and adopts the live ones,
// returning their workspace task ids so HealStale can exclude them.
func (s *Service) adoptSurvivors() ([]int64, error) {
	rows, err := s.DB.Query(
		`SELECT workspace_task_id, COALESCE(run_session_uuid,'') FROM plan_runs WHERE run_state='running'`)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		taskID int64
		uuid   string
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.taskID, &c.uuid); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var adopted []int64
	for _, c := range candidates {
		if strings.TrimSpace(c.uuid) == "" {
			continue // nothing to match a process against
		}
		pid, ok := s.findRun(c.uuid)
		if !ok {
			continue
		}
		s.adopt(c.taskID, pid, c.uuid)
		adopted = append(adopted, c.taskID)
	}
	return adopted, nil
}

// adopt reserves the slot for a run this process did not spawn, wires Cancel to
// the orphan's process group, and watches its pid. The worktree is deliberately
// left alone: this process never acquired it, and the next Start reuses the
// deterministic path.
func (s *Service) adopt(taskID int64, pid int, uuid string) {
	cancelled := &atomic.Bool{}
	// Slots.Adopt, not the admission path: the orchestrator is ALREADY running, so
	// a full pool must not stop us tracking it — refusing would leave the slot free
	// for a rival Start in the same worktree, the exact failure this file exists to
	// prevent. The pool stays over-subscribed until the orphan exits.
	_, ok := s.Slots.Adopt(s.slotKey(taskID), uuid, func() {
		cancelled.Store(true)
		if err := procgroup.Kill(pid); err != nil {
			log.Printf("warning: planrun: kill adopted run plan=%d pid=%d: %v", taskID, pid, err)
		}
	})
	if !ok {
		return // already tracked — never adopt twice
	}

	log.Printf("planrun: adopted running plan=%d uuid=%s pid=%d (survived a daemon restart)", taskID, uuid, pid)
	s.notify(taskID)
	s.spawn(func() { s.watchAdopted(taskID, pid, cancelled) })
}

// watchAdopted blocks until the adopted pid is gone, then stamps the terminal
// state and releases the slot. Mirrors the normal exit path, minus the things
// only a parent can do (exit status, worktree removal).
func (s *Service) watchAdopted(taskID int64, pid int, cancelled *atomic.Bool) {
	for s.procAlive(pid) {
		time.Sleep(s.pollInterval())
	}

	// Release-by-key, deliberately: what this watcher needs to know is whether the
	// run was STILL tracked when the pid vanished. Something else closing it out
	// first means the terminal write below would stamp over a recorded outcome.
	if !s.Slots.Release(s.slotKey(taskID)) {
		return // someone else already closed this run out
	}

	state, note := "done", adoptedExitNote
	if cancelled.Load() {
		state, note = "failed", "cancelled"
	}
	log.Printf("planrun: adopted run plan=%d pid=%d ended → %s", taskID, pid, state)
	s.stamp(taskID, state, note)
	s.notify(taskID)
}
