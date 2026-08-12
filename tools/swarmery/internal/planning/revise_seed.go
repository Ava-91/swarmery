package planning

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/phasediag"
)

// BuildEvidence renders the revise prompt's execution-evidence table for one
// workspace task and returns it together with the list of DONE phase docs
// (basenames) — the docs a revision must never touch.
//
// One row per epic_phases row: seq, doc basename, ticked/total criteria,
// run_state, the derived outcome (phasediag.OutcomeFromRow — the same
// derivation the Plans API and the diagnosis modal use, so the evidence the
// revise agent reasons over can never disagree with what the operator saw),
// the run branch, and the run error. "Done" is phasediag.CriteriaMet — the one
// exported phase-done derivation, shared with internal/api's planStatus.
func BuildEvidence(db *sql.DB, taskID int64) (string, []string, error) {
	rows, err := db.Query(`
		SELECT seq, doc_path, checkboxes_done, checkboxes_total,
		       run_state, run_error, run_branch,
		       run_checkboxes_before, run_checkboxes_after
		  FROM epic_phases
		 WHERE workspace_task_id = ?
		 ORDER BY seq, id`, taskID)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()

	var b strings.Builder
	b.WriteString("| # | Doc | Criteria | Run state | Outcome | Branch | Error |\n")
	b.WriteString("|---|-----|----------|-----------|---------|--------|-------|\n")
	var doneDocs []string
	for rows.Next() {
		var (
			seq, done, total    int
			docPath, runState   string
			runError, runBranch sql.NullString
			before, after       sql.NullInt64
		)
		if err := rows.Scan(&seq, &docPath, &done, &total,
			&runState, &runError, &runBranch, &before, &after); err != nil {
			return "", nil, err
		}
		doc := filepath.Base(docPath)
		outcome := phasediag.OutcomeFromRow(runState, total, done, before, after)
		fmt.Fprintf(&b, "| %d | `%s` | %d/%d | %s | %s | %s | %s |\n",
			seq, doc, done, total, runState, outcome,
			orDash(runBranch.String), orDash(runError.String))
		if phasediag.CriteriaMet(done, total) {
			doneDocs = append(doneDocs, doc)
		}
	}
	if err := rows.Err(); err != nil {
		return "", nil, err
	}
	return b.String(), doneDocs, nil
}

// orDash keeps the evidence table readable: an empty cell renders as an em-dash.
func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}
