package taskcap_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/taskcap"
)

// sweepNow is the fixed "now" every sweep case is measured against, so a case's
// age is a property of its seeded timestamps and never of the wall clock.
var sweepNow = time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)

// ts renders an instant in the millisecond-Z format every board timestamp uses
// (api.boardTSFormat / taskcap's own tsFormat) — the format the sweep's lexical
// `<` comparison depends on.
func ts(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// ago is a timestamp d before sweepNow.
func ago(d time.Duration) string { return ts(sweepNow.Add(-d)) }

// card is the shape of one seeded board row. Every field the sweep's WHERE
// clause reads is explicit, so a case states exactly which guard it exercises.
type card struct {
	source       string // 'queue' (board) | 'workspace'
	origin       string // manual | session | llm
	column       string
	createdAt    string
	movedAt      any // nil ⇒ NULL — the shape EVERY captured card actually has
	worktreePath any // nil ⇒ NULL
}

// seedCard inserts one board row and returns its id. Deliberately raw SQL
// rather than InsertCapturedTask: the cases need shapes that write path cannot
// produce (a manual card, an already-moved card, a card the dispatcher owns).
func seedCard(t *testing.T, db *sql.DB, projectID int64, extID string, c card) int64 {
	t.Helper()
	res, err := db.Exec(`
		INSERT INTO tasks (project_id, title, prompt, priority, status, created_at,
		                   source, external_id, board_column, file_scope, dependencies,
		                   origin, column_moved_at, worktree_path)
		VALUES (?, ?, 'p', 5, 'queued', ?, ?, ?, ?, '[]', '[]', ?, ?, ?)`,
		projectID, extID, c.createdAt, c.source, extID, c.column,
		c.origin, c.movedAt, c.worktreePath)
	if err != nil {
		t.Fatalf("seed %s: %v", extID, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func columnOf(t *testing.T, db *sql.DB, id int64) (column, status string, archivedAt sql.NullString) {
	t.Helper()
	if err := db.QueryRow(
		`SELECT board_column, status, archived_at FROM tasks WHERE id = ?`, id,
	).Scan(&column, &status, &archivedAt); err != nil {
		t.Fatalf("read task %d: %v", id, err)
	}
	return column, status, archivedAt
}

// TestSweepStaleInboxArchivesIdleCaptured is the feature: a captured card that
// sat untouched in triage past the TTL is a suggestion nobody wanted, and the
// sweep retires it. Both capture origins qualify.
func TestSweepStaleInboxArchivesIdleCaptured(t *testing.T) {
	db, projectID, _ := testDB(t)

	// The real graveyard shape: capture never writes column_moved_at, so an
	// untouched captured card carries NULL there and only created_at dates it.
	// A sweep keyed on column_moved_at alone would match none of them.
	sessionCard := seedCard(t, db, projectID, "T-sess01", card{
		source: "queue", origin: "session", column: "triage",
		createdAt: ago(30 * 24 * time.Hour), movedAt: nil,
	})
	llmCard := seedCard(t, db, projectID, "T-llm001", card{
		source: "queue", origin: "llm", column: "triage",
		createdAt: ago(60 * 24 * time.Hour), movedAt: nil,
	})
	// A captured card the user DID touch (dragged out and back), so its idle
	// clock restarted at column_moved_at rather than created_at.
	movedLongAgo := seedCard(t, db, projectID, "T-moved1", card{
		source: "queue", origin: "session", column: "triage",
		createdAt: ago(90 * 24 * time.Hour), movedAt: ago(20 * 24 * time.Hour),
	})

	n, err := taskcap.SweepStaleInbox(db, 14*24*time.Hour, sweepNow)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 3 {
		t.Fatalf("archived = %d, want 3", n)
	}
	for _, id := range []int64{sessionCard, llmCard, movedLongAgo} {
		column, status, archivedAt := columnOf(t, db, id)
		if column != "archived" || status != "cancelled" {
			t.Errorf("task %d = %s/%s, want archived/cancelled", id, column, status)
		}
		if !archivedAt.Valid || archivedAt.String != ts(sweepNow) {
			t.Errorf("task %d archived_at = %v, want %s", id, archivedAt, ts(sweepNow))
		}
	}

	// Idempotent: nothing is left in triage, so a second pass is a no-op.
	if again, err := taskcap.SweepStaleInbox(db, 14*24*time.Hour, sweepNow); err != nil || again != 0 {
		t.Errorf("second sweep = (%d, %v), want (0, nil)", again, err)
	}
}

// TestSweepStaleInboxExclusions pins the four things the sweep must never
// touch. Each case is seeded alongside one card that MUST be archived, so a
// sweep that silently matched nothing at all cannot pass.
func TestSweepStaleInboxExclusions(t *testing.T) {
	cases := map[string]card{
		// A human-written card is a commitment, not a suggestion.
		"manual origin": {
			source: "queue", origin: "manual", column: "triage",
			createdAt: ago(90 * 24 * time.Hour), movedAt: nil,
		},
		// Accepted work: the inbox TTL is about triage only.
		"non-triage column": {
			source: "queue", origin: "session", column: "todo",
			createdAt: ago(90 * 24 * time.Hour), movedAt: ago(90 * 24 * time.Hour),
		},
		// The dispatcher owns this row — same guard ingest's sweepers carry.
		"worktree owned": {
			source: "queue", origin: "session", column: "triage",
			createdAt: ago(90 * 24 * time.Hour), movedAt: nil,
			worktreePath: "/tmp/wt/T-x",
		},
		// Younger than the TTL: still a live suggestion.
		"fresh": {
			source: "queue", origin: "session", column: "triage",
			createdAt: ago(13 * 24 * time.Hour), movedAt: nil,
		},
		// Not a board row at all — workspace tasks are owned by the disk.
		"workspace source": {
			source: "workspace", origin: "session", column: "triage",
			createdAt: ago(90 * 24 * time.Hour), movedAt: nil,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			db, projectID, _ := testDB(t)
			protected := seedCard(t, db, projectID, "T-prot01", c)
			doomed := seedCard(t, db, projectID, "T-doom01", card{
				source: "queue", origin: "session", column: "triage",
				createdAt: ago(90 * 24 * time.Hour), movedAt: nil,
			})

			n, err := taskcap.SweepStaleInbox(db, 14*24*time.Hour, sweepNow)
			if err != nil {
				t.Fatalf("sweep: %v", err)
			}
			if n != 1 {
				t.Fatalf("archived = %d, want exactly the 1 doomed card", n)
			}
			if column, _, _ := columnOf(t, db, protected); column != c.column {
				t.Errorf("protected card moved to %q, want it left in %q", column, c.column)
			}
			if column, _, _ := columnOf(t, db, doomed); column != "archived" {
				t.Errorf("doomed card = %q, want archived", column)
			}
		})
	}
}

// TestSweepStaleInboxDisabled: a non-positive TTL is the off switch. Without
// this guard ttl=0 would set the cutoff to `now` and archive the entire inbox —
// the exact opposite of "disabled".
func TestSweepStaleInboxDisabled(t *testing.T) {
	db, projectID, _ := testDB(t)
	id := seedCard(t, db, projectID, "T-off001", card{
		source: "queue", origin: "session", column: "triage",
		createdAt: ago(365 * 24 * time.Hour), movedAt: nil,
	})
	for _, ttl := range []time.Duration{0, -time.Hour} {
		n, err := taskcap.SweepStaleInbox(db, ttl, sweepNow)
		if err != nil || n != 0 {
			t.Errorf("sweep(ttl=%s) = (%d, %v), want (0, nil)", ttl, n, err)
		}
	}
	if column, _, _ := columnOf(t, db, id); column != "triage" {
		t.Errorf("card = %q, want it untouched in triage", column)
	}
}
