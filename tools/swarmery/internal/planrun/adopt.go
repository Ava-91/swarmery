package planrun

// Adoption — a plan run that outlived the daemon. The loop lives in
// internal/runcore (why adoption exists at all, and what it can and cannot
// recover, is documented there); what stays here is this engine's policy:
//
//   - which rows are 'running' (plan_runs),
//   - what a Stop does to an orphan (kill its process group — condemning a live
//     orchestrator to 'failed / daemon restart' would both lie on the Plans page and
//     free the slot, so a Retry would put a second orchestrator into the same
//     worktree while the first is still committing),
//   - what to write when the pid finally goes away ('done' + the unknown-exit note;
//     what the run actually achieved is visible where it always is, in the plan's
//     phase checkboxes).

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
		`SELECT workspace_task_id, COALESCE(run_session_uuid,'') FROM plan_runs WHERE run_state='running'`)
}

func (t tracked) Adopt(c runcore.Candidate, pid int) (runcore.AdoptHooks, bool) {
	return runcore.AdoptHooks{
		Kill: func() {
			if err := procgroup.Kill(pid); err != nil {
				log.Printf("warning: planrun: kill adopted run plan=%d pid=%d: %v", c.ID, pid, err)
			}
		},
		Adopted: func() { t.s.notify(c.ID) },
		Ended: func(cancelled bool) {
			state, note := "done", runcore.AdoptedExitNote
			if cancelled {
				state, note = "failed", "cancelled"
			}
			t.s.stamp(c.ID, state, note)
			t.s.notify(c.ID)
		},
	}, true
}

// adoptSurvivors probes every 'running' plan run and adopts the live ones,
// returning their workspace task ids so HealStale can exclude them.
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
