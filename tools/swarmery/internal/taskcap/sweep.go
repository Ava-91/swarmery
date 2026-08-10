package taskcap

import (
	"database/sql"
	"time"
)

// DefaultInboxTTL is how long a captured card may sit untouched in triage
// before the sweeper retires it: 14 days. Long enough that a fortnight away
// does not eat a real suggestion, short enough that the inbox cannot silently
// become a 250-card graveyard again.
const DefaultInboxTTL = 336 * time.Hour

// StaleInboxWhere is the row-eligibility predicate shared by the TTL sweeper
// and the bulk-amnesty endpoint (internal/api.bulkArchiveBoardTasks). It lives
// here, as one string, because the two are the same decision at two cadences —
// a second hand-written copy in the API layer is exactly how an amnesty would
// end up archiving rows the automatic sweep is not allowed to touch.
//
// Each conjunct earns its place:
//
//   - source='queue'      — board rows only. A workspace task is owned by the
//     disk and would be resurrected by the next scan.
//   - board_column='triage' — the inbox is untriaged suggestions; anything the
//     user accepted into todo (or beyond) is work, not noise.
//   - origin IN ('session','llm') — a human-written card is a commitment, a
//     captured one is a suggestion. Only suggestions expire.
//   - worktree_path IS NULL — never touch a card the dispatcher owns. Same
//     invariant ingest's moveCapturedToReview / SweepSessionToReview carry.
const StaleInboxWhere = `source = 'queue'
		   AND board_column = 'triage'
		   AND origin IN ('session', 'llm')
		   AND worktree_path IS NULL`

// InboxIdleSince is the timestamp a card's idle clock runs from.
//
// COALESCE, not a bare column_moved_at: capture inserts a card straight into
// triage and never writes column_moved_at (see InsertCapturedTask), so every
// card that has only ever sat in the inbox — which is the entire population
// this sweep exists for — carries NULL there. Comparing NULL against a cutoff
// yields NULL, so a column_moved_at-only predicate would match precisely zero
// of the cards it was written to retire. created_at is the honest fallback: for
// an untouched card, "when it appeared" IS when its idle clock started.
const InboxIdleSince = `COALESCE(column_moved_at, created_at)`

// SweepStaleInbox archives captured cards that sat untouched in triage for
// longer than ttl, and reports how many it retired.
//
// A non-positive ttl is the off switch and returns (0, nil) without touching a
// row — the alternative reading (cutoff == now) would archive the whole inbox,
// which is the opposite of what SWARMERY_INBOX_TTL=0 asks for.
//
// The write sets three fields together: board_column='archived' is what the
// board reads, status='cancelled' says the card was retired rather than
// completed, and archived_at both dates the retirement and drops the row out of
// internal/staleness (its query is scoped to archived_at IS NULL). column_moved_at
// is refreshed too so the Archived column's sort places the sweep's output where
// a hand-archived card would land.
//
// Deliberately a single guarded UPDATE: it is race-safe against a user dragging
// one of these cards at the same moment (last write wins) and idempotent on
// replay, since a second pass finds nothing left in triage.
func SweepStaleInbox(db *sql.DB, ttl time.Duration, now time.Time) (int64, error) {
	if ttl <= 0 {
		return 0, nil
	}
	stamp := now.UTC().Format(tsFormat)
	cutoff := now.Add(-ttl).UTC().Format(tsFormat)
	res, err := db.Exec(`
		UPDATE tasks
		   SET board_column = 'archived',
		       status = 'cancelled',
		       column_moved_at = ?,
		       archived_at = ?
		 WHERE `+StaleInboxWhere+`
		   AND `+InboxIdleSince+` < ?`,
		stamp, stamp, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
