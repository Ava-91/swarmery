package verify

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Every infra path degrades to INCONCLUSIVE — that part is deliberate and stays.
// What was missing is WHICH path: the store holds a 1.1s inconclusive row whose
// detail is the empty string, and a verifier that never started was recorded the
// same way as one that ran and declined to call it. Those have different fixes,
// so the class is now stamped at the point the path is taken.
func TestInconclusiveDetailCarriesItsClass(t *testing.T) {
	for _, tc := range []struct {
		name      string
		runner    *stubRunner
		trees     Trees
		wantClass string
		wantWords string
	}{
		{
			name:      "process never started",
			runner:    &stubRunner{err: errors.New(`fork/exec /opt/homebrew/bin/claude: no such file or directory`)},
			trees:     stubTrees{hash: "t1"},
			wantClass: ClassNotStarted,
			wantWords: "no such file or directory",
		},
		{
			name:      "killed by the hard timeout",
			runner:    &stubRunner{run: &Run{TimedOut: true, ExitCode: -1}},
			trees:     stubTrees{hash: "t2"},
			wantClass: ClassTimedOut,
			wantWords: "hard timeout",
		},
		{
			name:      "exited having written nothing",
			runner:    &stubRunner{run: &Run{Output: "", ExitCode: 0}},
			trees:     stubTrees{hash: "t3"},
			wantClass: ClassNoVerdict,
			wantWords: "written nothing",
		},
		{
			name:      "wrote a transcript but no verdict line",
			runner:    &stubRunner{out: "I looked at the diff and then ran out of budget."},
			trees:     stubTrees{hash: "t4"},
			wantClass: ClassNoVerdict,
			wantWords: "no VERDICT: line",
		},
		{
			name:      "ran and declined to call it",
			runner:    &stubRunner{out: "deps would not install\nVERDICT: INCONCLUSIVE"},
			trees:     stubTrees{hash: "t5"},
			wantClass: ClassCouldNotConclude,
			wantWords: "deps would not install",
		},
		{
			name:      "worktree reclaimed before the tree could be read",
			runner:    &stubRunner{},
			trees:     stubTrees{err: errors.New("worktree gone")},
			wantClass: ClassUnverifiable,
			wantWords: "reclaimed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := testDB(t)
			s := newTestService(t, db, tc.runner, tc.trees)
			id := insertTask(t, db, taskOpts{})

			if err := s.VerifyTask(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			if got := verdictOf(t, db, id); got != "inconclusive" {
				t.Fatalf("verdict = %q, want inconclusive — the fail-safe must not change", got)
			}
			detail := detailOf(t, db, id)
			if !strings.HasPrefix(detail, tc.wantClass+":") {
				t.Errorf("detail = %q, want it classified %q", detail, tc.wantClass)
			}
			if !strings.Contains(detail, tc.wantWords) {
				t.Errorf("detail = %q, want it to mention %q", detail, tc.wantWords)
			}
		})
	}
}

// A blank detail is the one thing an inconclusive stamp must never be: it reads
// as "nothing happened", which is exactly what it never means.
func TestInconclusiveNeverStampsAnEmptyDetail(t *testing.T) {
	db := testDB(t)
	// An output that parses to INCONCLUSIVE with no collectable reasons at all.
	s := newTestService(t, db, &stubRunner{out: "VERDICT: INCONCLUSIVE"}, stubTrees{hash: "t-blank"})
	id := insertTask(t, db, taskOpts{})

	if err := s.VerifyTask(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(detailOf(t, db, id)); got == "" {
		t.Fatal("an inconclusive verdict was stamped with an empty detail")
	}
}

// The distinction the operator needs, asserted as a distinction rather than as
// two separate strings: a dead verifier and an undecided one must not read the
// same.
func TestNotStartedAndCouldNotConcludeAreDistinguishable(t *testing.T) {
	dead := testDB(t)
	sDead := newTestService(t, dead, &stubRunner{err: errors.New("no such file or directory")}, stubTrees{hash: "d"})
	idDead := insertTask(t, dead, taskOpts{})
	if err := sDead.VerifyTask(context.Background(), idDead); err != nil {
		t.Fatal(err)
	}

	undecided := testDB(t)
	sUnd := newTestService(t, undecided, &stubRunner{out: "the suite is flaky here\nVERDICT: INCONCLUSIVE"}, stubTrees{hash: "u"})
	idUnd := insertTask(t, undecided, taskOpts{})
	if err := sUnd.VerifyTask(context.Background(), idUnd); err != nil {
		t.Fatal(err)
	}

	a, b := detailOf(t, dead, idDead), detailOf(t, undecided, idUnd)
	if a == b {
		t.Fatalf("a verifier that never ran and one that could not conclude read identically: %q", a)
	}
	if strings.HasPrefix(a, ClassCouldNotConclude) || strings.HasPrefix(b, ClassNotStarted) {
		t.Errorf("the two classes are swapped: dead=%q undecided=%q", a, b)
	}
}
