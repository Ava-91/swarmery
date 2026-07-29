package phasediag

import (
	"database/sql"
	"testing"
)

// TestOutcome pins the run_state → outcome derivation. The interesting rows are
// the 'done' ones: the whole point of the package is that a clean process exit
// says nothing about whether work landed.
func TestOutcome(t *testing.T) {
	cases := []struct {
		name                 string
		runState             string
		total, before, after int
		want                 string
	}{
		{"idle empty state", "", 0, 0, 0, OutcomeIdle},
		{"idle explicit", "idle", 7, 0, 0, OutcomeIdle},
		{"unknown state reads as idle", "queued", 7, 0, 0, OutcomeIdle},
		{"running", "running", 7, 0, 3, OutcomeRunning},
		{"failed", "failed", 7, 0, 0, OutcomeFailed},
		{"done all ticked", "done", 7, 0, 7, OutcomeCompleted},
		{"done over-ticked", "done", 7, 0, 9, OutcomeCompleted},
		{"done partial", "done", 7, 3, 5, OutcomePartial},
		{"done nothing ticked", "done", 7, 0, 0, OutcomeNoop},
		{"done no criteria at all", "done", 0, 0, 0, OutcomeNoop},
		{"done same count as before", "done", 8, 3, 3, OutcomeNoop},
		{"done regressed", "done", 8, 5, 4, OutcomeNoop},
		// A pre-0041 row: before reads 0 because the column is NULL, and those
		// runs genuinely ticked nothing measurable.
		{"historical row null baseline", "done", 7, 0, 0, OutcomeNoop},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Outcome(tc.runState, tc.total, tc.before, tc.after); got != tc.want {
				t.Fatalf("Outcome(%q, %d, %d, %d) = %q, want %q",
					tc.runState, tc.total, tc.before, tc.after, got, tc.want)
			}
		})
	}
}

func i64(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }

// TestOutcomeFromRow pins the two row-level policies that make the derivation
// honest, so every caller that has a phase row gets them for free: the stamped
// run_checkboxes_after beats the live count, and a NULL run_checkboxes_before is
// UNMEASURED (collapsed to `after`), never "0 ticked".
func TestOutcomeFromRow(t *testing.T) {
	var null sql.NullInt64
	cases := []struct {
		name          string
		runState      string
		total, live   int
		before, after sql.NullInt64
		want          string
	}{
		{"stamped after beats the live count", "done", 7, 7, i64(1), i64(2), OutcomePartial},
		{"live count used when after is NULL", "done", 7, 5, i64(3), null, OutcomePartial},
		{"null baseline is never partial", "done", 7, 3, null, null, OutcomeNoop},
		{"null baseline on a full phase still completes", "done", 7, 7, null, null, OutcomeCompleted},
		{"null baseline with a stamped after", "done", 7, 7, null, i64(3), OutcomeNoop},
		{"measured full run", "done", 4, 4, i64(0), i64(4), OutcomeCompleted},
		{"measured no-op", "done", 4, 1, i64(1), i64(1), OutcomeNoop},
		{"running short-circuits", "running", 4, 0, null, null, OutcomeRunning},
		{"failed short-circuits", "failed", 4, 4, null, null, OutcomeFailed},
		{"idle", "", 4, 0, null, null, OutcomeIdle},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := OutcomeFromRow(tc.runState, tc.total, tc.live, tc.before, tc.after)
			if got != tc.want {
				t.Fatalf("OutcomeFromRow(%q, total=%d, live=%d, before=%v, after=%v) = %q, want %q",
					tc.runState, tc.total, tc.live, tc.before, tc.after, got, tc.want)
			}
		})
	}
}
