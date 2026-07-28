// Package phasediag turns a phase's DB row plus the repo's git state into a
// human-actionable diagnosis of what a headless phase run achieved. It is read-only
// and computed on demand: git state changes outside the daemon (a user merging a
// branch in a terminal), so a cached blocker would be stale exactly when it matters.
package phasediag

// Derived run outcomes. run_state describes how the PROCESS ended; the outcome
// describes whether WORK LANDED. The two diverge whenever an executor exits 0
// without ticking anything — a failed precondition, or refused work.
const (
	OutcomeIdle      = "idle"
	OutcomeRunning   = "running"
	OutcomeCompleted = "completed"
	OutcomePartial   = "partial"
	OutcomeNoop      = "noop"
	OutcomeFailed    = "failed"
)

// Outcome derives what a run achieved. before is the pre-run checkbox snapshot
// (0 for historical rows whose run_checkboxes_before is NULL — correct for them,
// since those runs ticked nothing).
//
// This is the SINGLE implementation of the derivation: the api layer calls it for
// the phase DTO too, so the list chip and the diagnosis modal can never disagree.
// Keep it pure and dependency-free.
func Outcome(runState string, total, before, after int) string {
	switch runState {
	case "running":
		return OutcomeRunning
	case "failed":
		return OutcomeFailed
	case "done":
		switch {
		case total > 0 && after >= total:
			return OutcomeCompleted
		case after > before:
			return OutcomePartial
		default:
			return OutcomeNoop
		}
	default:
		return OutcomeIdle
	}
}
