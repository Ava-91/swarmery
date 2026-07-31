package phaserun

// Adoption — a phase run that outlived the daemon.
//
// The run is spawned in its own process group (see runner.go / procgroup), so
// launchd stopping the daemon no longer takes the executor down with it: it
// keeps working, keeps ticking checkboxes in the phase doc, and keeps writing
// its transcript. That breaks HealStale's original premise ("we just started, so
// nothing can be running"): stamping such a row 'failed / daemon restart' makes
// the card claim a process is dead while it is visibly still producing work —
// and, worse, frees the single-flight slot so a Retry spawns a SECOND executor
// into the same worktree.
//
// So startup probes each 'running' row instead of assuming. A row whose process
// is still there is re-adopted: the state stays 'running', the slot is held, the
// Stop button keeps working (it kills the orphan's process group), and a watcher
// stamps the terminal state once the pid finally goes away. Only rows with no
// live process are healed to failed.
//
// What adoption cannot recover: the exit status. The orphan is not our child, so
// there is no wait() to read — it is stamped 'done' with an explicit note, which
// hands the "did anything land?" question to the checkbox interval
// (phasediag: completed / partial / noop), the honest signal we do still have.

import (
	"log"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/procfind"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/procgroup"
)

// adoptPollInterval is how often an adopted run's pid is probed. Coarse on
// purpose: the run is minutes-to-hours long, and the only cost of noticing its
// exit a few seconds late is a few seconds of a stale 'running' chip.
const adoptPollInterval = 5 * time.Second

// adoptedExitNote is stamped as run_error when an adopted run's process
// disappears. The state is 'done' because the checkbox interval, not this note,
// answers whether work landed — but the note must survive so nobody later reads
// a clean exit into a run we never actually reaped.
const adoptedExitNote = "adopted after a daemon restart — exit status unknown"

// findRun locates a live run process by its session uuid, via the seam when one
// is wired (tests) and a ps scan otherwise.
func (s *Service) findRun(sessionUUID string) (int, bool) {
	if s.FindRun != nil {
		return s.FindRun(sessionUUID)
	}
	return procfind.BySessionUUID(sessionUUID)
}

// procAlive reports whether a pid still exists, via the seam when wired.
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

// adoptSurvivors probes every 'running' row and adopts the ones whose process is
// still alive, returning their phase ids so HealStale can exclude them from the
// fail sweep. A row we cannot load is left to the sweep: an adopted run we cannot
// stamp on exit would be stuck 'running' forever, which is worse than a wrong
// 'failed' the operator can retry.
func (s *Service) adoptSurvivors() ([]int64, error) {
	rows, err := s.DB.Query(
		`SELECT id, COALESCE(run_session_uuid,'') FROM epic_phases WHERE run_state='running'`)
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
			continue // pre-uuid row: nothing to match a process against
		}
		pid, ok := s.findRun(c.uuid)
		if !ok {
			continue
		}
		info, err := s.loadPhase(c.id)
		if err != nil {
			log.Printf("warning: phaserun: phase=%d has a live process (pid=%d) but is unloadable (%v) — healing it instead",
				c.id, pid, err)
			continue
		}
		s.adopt(c.id, info, pid, c.uuid)
		adopted = append(adopted, c.id)
	}
	return adopted, nil
}

// adopt re-attaches the service to a run it did not spawn: it reserves the
// single-flight slot (so Retry cannot start a rival executor in the same
// worktree), wires Cancel to kill the orphan's process group, and watches the
// pid until it exits.
//
// The worktree is deliberately NOT removed when the adopted run ends: this
// process never acquired it, so it holds no lock over it, and the deterministic
// path is warm-reused by the next Start anyway.
func (s *Service) adopt(phaseID int64, info phaseInfo, pid int, uuid string) {
	cancelled := &atomic.Bool{}
	s.mu.Lock()
	if _, busy := s.active[phaseID]; busy {
		s.mu.Unlock()
		return // already tracked — never adopt the same run twice
	}
	s.active[phaseID] = run{
		uuid: uuid,
		cancel: func() {
			cancelled.Store(true)
			// The run leads its own group (procgroup.Isolate at spawn), so this
			// reaches its children too — the same kill a cancel of our own child does.
			if err := procgroup.Kill(pid); err != nil {
				log.Printf("warning: phaserun: kill adopted run phase=%d pid=%d: %v", phaseID, pid, err)
			}
		},
	}
	s.mu.Unlock()

	log.Printf("phaserun: adopted running phase=%d uuid=%s pid=%d (survived a daemon restart)", phaseID, uuid, pid)
	s.notify(info.WorkspaceTaskID)
	s.spawn(func() { s.watchAdopted(phaseID, info, pid, cancelled) })
}

// watchAdopted blocks until the adopted pid is gone, then stamps the terminal
// state and releases the slot. Mirrors runAndHandle's exit path, minus the
// things only a parent can do (exit status, worktree removal).
func (s *Service) watchAdopted(phaseID int64, info phaseInfo, pid int, cancelled *atomic.Bool) {
	for s.procAlive(pid) {
		time.Sleep(s.pollInterval())
	}

	s.mu.Lock()
	_, tracked := s.active[phaseID]
	delete(s.active, phaseID)
	s.mu.Unlock()
	if !tracked {
		return // something else already closed this run out; don't stamp over it
	}

	// A Stop the operator pressed is a cancellation, not an unknown exit — that
	// distinction is the one piece of the outcome adoption CAN still know.
	state, note := "done", adoptedExitNote
	if cancelled.Load() {
		state, note = "failed", "cancelled"
	}
	log.Printf("phaserun: adopted run phase=%d pid=%d ended → %s", phaseID, pid, state)
	s.stamp(phaseID, info.DocPath, state, note)
	s.notify(info.WorkspaceTaskID)
}
