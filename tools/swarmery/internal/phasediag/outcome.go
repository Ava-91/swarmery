// Package phasediag turns a phase's DB row plus the repo's git state into a
// human-actionable diagnosis of what a headless phase run achieved. It is read-only
// and computed on demand: git state changes outside the daemon (a user merging a
// branch in a terminal), so a cached blocker would be stale exactly when it matters.
package phasediag

import "database/sql"

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

// Outcome derives what a run achieved from the closed interval [before, after].
// It is the pure primitive: both edges are already resolved, so it admits no NULLs
// and holds no policy about what an unmeasured run means.
//
// Callers holding an epic_phases row must NOT call this directly — the row's two
// columns need OutcomeFromRow's policy first. Keep this pure and dependency-free.
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

// OutcomeFromRow derives the outcome straight from a phase row's columns, applying
// the two policies that make the derivation honest: run_checkboxes_after (the count
// stamped when the run ended) wins over the live count, which keeps moving; and a
// NULL run_checkboxes_before means UNMEASURED, so it collapses to `after` and an
// unmeasured run can never be reported as partial progress it may not have made.
// Callers that have a row should use this, never Outcome directly.
//
// This is the SINGLE derivation both surfaces go through — Diagnose for the modal
// and the api layer for the phase DTO — so the list chip and the diagnosis can never
// disagree. `live` is checkboxes_done, the fallback right edge for a run that never
// stamped one (pre-0042 rows, and runs still in flight).
func OutcomeFromRow(runState string, total, live int, before, after sql.NullInt64) string {
	right := live
	if after.Valid {
		right = int(after.Int64)
	}
	left := right // NULL baseline ⇒ zero delta, never 'partial'
	if before.Valid {
		left = int(before.Int64)
	}
	return Outcome(runState, total, left, right)
}
