package phasediag

import "testing"

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
