// FNXC:AgentEconomics 2026-07-30-09:50 — a report is only reproducible if its
// window and project filters actually bind, so the two Options cases that the
// main test file never exercises are pinned here.

package economics

import "testing"

// ------------------------------------------------------------------ options ---
//
// Lifted 2026-07-30 from a later, independent rewrite of economics_test.go that
// was orphaned when its worktree was pruned (kept at
// salvage/economics_test.orphaned-2026-07-29-2305.go in the audit task dir).
// Six of that copy's eight tests were renames of cases already present in
// economics_test.go and were deliberately NOT copied; only these two were
// genuinely missing. Both exercise Options, which nothing else does — every
// other test passes Options{}, leaving validate() and the scope-argument
// binding untested.

// TestOptionsWindowNarrowsTheSample pins the bind order of the six scope
// arguments produced by args(). A swapped pair still returns rows, so the only
// things that catch it are a window that must be empty and a project id that
// must match nothing.
func TestOptionsWindowNarrowsTheSample(t *testing.T) {
	db := newFixtureDB(t)

	full := mustCompute(t, db, Options{})
	if full.Sample.Turns == 0 {
		t.Fatal("unwindowed Turns = 0, the fixture did not load")
	}

	// Every fixture turn falls on 2026-07-10 or 2026-07-20, so a January window
	// must come back empty.
	empty := mustCompute(t, db, Options{Since: "2026-01-01", Until: "2026-01-31"})
	if empty.Sample.Turns != 0 {
		t.Errorf("windowed Turns = %d, want 0", empty.Sample.Turns)
	}

	// Project 2 holds exactly task 7 and session 8 — a single priced turn.
	other := mustCompute(t, db, Options{ProjectID: 2})
	if other.Sample.Turns != 1 || other.Sample.Tasks != 1 {
		t.Errorf("project 2 turns/tasks = %d/%d, want 1/1", other.Sample.Turns, other.Sample.Tasks)
	}

	none := mustCompute(t, db, Options{ProjectID: 999})
	if none.Sample.Turns != 0 || none.Sample.Tasks != 0 {
		t.Errorf("foreign project turns/tasks = %d/%d, want 0/0", none.Sample.Turns, none.Sample.Tasks)
	}
}

// TestOptionsRejectMalformedDates stops validate() from degrading into a silent
// pass: a typo in --since must fail the command outright rather than quietly
// widening the window and producing a number the operator would trust.
func TestOptionsRejectMalformedDates(t *testing.T) {
	db := newFixtureDB(t)
	for _, o := range []Options{{Since: "20-07-2026"}, {Until: "not-a-date"}, {Since: "2026-07-99x"}} {
		if _, err := Compute(db, o); err == nil {
			t.Errorf("Compute(%+v) accepted a malformed date", o)
		}
	}
}
