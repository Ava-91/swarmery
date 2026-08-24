package phasegate

import (
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	for _, tc := range []struct {
		name      string
		in        Input
		want      string
		wantWords string
	}{
		{
			name: "criteria met, verification never asked for",
			in:   Input{CriteriaDone: 5, CriteriaTotal: 5, VerifyMode: "off"},
			want: StateComplete,
		},
		{
			name: "criteria met, verify_mode empty is the same as off",
			in:   Input{CriteriaDone: 5, CriteriaTotal: 5},
			want: StateComplete,
		},
		{
			name: "criteria met and graded pass",
			in:   Input{CriteriaDone: 5, CriteriaTotal: 5, VerifyMode: "normal", VerifyVerdict: VerdictPass},
			want: StateComplete,
		},
		{
			// THE case this package exists for: the doc asked to be graded, the grade
			// never landed, and the phase used to read as done anyway.
			name:      "criteria met, verification asked for, no verdict",
			in:        Input{CriteriaDone: 5, CriteriaTotal: 5, VerifyMode: "normal"},
			want:      StateUnverified,
			wantWords: "no verdict",
		},
		{
			name:      "criteria met, verification could not conclude",
			in:        Input{CriteriaDone: 5, CriteriaTotal: 5, VerifyMode: "strict", VerifyVerdict: VerdictInconclusive},
			want:      StateUnverified,
			wantWords: "could not conclude",
		},
		{
			// Decision D5 stands: a FAILED grade is completed work with a
			// verify-failed blocker beside it. This gate covers the ABSENCE of a
			// grade, not a grade against the work.
			name: "criteria met, graded fail — still complete, blocker lives elsewhere",
			in:   Input{CriteriaDone: 5, CriteriaTotal: 5, VerifyMode: "strict", VerifyVerdict: VerdictFail},
			want: StateComplete,
		},
		{
			name:      "criteria not met",
			in:        Input{CriteriaDone: 2, CriteriaTotal: 5, VerifyMode: "normal", VerifyVerdict: VerdictPass},
			want:      StateIncomplete,
			wantWords: "2 of 5",
		},
		{
			name:      "no criteria at all is unprovable, not complete",
			in:        Input{CriteriaTotal: 0},
			want:      StateIncomplete,
			wantWords: "cannot be proven",
		},
		{
			// Legacy activated board tasks predate criteria counting; re-opening them
			// would rewrite history rather than gate new work.
			name: "legacy board-done phase",
			in:   Input{CriteriaTotal: 0, LegacyDone: true},
			want: StateComplete,
		},
		{
			name: "legacy done wins over a missing verdict",
			in:   Input{CriteriaDone: 5, CriteriaTotal: 5, VerifyMode: "strict", LegacyDone: true},
			want: StateComplete,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Check(tc.in)
			if got.State != tc.want {
				t.Fatalf("State = %q, want %q (reasons: %v)", got.State, tc.want, got.Reasons)
			}
			if got.Complete() != (tc.want == StateComplete) {
				t.Errorf("Complete() = %v for state %q", got.Complete(), got.State)
			}
			if tc.want == StateComplete && len(got.Reasons) != 0 {
				t.Errorf("a complete result must carry no reasons; got %v", got.Reasons)
			}
			if tc.want != StateComplete && len(got.Reasons) == 0 {
				t.Error("a refused result must say why")
			}
			if tc.wantWords != "" && !strings.Contains(strings.Join(got.Reasons, " "), tc.wantWords) {
				t.Errorf("reasons %v do not mention %q", got.Reasons, tc.wantWords)
			}
		})
	}
}

// Unverified must be its own state — collapsing it into either neighbour is the
// bug. Into `complete` and the work ships ungraded; into `failed`/`incomplete`
// and an infra hiccup reads as a real test failure, which is the distinction
// verify.ParseVerdict's bias-to-reject exists to protect.
func TestUnverifiedIsDistinctFromCompleteAndIncomplete(t *testing.T) {
	unverified := Check(Input{CriteriaDone: 3, CriteriaTotal: 3, VerifyMode: "normal"})
	complete := Check(Input{CriteriaDone: 3, CriteriaTotal: 3, VerifyMode: "normal", VerifyVerdict: VerdictPass})
	incomplete := Check(Input{CriteriaDone: 1, CriteriaTotal: 3, VerifyMode: "normal"})

	if unverified.State == complete.State {
		t.Error("an unverified phase reads the same as a verified one")
	}
	if unverified.State == incomplete.State {
		t.Error("an unverified phase reads the same as one with unticked criteria")
	}
	if unverified.Complete() {
		t.Error("an unverified phase must not pass the gate")
	}
}

// VerificationRequired is the knob that keeps the gate off every plan that never
// asked for grading — without it, this change would stall the fleet.
func TestVerificationRequired(t *testing.T) {
	for mode, want := range map[string]bool{"": false, "off": false, "normal": true, "strict": true} {
		if got := (Input{VerifyMode: mode}).VerificationRequired(); got != want {
			t.Errorf("VerificationRequired(%q) = %v, want %v", mode, got, want)
		}
	}
}
