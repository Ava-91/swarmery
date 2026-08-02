package api_test

// Phase 4, signal 2 at the kill call site. Board capture's lifecycle sweep has
// to fire here for the same reason hook B does (see prockill_capture_test.go):
// 'killed' is terminal and the ingest status ticker only ever scans
// `status IN ('active','idle')`, so no later pass revisits a row this handler
// wrote. Without the call, killing a session leaves every card the user accepted
// from it parked in In Progress forever, claiming a dead session is still
// working on them.

import (
	"database/sql"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/api"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/ingest"
)

// killCardSeq keeps seeded external_ids distinct; a counter is the one generator
// that cannot collide.
var killCardSeq atomic.Int64

// killParkedAt is a stamp old enough that "was column_moved_at rewritten?" is a
// comparison rather than a race with the clock.
const killParkedAt = "2020-01-01T00:00:00.000Z"

// TestKillSession_SweepsAcceptedCardsToReview: killing a session moves the cards
// it captured AND the user accepted to In Review — and nothing else. The row set
// deliberately surrounds the two movers with every near miss the guard has to
// reject, above all a dispatcher-owned card carrying the SAME origin_session_id:
// dispatch/service.go drives those rows through its own exit routing, and
// `origin != 'manual'` in the sweep is the only thing keeping the two state
// machines from writing over each other.
func TestKillSession_SweepsAcceptedCardsToReview(t *testing.T) {
	h := openKillTestDB(t)

	bus := ingest.NewBus()
	api.AttachBus(bus)
	t.Cleanup(func() { api.AttachBus(nil) })
	notes, unsubscribe := bus.Subscribe(32)
	t.Cleanup(unsubscribe)

	const (
		sessionID   = 11
		sessionUUID = "kill-sweep-uuid"
	)
	mustExecKill(t, h.DB, `INSERT INTO sessions
		(id, project_id, session_uuid, status, started_at, source, pid, proc_state)
		VALUES (?, 1, ?, 'active', '2024-01-01T00:00:00Z', 'jsonl', ?, 'running')`,
		sessionID, sessionUUID, startFakeClaude(t))
	// One assistant turn + an opening prompt would let hook B mint a fallback
	// card; both are omitted so this test observes the SWEEP alone and the
	// task_updated frame count below is unambiguous.

	accepted1 := seedKillCard(t, h.DB, sessionID, "todo:kill:1", "in_progress", "session", "accepted one")
	accepted2 := seedKillCard(t, h.DB, sessionID, "todo:kill:2", "in_progress", "session", "accepted two")
	suggestion := seedKillCard(t, h.DB, sessionID, "todo:kill:3", "triage", "session", "never accepted")
	dispatcher := seedKillCard(t, h.DB, sessionID, "", "in_progress", "manual", "dispatcher owned")

	// force=true is SIGKILL, which needs no escalation — the handler leaves no
	// timer goroutine behind and the test stays deterministic.
	if w := postKillBody(t, h, "11", `{"force":true}`); w.Code != http.StatusAccepted {
		t.Fatalf("kill = %d, want 202; body: %s", w.Code, w.Body.String())
	}

	for _, id := range []int64{accepted1, accepted2} {
		column, movedAt := killCardState(t, h.DB, id)
		if column != "in_review" {
			t.Errorf("accepted card %d board_column = %q, want in_review", id, column)
		}
		if movedAt == killParkedAt {
			t.Errorf("accepted card %d column_moved_at was not restamped", id)
		}
	}
	for _, tc := range []struct {
		id   int64
		want string
		why  string
	}{
		{suggestion, "triage", "a suggestion the user never accepted does not expire with its session"},
		{dispatcher, "in_progress", "origin='manual' is dispatcher-owned and must never be auto-moved"},
	} {
		column, movedAt := killCardState(t, h.DB, tc.id)
		if column != tc.want {
			t.Errorf("card %d board_column = %q, want %q — %s", tc.id, column, tc.want, tc.why)
		}
		if movedAt != killParkedAt {
			t.Errorf("card %d column_moved_at = %q, want it untouched — %s", tc.id, movedAt, tc.why)
		}
	}

	// --- Exactly one task_updated frame per moved card ------------------------
	frames := drainTaskFrames(t, notes)
	if len(frames) != 2 {
		t.Fatalf("task_updated frames = %d, want exactly 2 — one per moved card", len(frames))
	}
	got := map[int64]bool{frames[0].TaskID: true, frames[1].TaskID: true}
	if !got[accepted1] || !got[accepted2] {
		t.Errorf("frames = %+v, want task_updated for cards %d and %d", frames, accepted1, accepted2)
	}

	// --- A repeat kill sweeps nothing and stays off the wire ------------------
	// The first kill left proc_state='dead', which the handler 409s on, so
	// re-arm the row with a fresh live process to reach the sweep again.
	mustExecKill(t, h.DB,
		`UPDATE sessions SET proc_state = 'running', pid = ? WHERE id = ?`,
		startFakeClaude(t), sessionID)
	if w := postKillBody(t, h, "11", `{"force":true}`); w.Code != http.StatusAccepted {
		t.Fatalf("second kill = %d, want 202; body: %s", w.Code, w.Body.String())
	}
	if extra := drainTaskFrames(t, notes); len(extra) != 0 {
		t.Errorf("repeat kill published %d task_updated frames, want 0 — the cards already moved",
			len(extra))
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

// seedKillCard plants a board row directly: the states this test needs (a
// dispatcher-owned card, a card the user accepted) are ones a capture run cannot
// produce on its own. An empty captureKey stores NULL — what a hand-written card
// carries.
func seedKillCard(t *testing.T, db *sql.DB, sessionID int64, captureKey, column, origin, title string) int64 {
	t.Helper()
	var key any
	if captureKey != "" {
		key = captureKey
	}
	res, err := db.Exec(`
		INSERT INTO tasks (project_id, title, prompt, priority, status, created_at,
		                   source, external_id, board_column, file_scope, dependencies,
		                   column_moved_at, origin, origin_session_id, capture_key)
		VALUES (1, ?, 'seeded', 5, 'queued', ?, 'queue', ?, ?, '[]', '[]', ?, ?, ?, ?)`,
		title, killParkedAt, fmt.Sprintf("T-k%05d", killCardSeq.Add(1)), column, killParkedAt,
		origin, sessionID, key)
	if err != nil {
		t.Fatalf("seed card %q: %v", title, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func killCardState(t *testing.T, db *sql.DB, id int64) (column, movedAt string) {
	t.Helper()
	var moved sql.NullString
	if err := db.QueryRow(
		`SELECT board_column, column_moved_at FROM tasks WHERE id = ?`, id).Scan(&column, &moved); err != nil {
		t.Fatalf("read card %d: %v", id, err)
	}
	return column, moved.String
}
