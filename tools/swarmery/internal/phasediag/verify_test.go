package phasediag

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// setVerdict stamps the verification columns the way internal/verify does.
func (f *fx) setVerdict(t *testing.T, phaseID int64, verdict, detail string) {
	t.Helper()
	mustExec(t, f.db, `UPDATE epic_phases SET verify_verdict=?, verify_detail=? WHERE id=?`,
		verdict, detail, phaseID)
}

// TestVerifyFailedBlocker is the D5 contract in one test: a FAIL verdict on a phase
// that ticked every criterion raises a verify-failed BLOCKER carrying the verifier's
// reasons, and leaves the outcome exactly as the checkboxes derived it. The outcome
// vocabulary gains nothing.
func TestVerifyFailedBlocker(t *testing.T) {
	f := newFixture(t)
	p := f.addPhase(t, 1, "Phase 1", "[]", 3, 3) // all criteria ticked
	f.setRun(t, p, "done", "", 0)
	f.setVerdict(t, p, "fail", "criterion 2 claims a passing test; the test does not exist")

	d, err := Diagnose(f.db, nil, nil, p)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}

	if d.RunOutcome != OutcomeCompleted {
		t.Errorf("runOutcome = %q, want %q — the verdict is an input, not a status",
			d.RunOutcome, OutcomeCompleted)
	}
	b := blockerOf(t, d, KindVerifyFailed)
	if !strings.Contains(b.Detail, "the test does not exist") {
		t.Errorf("blocker detail = %q, want the verifier's own reasons", b.Detail)
	}
	if b.Summary == "" {
		t.Error("blocker has no summary — the UI renders it verbatim")
	}
	if d.VerifyVerdict == nil || *d.VerifyVerdict != "fail" {
		t.Errorf("verifyVerdict = %v, want fail", d.VerifyVerdict)
	}
	if d.VerifyDetail == nil || !strings.Contains(*d.VerifyDetail, "criterion 2") {
		t.Errorf("verifyDetail = %v, want the reasons", d.VerifyDetail)
	}
}

// TestVerifyPassAndInconclusiveRaiseNoBlocker: pass is the silent normal, and
// inconclusive is an absence of evidence — neither is something standing in the
// operator's way, so neither becomes a blocker. Both are still reported as fields.
func TestVerifyPassAndInconclusiveRaiseNoBlocker(t *testing.T) {
	for _, verdict := range []string{"pass", "inconclusive", ""} {
		t.Run("verdict="+verdict, func(t *testing.T) {
			f := newFixture(t)
			p := f.addPhase(t, 1, "Phase 1", "[]", 2, 2)
			f.setRun(t, p, "done", "", 0)
			if verdict != "" {
				f.setVerdict(t, p, verdict, "detail for "+verdict)
			}

			d, err := Diagnose(f.db, nil, nil, p)
			if err != nil {
				t.Fatalf("Diagnose: %v", err)
			}
			for _, b := range d.Blockers {
				if b.Kind == KindVerifyFailed {
					t.Fatalf("verdict %q raised a verify-failed blocker (kinds=%v)", verdict, kinds(d.Blockers))
				}
			}
			if verdict == "" {
				if d.VerifyVerdict != nil {
					t.Errorf("verifyVerdict = %v, want null for a phase never graded", *d.VerifyVerdict)
				}
			} else if d.VerifyVerdict == nil || *d.VerifyVerdict != verdict {
				t.Errorf("verifyVerdict = %v, want %q", d.VerifyVerdict, verdict)
			}
		})
	}
}

// TestVerifyFailedBlockerOrder: the blocker sits with the phase's own facts — after
// the branch states (which stand between the operator and a retry) and before
// no-criteria (a statement about measurability).
func TestVerifyFailedBlockerOrder(t *testing.T) {
	f := newFixture(t)
	p := f.addPhase(t, 1, "Phase 1", "[]", 0, 0) // no criteria ⇒ no-criteria blocker too
	f.setRun(t, p, "done", "", 0)
	f.setVerdict(t, p, "fail", "nothing demonstrated")
	git := newGit("main").branchExists("main", branchName(p), 3, "wip").branchList()

	d, err := Diagnose(f.db, git, nil, p)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	got := kinds(d.Blockers)
	want := []string{KindBranchDirty, KindVerifyFailed, KindNoCriteria}
	if len(got) != len(want) {
		t.Fatalf("blocker kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("blocker kinds = %v, want %v", got, want)
		}
	}
}

// TestOutcomeVocabularyIsClosed proves the acceptance criterion "no new outcome
// state" structurally rather than by grep: it parses outcome.go and asserts the exact
// set of Outcome* constants. §5.4 considered `completed-unverified` and rejected it —
// a future edit that adds one has to change this list and read why.
func TestOutcomeVocabularyIsClosed(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "outcome.go", nil, 0)
	if err != nil {
		t.Fatalf("parse outcome.go: %v", err)
	}
	got := map[string]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || len(spec.Values) != 1 {
			return true
		}
		name := spec.Names[0].Name
		if !strings.HasPrefix(name, "Outcome") {
			return true
		}
		lit, ok := spec.Values[0].(*ast.BasicLit)
		if !ok {
			return true
		}
		got[name] = strings.Trim(lit.Value, `"`)
		return true
	})

	want := map[string]string{
		"OutcomeIdle":      "idle",
		"OutcomeRunning":   "running",
		"OutcomeCompleted": "completed",
		"OutcomePartial":   "partial",
		"OutcomeNoop":      "noop",
		"OutcomeFailed":    "failed",
	}
	if len(got) != len(want) {
		t.Fatalf("outcome constants = %v, want exactly %v — verification must not add a state", got, want)
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s = %q, want %q", name, got[name], value)
		}
	}
}
