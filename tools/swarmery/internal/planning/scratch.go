package planning

// Scratch-dir hygiene (plan-revision phase 5). A revise session stages its
// proposal under <ScratchRoot>/<session uuid>/; Stage removes the dir on
// success and deliberately keeps it while the session is resumable (a
// validation failure is an interview turn). A session that dies before the
// sentinel — daemon crash, cancelled run, failed spawn — leaves the dir behind
// with nothing that will ever read it again.

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
)

// scratchOrphaned is the sweep predicate: a scratch dir is an orphan when its
// session uuid has NO planning_sessions row (never durably admitted, or the
// row was pruned) or when that row is terminal (done/failed/cancelled — a done
// revise session already staged its revision and the dir is a leftover of a
// tolerated RemoveAll failure). A non-terminal row keeps the dir: the operator
// can still resume the session and have the agent amend + re-stage it.
func scratchOrphaned(db *sql.DB, uuid string) (bool, error) {
	var status string
	err := db.QueryRow(
		`SELECT status FROM planning_sessions WHERE session_uuid = ?`, uuid).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	switch status {
	case StatusDone, StatusFailed, StatusCancelled:
		return true, nil
	}
	return false, nil
}

// SweepScratchOrphans removes every orphaned scratch dir under root and
// returns the removed dir names (the session uuids). A missing root is a clean
// no-op — the first revise session creates it. Per-dir failures skip the dir;
// the sweep never fails the caller over one stubborn entry.
func SweepScratchOrphans(db *sql.DB, root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		orphan, err := scratchOrphaned(db, e.Name())
		if err != nil {
			return removed, err
		}
		if !orphan {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
			continue // stubborn dir — the next start retries
		}
		removed = append(removed, e.Name())
	}
	return removed, nil
}
