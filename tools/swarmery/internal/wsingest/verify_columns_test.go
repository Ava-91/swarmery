package wsingest

import (
	"database/sql"
	"testing"
)

// TestParseDocVerify covers the opt-in knob's whole contract, including the two
// directions of "off": absent means the plan never asked, unrecognized means the
// author asked for something this daemon does not understand — and running a grader
// nobody specified is worse than not running one.
func TestParseDocVerify(t *testing.T) {
	for _, tc := range []struct {
		name, doc, want string
	}{
		{"absent", "# Phase 1\n\n**Repo:** /repo\n", VerifyOff},
		{"strict", "# Phase 1\n\n**Verify:** strict\n", VerifyStrict},
		{"normal", "# Phase 1\n\n**Verify:** normal\n", VerifyNormal},
		{"case-insensitive", "# Phase 1\n\n**VERIFY:** Strict\n", VerifyStrict},
		{"synonyms on/yes", "# Phase 1\n\n**Verify:** yes\n", VerifyNormal},
		{"explicit off", "# Phase 1\n\n**Verify:** off\n", VerifyOff},
		{"junk falls back to off", "# Phase 1\n\n**Verify:** paranoid\n", VerifyOff},
		{
			name: "only the header block counts",
			// A quoted agent prompt further down describes someone ELSE's phase; the
			// same bound parseDocStatus and parseDocRepo use.
			doc:  "# Phase 1\n\n**Repo:** /repo\n\n## Copy-paste agent prompt\n\n**Verify:** strict\n",
			want: VerifyOff,
		},
		{
			name: "past the 15-line window",
			doc:  "# Phase 1\n" + "\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n" + "**Verify:** strict\n",
			want: VerifyOff,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseDocVerify(tc.doc); got != tc.want {
				t.Errorf("ParseDocVerify = %q, want %q", got, tc.want)
			}
		})
	}
}

// seedVerifyState puts the daemon-owned verification trio on a phase row.
func seedVerifyState(t *testing.T, db *sql.DB, docPath, startPoint, verdict, detail string) {
	t.Helper()
	mustExec(t, db, `UPDATE epic_phases
		 SET run_start_point=?, verify_verdict=?, verify_detail=?
		 WHERE workspace_task_id=? AND doc_path=?`,
		startPoint, verdict, detail, carryTaskID, docPath)
}

func verifyStateOf(t *testing.T, db *sql.DB, docPath string) (start, verdict, detail sql.NullString) {
	t.Helper()
	if err := db.QueryRow(`SELECT run_start_point, verify_verdict, verify_detail
		 FROM epic_phases WHERE workspace_task_id=? AND doc_path=?`,
		carryTaskID, docPath).Scan(&start, &verdict, &detail); err != nil {
		t.Fatalf("read verify state of %s: %v", docPath, err)
	}
	return start, verdict, detail
}

// TestCarryAcrossRenames_CarriesTheVerificationTrio is the two-writers rule made
// executable. epic_phases identity is doc_path, so a renamed doc REPLACES the row;
// every daemon-owned column that snapshotPhases or carryAcrossRenames forgets is
// silently dropped on the next scan, with no error anywhere. A verdict lost that way
// downgrades "verified" to "never verified" — a lie the dashboard would then repeat.
func TestCarryAcrossRenames_CarriesTheVerificationTrio(t *testing.T) {
	db := carryFixture(t)
	seedPhase(t, db, 1, "Phase 1", "/plan/phase-1-old.md", "done", "uuid-1", "swarm/phase-9")
	seedVerifyState(t, db, "/plan/phase-1-old.md", "abc123", "fail", "the diff does not implement criterion 2")

	// The doc is renamed: same seq, new path.
	applyPhases(t, db, []epicPhase{phase(1, "Phase 1", "/plan/phase-1-new.md")})

	start, verdict, detail := verifyStateOf(t, db, "/plan/phase-1-new.md")
	if start.String != "abc123" {
		t.Errorf("run_start_point = %q, want abc123 — the verifier's diff base was dropped", start.String)
	}
	if verdict.String != "fail" {
		t.Errorf("verify_verdict = %q, want fail", verdict.String)
	}
	if detail.String != "the diff does not implement criterion 2" {
		t.Errorf("verify_detail = %q, want the detail carried over", detail.String)
	}
	// And the old row is gone, so nothing claims the same run twice.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM epic_phases WHERE doc_path='/plan/phase-1-old.md'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("the renamed-away row survived (%d rows)", n)
	}
}

// A verdict alone is state worth carrying: an otherwise-idle phase that has been
// graded must not lose its grade to a rename.
func TestCarryAcrossRenames_AVerdictAloneCounts(t *testing.T) {
	db := carryFixture(t)
	seedPhase(t, db, 1, "Phase 1", "/plan/p1-old.md", "idle", "", "")
	seedVerifyState(t, db, "/plan/p1-old.md", "", "pass", "looks right")

	applyPhases(t, db, []epicPhase{phase(1, "Phase 1", "/plan/p1-new.md")})

	if _, verdict, _ := verifyStateOf(t, db, "/plan/p1-new.md"); verdict.String != "pass" {
		t.Errorf("verify_verdict = %q, want pass — a graded idle phase lost its verdict", verdict.String)
	}
}

// verify_mode is DOC-owned, so it is re-derived rather than carried: whatever the
// new doc says wins, including "the author removed the opt-in".
func TestApplyEpics_VerifyModeIsDocOwned(t *testing.T) {
	db := carryFixture(t)
	p := phase(1, "Phase 1", "/plan/p1.md")
	p.verifyMode = VerifyStrict
	applyPhases(t, db, []epicPhase{p})

	var mode string
	if err := db.QueryRow(`SELECT verify_mode FROM epic_phases WHERE doc_path='/plan/p1.md'`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != VerifyStrict {
		t.Fatalf("verify_mode = %q, want strict", mode)
	}

	// The author drops the header line: the next scan turns verification back off.
	applyPhases(t, db, []epicPhase{phase(1, "Phase 1", "/plan/p1.md")})
	if err := db.QueryRow(`SELECT verify_mode FROM epic_phases WHERE doc_path='/plan/p1.md'`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != VerifyOff {
		t.Errorf("verify_mode = %q, want off after the doc stopped asking", mode)
	}
}
