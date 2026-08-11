package prune

import (
	"database/sql"
	"fmt"
	"time"
)

// Revision retention (plan-revision phase 5). Two independent windows:
//
//   - A 'staged' revision nobody decided within staleStagedWindow is moved to
//     'superseded' with decided_by='system'. The row is NEVER deleted — the
//     history list is the audit trail — but a two-week-old proposal is stale
//     by construction (the live plan kept moving) and blocking the one-open-
//     revision-per-plan slot forever would wedge the revise flow.
//
//   - Revisions decided more than contentRetention ago get their bulky
//     payloads (plan_revision_files.proposed + pre_image) nulled. The
//     metadata row and per-file action/hash audit stay intact; only the full
//     document bodies — the DB-size driver — are dropped.
const (
	staleStagedWindow = 14 * 24 * time.Hour
	contentRetention  = 90 * 24 * time.Hour
)

// RevisionStats reports one PruneRevisions pass.
type RevisionStats struct {
	Superseded    int64 // staged rows moved to superseded (decided_by='system')
	ContentNulled int64 // plan_revision_files rows whose proposed/pre_image were nulled
}

// PruneRevisions applies revision retention as of now. Timestamps are the
// stored RFC3339 UTC strings, so the cutoffs compare lexicographically.
func PruneRevisions(db *sql.DB, now time.Time) (RevisionStats, error) {
	var st RevisionStats
	ts := now.UTC().Format(time.RFC3339)
	staleCutoff := now.Add(-staleStagedWindow).UTC().Format(time.RFC3339)
	contentCutoff := now.Add(-contentRetention).UTC().Format(time.RFC3339)

	res, err := db.Exec(`
		UPDATE plan_revisions SET status = 'superseded', decided_by = 'system', decided_at = ?
		 WHERE status = 'staged' AND created_at < ?`, ts, staleCutoff)
	if err != nil {
		return st, fmt.Errorf("prune revisions: supersede stale staged: %w", err)
	}
	st.Superseded, _ = res.RowsAffected()

	res, err = db.Exec(`
		UPDATE plan_revision_files SET proposed = NULL, pre_image = NULL
		 WHERE (proposed IS NOT NULL OR pre_image IS NOT NULL)
		   AND revision_id IN (
			SELECT id FROM plan_revisions WHERE decided_at IS NOT NULL AND decided_at < ?)`,
		contentCutoff)
	if err != nil {
		return st, fmt.Errorf("prune revisions: null old content: %w", err)
	}
	st.ContentNulled, _ = res.RowsAffected()
	return st, nil
}
