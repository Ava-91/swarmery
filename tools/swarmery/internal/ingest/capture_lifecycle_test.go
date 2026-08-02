package ingest

// Phase 4 — capture lifecycle sync. Once the user ACCEPTS a captured card into
// In Progress, the daemon owns getting it out again: the card moves to In Review
// when the source todo completes (signal 1) or the source session ends (signal
// 2). Everything else about the board stays manual.
//
// The property every test in this file is really defending is the scope guard.
// Auto-move writes to a table the dispatcher also drives, and the two state
// machines are kept disjoint by TWO predicates: `origin != 'manual'` excludes
// hand-written cards, and `worktree_path IS NULL` excludes the rows the
// dispatcher is running RIGHT NOW — including a captured card it re-admitted
// after a rework, which keeps its origin and capture_key forever and so is
// invisible to the origin predicate. A bug that widened either would not look
// like a bug; it would look like the dispatcher losing track of its own runs. So
// both are asserted head-on, with rows built to satisfy every OTHER condition
// the statements test, rather than merely implied by tests that happen not to
// create such rows — and each of those tests carries a positive control, because
// a guard that just switched auto-move off would otherwise pass them all.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

const (
	// lifecycleTodo is the first todo of testdata/fixtures/todo-capture-session.jsonl,
	// so the hand-built blocks below and the transcript-driven test at the bottom
	// address the SAME card.
	lifecycleTodo = "Extract the retry helper into internal/retry"
	// parkedAt is a stamp far enough in the past that "did the move rewrite
	// column_moved_at?" is answerable by comparison rather than by timing.
	parkedAt = "2020-01-01T00:00:00.000Z"
)

// ── signal 1: the source todo completes ──────────────────────────────────────

// TestCompletedTodoMovesAcceptedCardToReview is the happy path of signal 1: a
// card the user accepted, whose todo then finished, ends up in In Review with a
// fresh column_moved_at and exactly ONE announcement.
//
// It runs hook A the way ingest does — inside a transaction — which is also the
// deadlock regression: store.Open caps the pool at a single connection, so if
// moveCapturedToReview ever took the *sql.DB handle instead of the batch's tx,
// this test would not fail, it would HANG waiting for a connection the
// transaction below is holding.
func TestCompletedTodoMovesAcceptedCardToReview(t *testing.T) {
	db := testDB(t)
	seedCaptureSession(t, db, captureSessionUUID)
	projectID := projectIDOf(t, db, captureSessionUUID)
	sessionID := sessionIDByUUID(t, db, captureSessionUUID)
	key := todoCaptureKey(captureSessionUUID, lifecycleTodo)

	// Batch 1 — the todo is in flight, so one suggestion lands in triage.
	if ids := captureBatch(t, db, projectID, sessionID,
		todoBlockStatus(t, "in_progress", lifecycleTodo)); len(ids) != 1 {
		t.Fatalf("first batch announced %v, want exactly the one card it inserted", ids)
	}
	cardID := acceptCard(t, db, key) // the user drags it to In Progress

	// Batch 2 — the todo completes.
	ids := captureBatch(t, db, projectID, sessionID,
		todoBlockStatus(t, todoStatusCompleted, lifecycleTodo))
	if len(ids) != 1 || ids[0] != cardID {
		t.Fatalf("completed todo announced %v, want exactly [%d] — one frame per moved card", ids, cardID)
	}

	got := cardByKey(t, db, key)
	if got.column != "in_review" {
		t.Errorf("board_column = %q, want in_review", got.column)
	}
	if !got.movedAt.Valid || got.movedAt.String == parkedAt {
		t.Errorf("column_moved_at = %v, want a fresh stamp (was %q before the move)", got.movedAt, parkedAt)
	}
	// The board sorts and compares column_moved_at lexically, so an auto-move
	// stamped in a different shape than a user drag writes would order wrong in
	// the column it lands in.
	if _, err := time.Parse(captureTSFormat, got.movedAt.String); err != nil {
		t.Errorf("column_moved_at = %q does not parse as the board stamp format: %v", got.movedAt.String, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM tasks`); n != 1 {
		t.Errorf("tasks = %d, want 1 — a completed todo moves its card, it does not mint another", n)
	}
}

// TestCompletedTodoLeavesUnacceptedAndFinishedCardsAlone pins the two halves of
// the board the daemon may not touch: columns BEFORE acceptance (a suggestion
// does not expire just because the session that suggested it moved on) and
// columns AFTER review (a finished card must never be resurrected into a live
// column by a re-tail of an old transcript).
func TestCompletedTodoLeavesUnacceptedAndFinishedCardsAlone(t *testing.T) {
	for _, column := range []string{"triage", "todo", "in_review", "done", "archived"} {
		t.Run(column, func(t *testing.T) {
			db := testDB(t)
			seedCaptureSession(t, db, captureSessionUUID)
			projectID := projectIDOf(t, db, captureSessionUUID)
			sessionID := sessionIDByUUID(t, db, captureSessionUUID)
			key := todoCaptureKey(captureSessionUUID, lifecycleTodo)

			captureBatch(t, db, projectID, sessionID,
				todoBlockStatus(t, "in_progress", lifecycleTodo))
			mustExecCapture(t, db,
				`UPDATE tasks SET board_column = ?, column_moved_at = ? WHERE capture_key = ?`,
				column, parkedAt, key)

			ids := captureBatch(t, db, projectID, sessionID,
				todoBlockStatus(t, todoStatusCompleted, lifecycleTodo))
			if len(ids) != 0 {
				t.Errorf("a completed todo announced %v for a card in %q, want nothing", ids, column)
			}
			got := cardByKey(t, db, key)
			if got.column != column {
				t.Errorf("board_column = %q, want %q — only in_progress cards auto-move", got.column, column)
			}
			if got.movedAt.String != parkedAt {
				t.Errorf("column_moved_at = %v, want it untouched at %q", got.movedAt, parkedAt)
			}
		})
	}
}

// TestCompletedTodoMoveIsIdempotent: ingest replays constantly — a re-tail, a
// daemon restart, an offset reset all re-read the same completed todo. The
// guarded UPDATE matches nothing the second time (the card is already
// in_review), so the batch announces nothing and no connected dashboard sees a
// re-ingest as live board activity.
func TestCompletedTodoMoveIsIdempotent(t *testing.T) {
	db := testDB(t)
	seedCaptureSession(t, db, captureSessionUUID)
	projectID := projectIDOf(t, db, captureSessionUUID)
	sessionID := sessionIDByUUID(t, db, captureSessionUUID)
	key := todoCaptureKey(captureSessionUUID, lifecycleTodo)

	captureBatch(t, db, projectID, sessionID, todoBlockStatus(t, "in_progress", lifecycleTodo))
	acceptCard(t, db, key)

	done := todoBlockStatus(t, todoStatusCompleted, lifecycleTodo)
	if ids := captureBatch(t, db, projectID, sessionID, done); len(ids) != 1 {
		t.Fatalf("first completed batch announced %v, want one moved card", ids)
	}
	movedAt := cardByKey(t, db, key).movedAt.String

	for pass := 2; pass <= 3; pass++ {
		if ids := captureBatch(t, db, projectID, sessionID, done); len(ids) != 0 {
			t.Errorf("replay pass %d announced %v, want nothing — a replay changes no row", pass, ids)
		}
	}
	got := cardByKey(t, db, key)
	if got.column != "in_review" || got.movedAt.String != movedAt {
		t.Errorf("after replay card = %+v, want it parked at in_review/%q", got, movedAt)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM tasks`); n != 1 {
		t.Errorf("tasks after replay = %d, want 1", n)
	}
}

// TestCompletedTodoMovesAcceptedCardFromTranscript is the same signal driven by
// real transcripts instead of hand-built blocks, and it additionally proves the
// move is keyed by the NORMALIZED todo text: the replay fixture writes the same
// todo as "  extract the RETRY helper into internal/retry  ", which has to
// resolve to the card the first transcript created or a completed todo would
// silently move nothing.
func TestCompletedTodoMovesAcceptedCardFromTranscript(t *testing.T) {
	db := testDB(t)
	ingestFixture(t, db, "todo-capture-session.jsonl") // three todos → three triage cards
	sessionID := sessionIDByUUID(t, db, captureSessionUUID)
	key := todoCaptureKey(captureSessionUUID, lifecycleTodo)
	cardID := acceptCard(t, db, key)

	// The same session, later: todo #1 is completed, #2 in flight, #3 pending.
	ingestFixture(t, db, "todo-capture-replay.jsonl")

	if got := cardByKey(t, db, key); got.column != "in_review" {
		t.Errorf("accepted card board_column = %q, want in_review", got.column)
	}
	// The other two are still suggestions and stay exactly where they were.
	if n := count(t, db,
		`SELECT COUNT(*) FROM tasks WHERE origin_session_id = ? AND board_column = 'triage'`,
		sessionID); n != 2 {
		t.Errorf("triage cards = %d, want the 2 unaccepted suggestions left alone", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM tasks`); n != 3 {
		t.Errorf("tasks = %d, want 3 — the replay re-read known todos, it did not mint cards", n)
	}

	// A third read of the same bytes changes nothing.
	ingestFixture(t, db, "todo-capture-replay.jsonl")
	if got := cardByKey(t, db, key); got.column != "in_review" || got.id != cardID {
		t.Errorf("after a second replay card = %+v, want the same row still at in_review", got)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM tasks`); n != 3 {
		t.Errorf("tasks after a second replay = %d, want 3", n)
	}
}

// ── signal 2: the source session ends ────────────────────────────────────────

// TestSweepSessionToReviewMovesOnlyAcceptedCards: a session reaching a terminal
// status hands back precisely the cards it should — every accepted card of THAT
// session and nothing else. The fixture deliberately surrounds the two movers
// with every near miss: an unaccepted suggestion, a finished card, a
// dispatcher-owned row, and an accepted card belonging to a different session.
func TestSweepSessionToReviewMovesOnlyAcceptedCards(t *testing.T) {
	db := testDB(t)
	seedCaptureSession(t, db, captureSessionUUID)
	projectID := projectIDOf(t, db, captureSessionUUID)
	sessionID := sessionIDByUUID(t, db, captureSessionUUID)

	other := "aabbccdd-9999-4000-8000-000000000009"
	mustExecCapture(t, db, `
		INSERT INTO sessions (project_id, session_uuid, status, started_at, cwd, source)
		VALUES (?, ?, 'active', '2026-07-10T09:00:00.000Z', ?, 'jsonl')`,
		projectID, other, captureProjectPath)
	otherSessionID := sessionIDByUUID(t, db, other)

	accepted1 := seedCard(t, db, cardSpec{projectID: projectID, sessionID: sessionID,
		key: "todo:a:1", column: "in_progress", origin: "session", title: "accepted one"})
	accepted2 := seedCard(t, db, cardSpec{projectID: projectID, sessionID: sessionID,
		key: "todo:a:2", column: "in_progress", origin: "session", title: "accepted two"})
	suggestion := seedCard(t, db, cardSpec{projectID: projectID, sessionID: sessionID,
		key: "todo:a:3", column: "triage", origin: "session", title: "never accepted"})
	finished := seedCard(t, db, cardSpec{projectID: projectID, sessionID: sessionID,
		key: "todo:a:4", column: "done", origin: "session", title: "already finished"})
	dispatcher := seedCard(t, db, cardSpec{projectID: projectID, sessionID: sessionID,
		column: "in_progress", origin: "manual", title: "dispatcher owned"})
	elsewhere := seedCard(t, db, cardSpec{projectID: projectID, sessionID: otherSessionID,
		key: "todo:b:1", column: "in_progress", origin: "session", title: "another session"})

	moved := SweepSessionToReview(db, sessionID)
	if len(moved) != 2 || moved[0] != accepted1 || moved[1] != accepted2 {
		t.Fatalf("sweep returned %v, want exactly [%d %d] in id order", moved, accepted1, accepted2)
	}
	for _, id := range []int64{accepted1, accepted2} {
		got := cardByID(t, db, id)
		if got.column != "in_review" {
			t.Errorf("card %d board_column = %q, want in_review", id, got.column)
		}
		if !got.movedAt.Valid || got.movedAt.String == parkedAt {
			t.Errorf("card %d column_moved_at = %v, want a fresh stamp", id, got.movedAt)
		}
	}
	for _, tc := range []struct {
		id   int64
		want string
		why  string
	}{
		{suggestion, "triage", "an unaccepted suggestion does not expire with its session"},
		{finished, "done", "a finished card is never resurrected into a live column"},
		{dispatcher, "in_progress", "origin='manual' is dispatcher-owned and untouchable"},
		{elsewhere, "in_progress", "the sweep is scoped to ONE session"},
	} {
		got := cardByID(t, db, tc.id)
		if got.column != tc.want {
			t.Errorf("card %d board_column = %q, want %q — %s", tc.id, got.column, tc.want, tc.why)
		}
		if got.movedAt.String != parkedAt {
			t.Errorf("card %d column_moved_at = %v, want it untouched — %s", tc.id, got.movedAt, tc.why)
		}
	}

	// A second sweep (a re-tail, a repeat kill) finds nothing left to move and
	// therefore says nothing.
	if again := SweepSessionToReview(db, sessionID); len(again) != 0 {
		t.Errorf("second sweep returned %v, want nothing", again)
	}
	// A session that never had a card is the common case and must be silent.
	if none := SweepSessionToReview(db, 999999); len(none) != 0 {
		t.Errorf("sweep of an unknown session returned %v, want nothing", none)
	}
}

// TestSweepSessionToReviewOnStatusTicker wires signal 2 to its first call site:
// the ingest status ticker aging a quiet session to 'completed'. Exactly one
// task_updated frame per moved card, and none on the ticker's next pass — the
// session is already terminal, so there is no transition and nothing to say.
func TestSweepSessionToReviewOnStatusTicker(t *testing.T) {
	db := testDB(t)
	ingestFixture(t, db, "todo-capture-session.jsonl")
	sessionID := sessionIDByUUID(t, db, captureSessionUUID)
	// The fixture's own timestamps already read as long finished; put the row
	// back to 'idle' so the ticker has a transition to make.
	mustExecCapture(t, db, `UPDATE sessions SET status = 'idle' WHERE id = ?`, sessionID)
	cardID := acceptCard(t, db, todoCaptureKey(captureSessionUUID, lifecycleTodo))

	bus := NewBus()
	notes, cancel := bus.Subscribe(32)
	defer cancel()

	p := NewPipeline(db, Config{ProjectsRoot: t.TempDir()}, bus)
	p.recomputeStatuses()

	if got := cardByID(t, db, cardID); got.column != "in_review" {
		t.Fatalf("accepted card board_column = %q after the session completed, want in_review", got.column)
	}
	frames := drainTaskFrames(t, notes)
	if len(frames) != 1 {
		t.Fatalf("task_updated frames = %d, want exactly 1 per moved card", len(frames))
	}
	if frames[0].TaskID != cardID || frames[0].SessionID != sessionID {
		t.Errorf("frame = %+v, want task_updated for card %d of session %d", frames[0], cardID, sessionID)
	}

	p.recomputeStatuses()
	if extra := drainTaskFrames(t, notes); len(extra) != 0 {
		t.Errorf("second ticker pass published %d task frames, want 0", len(extra))
	}
}

// ── the scope guard, asserted head-on ────────────────────────────────────────

// TestAutoMoveNeverTouchesDispatcherOwnedCards is the safety test phase 4 exists
// to pass. Both signals write to `tasks`, the same table dispatch/service.go
// drives through its own exit routing (finishReview, finishDone, finishBlocked),
// and the ONLY thing keeping the two state machines disjoint is
// `origin != 'manual'` in each UPDATE.
//
// Each subtest therefore builds a manual row that satisfies every OTHER
// condition its signal tests — the exact capture_key a completed todo resolves
// to, the exact origin_session_id the sweep selects on, board_column
// 'in_progress' — so the only reason it survives is the origin guard. Delete
// that conjunct from either statement and these are the tests that go red.
func TestAutoMoveNeverTouchesDispatcherOwnedCards(t *testing.T) {
	t.Run("signal 1: completed todo", func(t *testing.T) {
		db := testDB(t)
		seedCaptureSession(t, db, captureSessionUUID)
		projectID := projectIDOf(t, db, captureSessionUUID)
		sessionID := sessionIDByUUID(t, db, captureSessionUUID)
		key := todoCaptureKey(captureSessionUUID, lifecycleTodo)

		id := seedCard(t, db, cardSpec{projectID: projectID, sessionID: sessionID,
			key: key, column: "in_progress", origin: "manual", title: "dispatcher owned"})

		// The full hook-A path, not the helper in isolation: the capture insert
		// no-ops on the existing capture_key and the move is what runs next.
		if ids := captureBatch(t, db, projectID, sessionID,
			todoBlockStatus(t, todoStatusCompleted, lifecycleTodo)); len(ids) != 0 {
			t.Errorf("a completed todo announced %v for a dispatcher-owned card, want nothing", ids)
		}
		got := cardByID(t, db, id)
		if got.column != "in_progress" || got.movedAt.String != parkedAt {
			t.Errorf("dispatcher-owned card = %+v, want it untouched at in_progress/%q", got, parkedAt)
		}
	})

	t.Run("signal 2: session end", func(t *testing.T) {
		db := testDB(t)
		seedCaptureSession(t, db, captureSessionUUID)
		projectID := projectIDOf(t, db, captureSessionUUID)
		sessionID := sessionIDByUUID(t, db, captureSessionUUID)

		manual := seedCard(t, db, cardSpec{projectID: projectID, sessionID: sessionID,
			column: "in_progress", origin: "manual", title: "dispatcher owned"})
		captured := seedCard(t, db, cardSpec{projectID: projectID, sessionID: sessionID,
			key: "todo:guard:1", column: "in_progress", origin: "session", title: "captured and accepted"})

		moved := SweepSessionToReview(db, sessionID)
		if len(moved) != 1 || moved[0] != captured {
			t.Fatalf("sweep returned %v, want exactly [%d] — the captured card only", moved, captured)
		}
		got := cardByID(t, db, manual)
		if got.column != "in_progress" || got.movedAt.String != parkedAt {
			t.Errorf("dispatcher-owned card = %+v, want it untouched at in_progress/%q", got, parkedAt)
		}
	})
}

// TestAutoMoveNeverTouchesARedispatchedCapturedCard covers the hole
// `origin != 'manual'` alone leaves open, on the rework path the phase itself
// documents.
//
// A captured card the user sends back for rework (in_review → todo) is picked up
// by the dispatcher like any other queue row. admit() (dispatch/service.go) sets
// board_column='in_progress', status='running' and a worktree_path — and clears
// NONE of origin, capture_key or origin_session_id; api/tasks_board.go refuses to
// patch them precisely because capture_key is the permanent idempotency key.
// The row is therefore a LIVE DISPATCHER RUN that still reads as a captured card,
// and both auto-move signals still resolve to it: a re-tail re-observes the same
// completed todo (signal 1), and the session it was captured from eventually goes
// terminal like every session does (signal 2). Either one flipping it to
// in_review while status='running' and the worktree is still held is the
// dispatcher losing track of its own run.
//
// `worktree_path IS NULL` is what stops both, and each subtest carries its own
// POSITIVE CONTROL — an ordinary accepted captured card, identical but for the
// worktree — so a guard that simply switched the feature off would not pass.
func TestAutoMoveNeverTouchesARedispatchedCapturedCard(t *testing.T) {
	const controlTodo = "Write the retry helper's table test"

	t.Run("signal 1: completed todo", func(t *testing.T) {
		db := testDB(t)
		seedCaptureSession(t, db, captureSessionUUID)
		projectID := projectIDOf(t, db, captureSessionUUID)
		sessionID := sessionIDByUUID(t, db, captureSessionUUID)

		// Reworked, then re-admitted by the dispatcher: still origin='session',
		// still carrying the capture_key this very todo resolves to.
		redispatched := seedCard(t, db, cardSpec{projectID: projectID, sessionID: sessionID,
			key: todoCaptureKey(captureSessionUUID, lifecycleTodo), column: "in_progress",
			origin: "session", title: "reworked and re-dispatched",
			status: "running", worktreePath: "/tmp/swarmery-worktrees/swarm-42"})
		// The control: accepted by the user, never dispatched. It MUST still move.
		control := seedCard(t, db, cardSpec{projectID: projectID, sessionID: sessionID,
			key: todoCaptureKey(captureSessionUUID, controlTodo), column: "in_progress",
			origin: "session", title: "accepted, never dispatched"})

		// One TodoWrite reporting both todos completed, through the full hook-A
		// path: the inserts no-op on the existing capture_keys and the moves run.
		ids := captureBatch(t, db, projectID, sessionID,
			todoBlockStatus(t, todoStatusCompleted, lifecycleTodo, controlTodo))
		if len(ids) != 1 || ids[0] != control {
			t.Fatalf("completed todos announced %v, want exactly [%d] — the never-dispatched card only",
				ids, control)
		}

		got := cardByID(t, db, redispatched)
		if got.column != "in_progress" || got.movedAt.String != parkedAt {
			t.Errorf("re-dispatched card = %+v, want it untouched at in_progress/%q — "+
				"a live run holding a worktree is dispatcher-owned", got, parkedAt)
		}
		if ctl := cardByID(t, db, control); ctl.column != "in_review" || ctl.movedAt.String == parkedAt {
			t.Errorf("control card = %+v, want in_review with a fresh stamp — "+
				"the guard must exclude live runs, not disable auto-move", ctl)
		}
	})

	t.Run("signal 2: session end", func(t *testing.T) {
		db := testDB(t)
		seedCaptureSession(t, db, captureSessionUUID)
		projectID := projectIDOf(t, db, captureSessionUUID)
		sessionID := sessionIDByUUID(t, db, captureSessionUUID)

		redispatched := seedCard(t, db, cardSpec{projectID: projectID, sessionID: sessionID,
			key: "todo:redispatched:1", column: "in_progress", origin: "session",
			title:  "reworked and re-dispatched",
			status: "running", worktreePath: "/tmp/swarmery-worktrees/swarm-43"})
		control := seedCard(t, db, cardSpec{projectID: projectID, sessionID: sessionID,
			key: "todo:redispatched:2", column: "in_progress", origin: "session",
			title: "accepted, never dispatched"})

		moved := SweepSessionToReview(db, sessionID)
		if len(moved) != 1 || moved[0] != control {
			t.Fatalf("sweep returned %v, want exactly [%d] — the never-dispatched card only",
				moved, control)
		}
		got := cardByID(t, db, redispatched)
		if got.column != "in_progress" || got.movedAt.String != parkedAt {
			t.Errorf("re-dispatched card = %+v, want it untouched at in_progress/%q — "+
				"origin_session_id is immutable, so the sweep still resolves to it", got, parkedAt)
		}
		if ctl := cardByID(t, db, control); ctl.column != "in_review" {
			t.Errorf("control card board_column = %q, want in_review — "+
				"the guard must exclude live runs, not disable the sweep", ctl.column)
		}
	})
}

// ── helpers ──────────────────────────────────────────────────────────────────

// todoBlockStatus builds the assistant tool_use block a TodoWrite call arrives
// as, with an explicit status on every item. capture_test.go's todoBlock pins
// "pending" because phase 3 did not care; lifecycle sync is a function OF the
// status, so these tests have to vary it.
func todoBlockStatus(t *testing.T, status string, contents ...string) contentBlock {
	t.Helper()
	type todo struct {
		Content    string `json:"content"`
		Status     string `json:"status"`
		ActiveForm string `json:"activeForm"`
	}
	todos := make([]todo, 0, len(contents))
	for _, c := range contents {
		todos = append(todos, todo{Content: c, Status: status, ActiveForm: c})
	}
	raw, err := json.Marshal(map[string]any{"todos": todos})
	if err != nil {
		t.Fatal(err)
	}
	return contentBlock{Type: "tool_use", ID: "toolu_lifecycle", Name: "TodoWrite", Input: raw}
}

// captureBatch runs ONE tail batch's worth of hook A and returns the ids the
// batch would announce, i.e. exactly ingester.capturedTaskIDs — the list the
// pipeline turns into task_updated frames after the commit. Running it inside a
// real transaction is the point: it is the shape ingest uses, and the shape a
// *sql.DB-based move would deadlock in.
func captureBatch(t *testing.T, db *sql.DB, projectID, sessionID int64, b contentBlock) []int64 {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	in := &ingester{
		tx:          tx,
		projectID:   projectID,
		sessionID:   sessionID,
		sessionUUID: captureSessionUUID,
	}
	in.captureTodos(b, false)
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit capture batch: %v", err)
	}
	return in.capturedTaskIDs
}

// acceptCard is the user dragging a suggestion into In Progress: the column plus
// the stamp the PATCH handler writes on a column change. Returns the card id.
func acceptCard(t *testing.T, db *sql.DB, captureKey string) int64 {
	t.Helper()
	mustExecCapture(t, db,
		`UPDATE tasks SET board_column = 'in_progress', column_moved_at = ? WHERE capture_key = ?`,
		parkedAt, captureKey)
	return cardByKey(t, db, captureKey).id
}

// cardSpec describes a board row to plant directly, for the states a capture
// run cannot produce on its own (a dispatcher-owned row, a card already done).
// An empty key means capture_key IS NULL — what every hand-written card carries.
//
// status/worktreePath default to what InsertCapturedTask writes and the board
// PATCH never touches ('queued', NULL). Setting them is how a test says "the
// dispatcher re-admitted this card": admit() writes board_column='in_progress',
// status='running' and a worktree_path in ONE statement, and clears none of the
// capture columns.
type cardSpec struct {
	projectID    int64
	sessionID    int64
	key          string
	column       string
	origin       string
	title        string
	status       string
	worktreePath string
}

func seedCard(t *testing.T, db *sql.DB, s cardSpec) int64 {
	t.Helper()
	var key any
	if s.key != "" {
		key = s.key
	}
	var worktree any
	if s.worktreePath != "" {
		worktree = s.worktreePath
	}
	status := s.status
	if status == "" {
		status = "queued"
	}
	res, err := db.Exec(`
		INSERT INTO tasks (project_id, title, prompt, priority, status, created_at,
		                   source, external_id, board_column, file_scope, dependencies,
		                   column_moved_at, origin, origin_session_id, capture_key,
		                   worktree_path)
		VALUES (?, ?, 'seeded', 5, ?, ?, 'queue', ?, ?, '[]', '[]', ?, ?, ?, ?, ?)`,
		s.projectID, s.title, status, parkedAt, seedExternalID(),
		s.column, parkedAt, s.origin, s.sessionID, key, worktree)
	if err != nil {
		t.Fatalf("seed card %q: %v", s.title, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// seedExternalID mints a distinct "T-xxxxxx" per seeded row. external_id only
// has to be unique, and a counter is the one generator that cannot collide.
var seedExternalIDSeq atomic.Int64

func seedExternalID() string {
	return fmt.Sprintf("T-s%05d", seedExternalIDSeq.Add(1))
}

// boardCard is the slice of a board row these tests assert on.
type boardCard struct {
	id      int64
	column  string
	movedAt sql.NullString
}

func cardByKey(t *testing.T, db *sql.DB, captureKey string) boardCard {
	t.Helper()
	return scanCard(t, db, `SELECT id, board_column, column_moved_at FROM tasks WHERE capture_key = ?`, captureKey)
}

func cardByID(t *testing.T, db *sql.DB, id int64) boardCard {
	t.Helper()
	return scanCard(t, db, `SELECT id, board_column, column_moved_at FROM tasks WHERE id = ?`, id)
}

func scanCard(t *testing.T, db *sql.DB, q string, arg any) boardCard {
	t.Helper()
	var c boardCard
	if err := db.QueryRow(q, arg).Scan(&c.id, &c.column, &c.movedAt); err != nil {
		t.Fatalf("read card (%v): %v", arg, err)
	}
	return c
}
