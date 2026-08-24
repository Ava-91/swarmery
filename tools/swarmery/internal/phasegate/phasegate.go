// Package phasegate is THE completion gate for a plan phase: one derivation that
// answers "may this phase be recorded as complete?" and, when the answer is no,
// says why.
//
// It exists because completion used to be decided in four places from the same
// two columns (checkboxes_done / checkboxes_total) and nothing else — so a phase
// whose verification never ran read exactly like one that had been graded and
// passed. The store still holds the proof: a verification row 1.1 seconds long
// with an empty detail, and another reading "verifier did not run: fork/exec
// …/claude: no such file or directory". Both stamped INCONCLUSIVE, and both
// phases presented as done.
//
// Design notes, so this does not get widened by accident:
//
//   - The gate is ONE call returning ONE state plus reasons, not a set of
//     independent checks. An operator should see a single completion gate that can
//     cite more than one reason, rather than several half-gates each refusing for
//     its own cause. Later conditions (a mandatory Completion Report, a recorded
//     lesson) belong in Input and Result.Reasons — not in a second gate beside
//     this one.
//   - It is PURE: no DB, no clock, no git. Callers hand it the row's fields.
//   - It deliberately does NOT restate phasediag.Outcome. That vocabulary answers
//     "did work land?" and is closed on purpose (decision D5 rejected a
//     `completed-unverified` outcome, because it forks that one question in two).
//     This package answers a different question — "may we call it done?" — and
//     the two travel side by side.
//   - `fail` is NOT this gate's business. A phase graded FAIL keeps the behaviour
//     decision D5 settled: it reads as completed work with a verify-failed blocker
//     beside it. Turning FAIL into "not complete" here would silently re-open a
//     settled decision, and FAIL is already visible. The gate covers the case that
//     was invisible: an ABSENT verdict.
package phasegate

import "fmt"

// Completion states. Closed set.
const (
	// StateComplete — every condition met; the phase may be recorded as complete.
	StateComplete = "complete"
	// StateUnverified — the work is there (criteria ticked) but the grade is
	// missing or could not conclude. Distinct from complete AND from failed: the
	// work may well be fine, and nobody knows.
	StateUnverified = "unverified"
	// StateIncomplete — the criteria themselves are not met. The pre-existing
	// answer, unchanged.
	StateIncomplete = "incomplete"
)

// Verdict values as stamped on the row. Mirrors verify.Verdict without importing
// it: phasegate must stay leaf-level so both api and phaserun can depend on it.
const (
	VerdictPass         = "pass"
	VerdictFail         = "fail"
	VerdictInconclusive = "inconclusive"
)

// VerifyOff is the verify_mode value meaning "this doc never asked to be graded".
// A phase that did not opt in cannot be held to a verdict it never requested —
// gating those would stall every plan that does not use verification.
const VerifyOff = "off"

// Input is one phase's completion-relevant row fields.
type Input struct {
	CriteriaDone  int
	CriteriaTotal int
	// VerifyMode is the doc's `**Verify:**` opt-in: off | normal | strict. Empty
	// is treated as off, matching wsingest's own normalisation.
	VerifyMode string
	// VerifyVerdict is the stamped grade: "" (never graded) | pass | fail |
	// inconclusive.
	VerifyVerdict string
	// LegacyDone marks a legacy activated board task that reached done/archived.
	// Those predate acceptance-criteria counting and prove completion by their
	// column; the gate honours that rather than retroactively re-opening them.
	LegacyDone bool
}

// Result is the gate's answer. Reasons is empty exactly when State is
// StateComplete.
type Result struct {
	State   string
	Reasons []string
}

// Complete reports whether the phase may be recorded as complete.
func (r Result) Complete() bool { return r.State == StateComplete }

// VerificationRequired reports whether this phase opted into being graded.
func (in Input) VerificationRequired() bool {
	return in.VerifyMode != "" && in.VerifyMode != VerifyOff
}

// criteriaMet is the pre-existing derivation, inlined here so this package stays
// leaf-level. phasediag.CriteriaMet remains the exported name other callers use;
// the two must agree, and a test pins that.
func criteriaMet(done, total int) bool { return total > 0 && done >= total }

// Check runs the gate.
func Check(in Input) Result {
	if in.LegacyDone {
		return Result{State: StateComplete}
	}

	var reasons []string

	if !criteriaMet(in.CriteriaDone, in.CriteriaTotal) {
		if in.CriteriaTotal == 0 {
			reasons = append(reasons,
				"this phase has no acceptance-criteria checkboxes, so its completion cannot be proven")
		} else {
			reasons = append(reasons, fmt.Sprintf("%d of %d acceptance criteria are ticked",
				in.CriteriaDone, in.CriteriaTotal))
		}
		return Result{State: StateIncomplete, Reasons: reasons}
	}

	if in.VerificationRequired() {
		switch in.VerifyVerdict {
		case "":
			return Result{State: StateUnverified, Reasons: []string{
				"this phase asked to be verified (verify: " + in.VerifyMode +
					") and carries no verdict — the grade never landed, so the ticked criteria are unconfirmed",
			}}
		case VerdictInconclusive:
			return Result{State: StateUnverified, Reasons: []string{
				"verification could not conclude, so the ticked criteria are unconfirmed — " +
					"see the verify detail for whether the verifier ran at all",
			}}
		}
	}

	return Result{State: StateComplete}
}
