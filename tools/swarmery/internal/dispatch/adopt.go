package dispatch

// Adoption — a dispatched run that outlived the daemon.
//
// The executor is spawned in its own process group (internal/procgroup), so a
// daemon restart no longer kills it: it keeps editing the worktree and keeps
// committing. HealStale's premise ("we just started, so nothing can be running")
// is therefore no longer safe — requeuing such a task to `todo` hands the SAME
// worktree to a second executor while the first is still writing in it.
//
// Dispatch adopts differently from phaserun/planrun, and deliberately so: it
// already owns an evidence-based reclaim path. procwatch marks the run's session
// proc_state='dead', and HealDeadProcess then requeues the task with retry
// accounting, progress high-water and worktree handling all in one place.
// Duplicating that here would mean two reclaim policies to keep in step, so
// adoption does the one thing that path cannot do for itself: hold the
// concurrency slot while the orphan runs, so the scheduler does not dispatch
// over the cap, and release it when the process is finally gone.

import (
	"log"
	"strings"
	"syscall"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/procfind"
)

// adoptPollInterval is how often an adopted run's pid is probed. A dispatched
// run lasts minutes to hours; noticing its exit a few seconds late costs only a
// few seconds of a held slot.
const adoptPollInterval = 5 * time.Second

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

// adoptSurvivors probes every in_progress queue task and adopts the ones whose
// executor is still alive, returning their ids so HealStale leaves them alone.
func (s *Service) adoptSurvivors() ([]int64, error) {
	rows, err := s.DB.Query(`
		SELECT id, COALESCE(dispatch_session_uuid,'')
		  FROM tasks
		 WHERE source='queue' AND board_column='in_progress'`)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		id   int64
		uuid string
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.uuid); err != nil {
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
		s.adopt(c.id, pid, c.uuid)
		adopted = append(adopted, c.id)
	}
	return adopted, nil
}

// adopt holds the concurrency slot for a run this process did not spawn and
// watches its pid. When the process finally exits, the slot is released and the
// scheduler is poked: procwatch will have written proc_state='dead', which is
// exactly the evidence HealDeadProcess needs to requeue the task properly.
func (s *Service) adopt(taskID int64, pid int, uuid string) {
	// Slots.Adopt, not the admission path: the executor is ALREADY running, so a
	// full pool must not stop us tracking it. Refusing would leave the slot free
	// and let the scheduler dispatch over the cap into the same worktree — the
	// very thing this function exists to prevent. The pool stays over-subscribed
	// until the orphan exits, and new admissions are refused meanwhile, which is
	// the honest reading of the machine's state after a restart.
	release, ok := s.Slots.Adopt(s.slotKey(taskID), uuid, nil)
	if !ok {
		return // already tracked — never adopt the same run twice
	}
	log.Printf("dispatch: adopted running task=%d uuid=%s pid=%d (survived a daemon restart)", taskID, uuid, pid)
	s.spawn(func() {
		for s.procAlive(pid) {
			time.Sleep(s.pollInterval())
		}
		log.Printf("dispatch: adopted run task=%d pid=%d ended — releasing its slot", taskID, pid)
		release()
		s.Poke()
	})
}
