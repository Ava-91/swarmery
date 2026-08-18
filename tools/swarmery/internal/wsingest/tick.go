// Auto-tick (Plans page): once a board task minted from a plan phase reaches
// the done column, the board verdict is the source of truth — but plan progress
// is counted from the phase doc's acceptance-criteria checkboxes, so a done
// phase would keep reading 0/N (e.g. a PREMISE STALE early exit never touches
// the doc). TickPhaseChecklist closes that seam by checking every remaining box
// in the doc. Only the md file is written (md = truth): the plan-dir watcher
// folds the new counts into epic_phases and publishes plan_updated, so the UI
// updates through the normal live path.

package wsingest

import (
	"database/sql"
	"errors"
	"os"
	"strings"
)

// TickPhaseChecklist checks every unchecked acceptance-criteria checkbox in the
// doc of the phase whose activated board task is boardTaskID. Returns how many
// boxes it ticked. A task not minted from a phase, or a doc with no unchecked
// boxes, is a (0, nil) no-op. Line matching mirrors CountCheckboxes exactly, so
// a tick never changes the total.
func TickPhaseChecklist(db *sql.DB, boardTaskID int64) (int, error) {
	var docPath string
	err := db.QueryRow(
		`SELECT doc_path FROM epic_phases WHERE activated_board_task_id = ?`, boardTaskID).
		Scan(&docPath)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	body, err := os.ReadFile(docPath)
	if err != nil {
		return 0, err
	}
	out, n := tickAllCheckboxes(string(body))
	if n == 0 {
		return 0, nil
	}
	if err := os.WriteFile(docPath, []byte(out), 0o644); err != nil {
		return 0, err
	}
	return n, nil
}

// tickAllCheckboxes flips every `- [ ]` line to `- [x]`, returning the new text
// and the number of lines flipped. Pure; unit-tested.
func tickAllCheckboxes(text string) (string, int) {
	lines := strings.Split(text, "\n")
	n := 0
	// Lines inside fenced code blocks are skipped for the same reason
	// CountCheckboxes skips them, and here the stakes are higher: a doc quoting a
	// template would have its EXAMPLE rewritten on disk, silently turning the
	// documentation of an empty checklist into a ticked one.
	forEachLineOutsideFences(text, func(i int, line string) {
		loc := checkboxRe.FindStringSubmatchIndex(line)
		if loc == nil {
			return
		}
		if line[loc[2]:loc[3]] == " " {
			lines[i] = line[:loc[2]] + "x" + line[loc[3]:]
			n++
		}
	})
	return strings.Join(lines, "\n"), n
}
