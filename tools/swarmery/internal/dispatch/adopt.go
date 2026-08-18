package dispatch

// Adoption — a dispatched run that outlived the daemon. The loop lives in
// internal/runcore (why adoption exists at all is documented there); what stays
// here is how dispatch differs from the other two engines, and it differs
// deliberately.
//
// Dispatch already owns an evidence-based reclaim path: procwatch marks the run's
// session proc_state='dead', and HealDeadProcess then requeues the task with retry
// accounting, progress high-water and worktree handling all in one place.
// Duplicating that here would mean two reclaim policies to keep in step — so
// adoption does the one thing that path cannot do for itself: hold the concurrency
// slot while the orphan runs, so the scheduler does not dispatch over the cap, and
// poke it when the process is finally gone.
//
// Hence no Kill hook (a board run has no cancel path) and no terminal write: the
// only Ended action is a Poke, which lets HealDeadProcess act on the evidence
// procwatch has by then written.

import "github.com/atretyak1985/swarmery/tools/swarmery/internal/runcore"

// tracked adapts this service to runcore.Tracked.
type tracked struct{ s *Service }

func (tracked) Engine() string { return Engine }

func (t tracked) ScanRunning() ([]runcore.Candidate, error) {
	return runcore.ScanCandidates(t.s.DB, `
		SELECT id, COALESCE(dispatch_session_uuid,'')
		  FROM tasks
		 WHERE source='queue' AND board_column='in_progress'`)
}

func (t tracked) Adopt(runcore.Candidate, int) (runcore.AdoptHooks, bool) {
	return runcore.AdoptHooks{Ended: func(bool) { t.s.Poke() }}, true
}

// adoptSurvivors probes every in_progress queue task and adopts the ones whose
// executor is still alive, returning their ids so HealStale leaves them alone.
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
