package wsingest

import "errors"

// PlanPhase is one row of a README phase-sequencing table as ParsePlanTable
// returns it: the exported mirror of the scanner-internal epicPhase, holding
// only what the table itself states (no filesystem joins). Doc is the bare
// filename from the Doc cell — never an absolute path.
type PlanPhase struct {
	Seq       int
	Name      string
	Doc       string
	DependsOn []int
	Repo      string
}

// ErrNoPlanTable marks a README with no recognizable phase-sequencing table.
var ErrNoPlanTable = errors.New("wsingest: no phase-sequencing table in README")

// ParsePlanTable parses the README phase-sequencing table with THE parser the
// epic scanner uses (parsePlanTable — same header detection, Doc-cell
// extraction, and "Depends on" integer scan), so a validator judging a
// proposed README can never accept a table the scanner would read differently.
// Returns ErrNoPlanTable when the README carries no recognizable table (the
// scanner's fallback there is one-phase-per-doc; callers decide their own).
//
// Note the raw DependsOn: unlike parsePlan, no pruneDanglingDeps runs here —
// the caller sees every integer the cell quoted, which is exactly what a
// strict validator wants to reject.
func ParsePlanTable(readme string) ([]PlanPhase, error) {
	rows := parsePlanTable(readme)
	if len(rows) == 0 {
		return nil, ErrNoPlanTable
	}
	out := make([]PlanPhase, len(rows))
	for i, r := range rows {
		out[i] = PlanPhase{Seq: r.seq, Name: r.name, Doc: r.docPath, DependsOn: r.dependsOn, Repo: r.repo}
	}
	return out, nil
}
