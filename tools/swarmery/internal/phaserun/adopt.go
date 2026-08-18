package phaserun

// Adoption — a phase run that outlived the daemon. The loop lives in
// internal/runcore (why adoption exists at all, and what it can and cannot
// recover, is documented there); what stays here is this engine's policy:
//
//   - which rows are 'running' (epic_phases),
//   - what a Stop does to an orphan (kill its process group),
//   - what to write when the pid finally goes away ('done' + the unknown-exit
//     note, which hands the "did anything land?" question to the checkbox interval
//     — phasediag: completed / partial / noop, the honest signal we do still have).

import (
	"log"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/procgroup"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/runcore"
)

// tracked adapts this service to runcore.Tracked.
type tracked struct{ s *Service }

func (tracked) Engine() string { return Engine }

func (t tracked) ScanRunning() ([]runcore.Candidate, error) {
	return runcore.ScanCandidates(t.s.DB,
		`SELECT id, COALESCE(run_session_uuid,'') FROM epic_phases WHERE run_state='running'`)
}

func (t tracked) Adopt(c runcore.Candidate, pid int) (runcore.AdoptHooks, bool) {
	// The row has to be loadable: the terminal stamp needs its doc path, and the UI
	// nudge needs its workspace task id. A row we cannot load is left to the heal
	// sweep — an adopted run we could never stamp would be stuck 'running' for ever.
	info, err := t.s.loadPhase(c.ID)
	if err != nil {
		log.Printf("warning: phaserun: phase=%d has a live process (pid=%d) but is unloadable (%v) — healing it instead",
			c.ID, pid, err)
		return runcore.AdoptHooks{}, false
	}
	return runcore.AdoptHooks{
		Kill: func() {
			// The run leads its own group (procgroup.Isolate at spawn), so this reaches
			// its children too — the same kill a cancel of our own child does.
			if err := procgroup.Kill(pid); err != nil {
				log.Printf("warning: phaserun: kill adopted run phase=%d pid=%d: %v", c.ID, pid, err)
			}
		},
		Adopted: func() { t.s.notify(info.WorkspaceTaskID) },
		Ended: func(cancelled bool) {
			state, note := "done", runcore.AdoptedExitNote
			if cancelled {
				state, note = "failed", "cancelled"
			}
			t.s.stamp(c.ID, info.DocPath, state, note)
			t.s.notify(info.WorkspaceTaskID)
		},
	}, true
}

// adoptSurvivors probes every 'running' row and adopts the live ones, returning
// their phase ids so HealStale can exclude them from the fail sweep.
func (s *Service) adoptSurvivors() ([]int64, error) {
	return runcore.Adopter{
		Slots:     s.Slots,
		Tracked:   tracked{s},
		FindRun:   s.FindRun,
		ProcAlive: s.ProcAlive,
		Poll:      s.adoptPoll,
		Go:        s.Go,
	}.AdoptSurvivors()
}
