package ingest

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	captureSessionUUID    = "aabbccdd-1111-4000-8000-000000000001"
	dispatchedSessionUUID = "aabbccdd-2222-4000-8000-000000000002"
	todolessSessionUUID   = "aabbccdd-3333-4000-8000-000000000003"
	captureProjectPath    = "/Users/user/work/capture-app"
)

// ingestFixture ingests one committed transcript fixture into db.
func ingestFixture(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	if _, err := File(db, filepath.Join(fixtures, name)); err != nil {
		t.Fatalf("ingest %s: %v", name, err)
	}
}

func sessionIDByUUID(t *testing.T, db *sql.DB, uuid string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`SELECT id FROM sessions WHERE session_uuid = ?`, uuid).Scan(&id); err != nil {
		t.Fatalf("session %s: %v", uuid, err)
	}
	return id
}

// capturedTitles lists the titles of the cards captured from a session, in
// insert order — the readable form of "what landed on the board".
func capturedTitles(t *testing.T, db *sql.DB, sessionID int64) []string {
	t.Helper()
	rows, err := db.Query(
		`SELECT title FROM tasks WHERE origin_session_id = ? ORDER BY id`, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// pinSystemProject makes path the System project dir for the duration of a test
// (the override is process-global, like worktreeRootOverride).
func pinSystemProject(t *testing.T, path string) {
	t.Helper()
	old := systemBaseOverride
	systemBaseOverride = path
	t.Cleanup(func() { systemBaseOverride = old })
}

// ── hook A: TodoWrite → Triage cards ─────────────────────────────────────────

// TestCaptureTodosCreatesTriageCards: every item of a TodoWrite call becomes one
// suggested card, carrying the provenance a human needs to judge it three days
// later — which session it came from and what that session was asked to do.
func TestCaptureTodosCreatesTriageCards(t *testing.T) {
	db := testDB(t)
	ingestFixture(t, db, "todo-capture-session.jsonl")
	sessionID := sessionIDByUUID(t, db, captureSessionUUID)

	want := []string{
		"Extract the retry helper into internal/retry",
		"Add a table test for exponential backoff",
		"Document the retry budget in README",
	}
	got := capturedTitles(t, db, sessionID)
	if len(got) != len(want) {
		t.Fatalf("captured %d cards (%q), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("card %d title = %q, want %q", i, got[i], want[i])
		}
	}

	rows, err := db.Query(`
		SELECT title, prompt, origin, board_column, source, status, priority, capture_key,
		       external_id, agent
		  FROM tasks WHERE origin_session_id = ? ORDER BY id`, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seenKeys := map[string]bool{}
	n := 0
	for rows.Next() {
		var (
			title, prompt, origin, column, source, status, key, extID string
			priority                                                  int
			agent                                                     sql.NullString
		)
		if err := rows.Scan(&title, &prompt, &origin, &column, &source, &status,
			&priority, &key, &extID, &agent); err != nil {
			t.Fatal(err)
		}
		n++
		if origin != "session" || column != "triage" || source != "queue" || status != "queued" {
			t.Errorf("card %q = origin %q column %q source %q status %q; want session/triage/queue/queued",
				title, origin, column, source, status)
		}
		if priority != 5 {
			t.Errorf("card %q priority = %d, want normal (5)", title, priority)
		}
		if agent.Valid {
			t.Errorf("card %q agent = %q, want NULL (a suggestion picks no agent)", title, agent.String)
		}
		if !strings.HasPrefix(extID, "T-") || len(extID) != 8 {
			t.Errorf("card %q external_id = %q, want T-xxxxxx", title, extID)
		}
		// capture_key: exactly 'todo:<uuid>:<12 lowercase hex>'.
		wantPrefix := "todo:" + captureSessionUUID + ":"
		if !strings.HasPrefix(key, wantPrefix) {
			t.Fatalf("capture_key %q, want prefix %q", key, wantPrefix)
		}
		hash := strings.TrimPrefix(key, wantPrefix)
		if len(hash) != 12 || strings.Trim(hash, "0123456789abcdef") != "" {
			t.Errorf("capture_key hash = %q, want 12 lowercase hex chars", hash)
		}
		if seenKeys[key] {
			t.Errorf("duplicate capture_key %q across cards of one session", key)
		}
		seenKeys[key] = true
		// Provenance footer: the session id, plus the prompt that started it.
		if !strings.Contains(prompt, "Captured from session "+captureSessionUUID) {
			t.Errorf("card %q prompt is missing the session provenance:\n%s", title, prompt)
		}
		if !strings.Contains(prompt, "Refactor the retry helper") {
			t.Errorf("card %q prompt is missing the opening-prompt excerpt:\n%s", title, prompt)
		}
		if !strings.HasPrefix(prompt, title) {
			t.Errorf("card prompt should open with the todo text, got:\n%s", prompt)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("scanned %d captured rows, want 3", n)
	}

	// Nothing else on the board: the Read call in the same transcript must not
	// have produced a card.
	if got := count(t, db, `SELECT COUNT(*) FROM tasks`); got != 3 {
		t.Errorf("tasks total = %d, want 3 (only the todos)", got)
	}
}

// TestCaptureTodosReplayAddsNothing: ingest replays constantly — a re-tail, a
// daemon restart, an offset reset, and the session's own repeated TodoWrite
// calls (statuses advance, whitespace and case drift) all describe the SAME
// three todos. Every one of those paths must converge on three cards.
func TestCaptureTodosReplayAddsNothing(t *testing.T) {
	db := testDB(t)
	ingestFixture(t, db, "todo-capture-session.jsonl")
	sessionID := sessionIDByUUID(t, db, captureSessionUUID)
	first := capturedTitles(t, db, sessionID)
	if len(first) != 3 {
		t.Fatalf("first ingest captured %d cards, want 3", len(first))
	}

	// (1) The session rewrites its todo list: same items, advanced statuses,
	// jittered whitespace and case. Normalization must collapse them.
	ingestFixture(t, db, "todo-capture-replay.jsonl")
	if got := capturedTitles(t, db, sessionID); len(got) != 3 {
		t.Errorf("after the session's todo rewrite: %d cards (%q), want the original 3", len(got), got)
	}

	// (2) A full re-ingest of the original transcript (offset reset / restart).
	ingestFixture(t, db, "todo-capture-session.jsonl")
	ingestFixture(t, db, "todo-capture-replay.jsonl")
	got := capturedTitles(t, db, sessionID)
	if len(got) != 3 {
		t.Fatalf("after re-ingest: %d cards (%q), want 3", len(got), got)
	}
	for i := range first {
		if got[i] != first[i] {
			t.Errorf("card %d changed on replay: %q → %q (a replay is a no-op, not an update)",
				i, first[i], got[i])
		}
	}
}

// TestCaptureTodosSkipsDispatchedSession: a dispatched run is a board card
// EXECUTING. Capturing its todos would mint children of the card that spawned
// it. Both link shapes count — the uuid the dispatcher parks before spawning,
// and the explicit task_sessions row it reconciles afterwards.
func TestCaptureTodosSkipsDispatchedSession(t *testing.T) {
	for _, tc := range []struct {
		name string
		link func(t *testing.T, db *sql.DB, projectID, sessionID int64)
	}{
		{
			name: "dispatch_session_uuid parked before the spawn",
			link: func(t *testing.T, db *sql.DB, projectID, _ int64) {
				t.Helper()
				mustExecCapture(t, db, `
					INSERT INTO tasks (project_id, title, prompt, created_at, dispatch_session_uuid)
					VALUES (?, 'the card being executed', 'p', '2026-07-10T09:00:00.000Z', ?)`,
					projectID, dispatchedSessionUUID)
			},
		},
		{
			name: "explicit task_sessions link",
			link: func(t *testing.T, db *sql.DB, projectID, sessionID int64) {
				t.Helper()
				mustExecCapture(t, db, `
					INSERT INTO tasks (project_id, title, prompt, created_at)
					VALUES (?, 'the card being executed', 'p', '2026-07-10T09:00:00.000Z')`, projectID)
				var taskID int64
				if err := db.QueryRow(`SELECT id FROM tasks ORDER BY id DESC LIMIT 1`).Scan(&taskID); err != nil {
					t.Fatal(err)
				}
				mustExecCapture(t, db, `
					INSERT INTO task_sessions (task_id, session_id, link_source)
					VALUES (?, ?, 'explicit')`, taskID, sessionID)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := testDB(t)
			// Seed the project + session rows WITHOUT capture running, so the
			// dispatch link exists before the todo lines are read — the live
			// ordering (the dispatcher links, then the transcript appears).
			seedCaptureSession(t, db, dispatchedSessionUUID)
			projectID := projectIDOf(t, db, dispatchedSessionUUID)
			sessionID := sessionIDByUUID(t, db, dispatchedSessionUUID)
			tc.link(t, db, projectID, sessionID)

			ingestFixture(t, db, "dispatched-todo-session.jsonl")

			if got := capturedTitles(t, db, sessionID); len(got) != 0 {
				t.Errorf("dispatched session captured %d cards (%q), want 0", len(got), got)
			}
			if got := count(t, db, `SELECT COUNT(*) FROM tasks WHERE capture_key IS NOT NULL`); got != 0 {
				t.Errorf("captured rows = %d, want 0", got)
			}
		})
	}
}

// TestCaptureTodosSkipsSystemProject: the System project (~/.swarmery) holds the
// daemon's own headless runs — phase executors, telemetry sweeps. Their todos
// are the machine's bookkeeping and would bury the operator's board under its
// own exhaust.
func TestCaptureTodosSkipsSystemProject(t *testing.T) {
	db := testDB(t)
	pinSystemProject(t, captureProjectPath)

	ingestFixture(t, db, "todo-capture-session.jsonl")
	sessionID := sessionIDByUUID(t, db, captureSessionUUID)
	if got := capturedTitles(t, db, sessionID); len(got) != 0 {
		t.Errorf("System-project session captured %d cards (%q), want 0", len(got), got)
	}
}

// TestCaptureTodosSkipsSidechain: a subagent's todo list is its private plan,
// not the operator's work — the orchestrator transcript already records what it
// was asked to do.
func TestCaptureTodosSkipsSidechain(t *testing.T) {
	db := testDB(t)
	seedCaptureSession(t, db, captureSessionUUID)

	block := todoBlock(t, "Extract the retry helper into internal/retry")
	// Resolve the ids BEFORE opening the transaction: the store runs on a
	// single connection (store.Open → SetMaxOpenConns(1)), so any query against
	// db while a tx is open would wait for a connection the tx is holding.
	projectID := projectIDOf(t, db, captureSessionUUID)
	sessionID := sessionIDByUUID(t, db, captureSessionUUID)

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	in := &ingester{
		tx:          tx,
		projectID:   projectID,
		sessionID:   sessionID,
		sessionUUID: captureSessionUUID,
	}

	in.captureTodos(block, true) // sidechain
	if len(in.capturedTaskIDs) != 0 {
		t.Errorf("sidechain captured %d cards, want 0", len(in.capturedTaskIDs))
	}
	// Control: the identical block on the main transcript DOES capture, so the
	// assertion above failed on the sidechain flag and not on a broken block.
	in.captureTodos(block, false)
	if len(in.capturedTaskIDs) != 1 {
		t.Errorf("main-transcript capture = %d cards, want 1", len(in.capturedTaskIDs))
	}
}

// TestCaptureTodosShortCircuitsNonTodoWrite is the hot-path guarantee: a
// transcript makes thousands of tool calls and a handful of TodoWrite calls, so
// everything capture does — the input decode, the skip query, the inserts — has
// to sit behind the tool-NAME comparison.
//
// The proof is structural rather than a benchmark: the ingester is built with a
// NIL transaction, so any database access below the gate panics, and the
// memoized skip verdict is asserted to still be unset, so the skip query never
// ran either. Deliberately malformed tool input backs it up — a decode attempt
// on these blocks would be visible as a decode error path, and there is none.
func TestCaptureTodosShortCircuitsNonTodoWrite(t *testing.T) {
	in := &ingester{
		tx:          nil, // any query below the name gate → nil-pointer panic
		projectID:   1,
		sessionID:   1,
		sessionUUID: captureSessionUUID,
	}
	for _, name := range []string{"Read", "Edit", "Write", "Bash", "Grep", "Glob", "Agent", "Skill", "todowrite", ""} {
		in.captureTodos(contentBlock{
			Type:  "tool_use",
			ID:    "toolu_x",
			Name:  name,
			Input: json.RawMessage(`{"todos":[{"content":"not a real todo"}], "trailing`), // invalid JSON
		}, false)
	}
	if in.skipCapture != nil {
		t.Errorf("skip verdict computed for non-TodoWrite blocks (= %v); the name gate must return first",
			*in.skipCapture)
	}
	if len(in.capturedTaskIDs) != 0 {
		t.Errorf("non-TodoWrite blocks captured %d cards, want 0", len(in.capturedTaskIDs))
	}
}

// TestNormalizeTodoContent pins the normalization the capture_key depends on:
// the same todo re-emitted with drifted whitespace or case is the SAME todo.
func TestNormalizeTodoContent(t *testing.T) {
	same := []string{
		"Extract the retry helper",
		"  Extract the retry helper  ",
		"extract THE retry   helper",
		"Extract\tthe\nretry helper",
	}
	want := todoCaptureKey(captureSessionUUID, same[0])
	for _, s := range same[1:] {
		if got := todoCaptureKey(captureSessionUUID, s); got != want {
			t.Errorf("todoCaptureKey(%q) = %q, want %q (normalization must collapse this)", s, got, want)
		}
	}
	if got := todoCaptureKey(captureSessionUUID, "Extract the retry helpers"); got == want {
		t.Error("different todo text produced the same capture_key")
	}
	if got := sessionCaptureKey(captureSessionUUID); got != "sess:"+captureSessionUUID {
		t.Errorf("sessionCaptureKey = %q", got)
	}
}

// TestClipCountsRunes: captured titles are rendered verbatim on the board, and a
// byte-wise cut of a multi-byte rune shows up as a replacement character.
func TestClipCountsRunes(t *testing.T) {
	s := strings.Repeat("ї", 200)
	got := clip(s, captureTitleLimit)
	if r := []rune(got); len(r) != captureTitleLimit+1 || r[len(r)-1] != '…' {
		t.Errorf("clip produced %d runes, want %d + ellipsis", len(r), captureTitleLimit)
	}
	if !strings.HasPrefix(got, strings.Repeat("ї", captureTitleLimit)) {
		t.Errorf("clip corrupted the retained runes: %q", got)
	}
	if got := clip("short", captureTitleLimit); got != "short" {
		t.Errorf("clip(short) = %q, want it untouched", got)
	}
	if got := clip("anything", 0); got != "" {
		t.Errorf("clip(_, 0) = %q, want empty", got)
	}
}

// ── hook A: notification contract ────────────────────────────────────────────

// TestCaptureTodosPublishesOncePerInsert: the tail surfaces exactly one task id
// per card that REALLY landed, and none on a replay. The pipeline turns that
// list into task_updated frames, so a re-tail of an old transcript must not
// look like live board activity to every connected dashboard.
func TestCaptureTodosPublishesOncePerInsert(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "todo-capture-session.jsonl")
	copyFixtureFile(t, "todo-capture-session.jsonl", path)

	res, err := TailFile(db, path, "", Thresholds{})
	if err != nil {
		t.Fatalf("first tail: %v", err)
	}
	if len(res.CapturedTaskIDs) != 3 {
		t.Fatalf("first tail CapturedTaskIDs = %v, want 3 ids", res.CapturedTaskIDs)
	}
	seen := map[int64]bool{}
	for _, id := range res.CapturedTaskIDs {
		if id == 0 || seen[id] {
			t.Errorf("CapturedTaskIDs = %v, want 3 distinct non-zero ids", res.CapturedTaskIDs)
		}
		seen[id] = true
	}

	// Same bytes again from offset 0 (the file-recreated / offset-reset path).
	if _, err := db.Exec(`DELETE FROM file_offsets`); err != nil {
		t.Fatal(err)
	}
	res2, err := TailFile(db, path, "", Thresholds{})
	if err != nil {
		t.Fatalf("replay tail: %v", err)
	}
	if len(res2.CapturedTaskIDs) != 0 {
		t.Errorf("replay tail CapturedTaskIDs = %v, want none — a replay inserted nothing",
			res2.CapturedTaskIDs)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM tasks`); got != 3 {
		t.Errorf("tasks after replay = %d, want 3", got)
	}
}

// ── hook B: the session fallback card ────────────────────────────────────────

// TestCaptureSessionCardFallback: a session that ends having captured no todos
// still leaves a trace on the board — one card built from what it was asked to
// do. Short "just do X" sessions are the common case and would otherwise vanish.
func TestCaptureSessionCardFallback(t *testing.T) {
	db := testDB(t)
	ingestFixture(t, db, "todoless-session.jsonl")
	sessionID := sessionIDByUUID(t, db, todolessSessionUUID)

	id, inserted := CaptureSessionCard(db, sessionID)
	if !inserted || id == 0 {
		t.Fatalf("CaptureSessionCard = (%d, %v), want a real insert", id, inserted)
	}

	var title, prompt, key, origin, column string
	var originSession sql.NullInt64
	if err := db.QueryRow(
		`SELECT title, prompt, capture_key, origin, board_column, origin_session_id
		   FROM tasks WHERE id = ?`, id,
	).Scan(&title, &prompt, &key, &origin, &column, &originSession); err != nil {
		t.Fatal(err)
	}
	// Title is the FIRST LINE of the opening prompt: a board column shows one line.
	if title != "Ship the nightly digest job" {
		t.Errorf("title = %q, want the first line of the opening prompt", title)
	}
	if key != "sess:"+todolessSessionUUID {
		t.Errorf("capture_key = %q, want sess:%s", key, todolessSessionUUID)
	}
	if origin != "session" || column != "triage" {
		t.Errorf("card = origin %q column %q, want session/triage", origin, column)
	}
	if !originSession.Valid || originSession.Int64 != sessionID {
		t.Errorf("origin_session_id = %v, want %d", originSession, sessionID)
	}
	// The card body is the WHOLE opening prompt, not just its title line.
	if !strings.Contains(prompt, "email the summary to the on-call rota") {
		t.Errorf("prompt lost the body of the opening message:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Captured from session "+todolessSessionUUID) {
		t.Errorf("prompt is missing the provenance footer:\n%s", prompt)
	}
	// …but not twice: a session card IS the opening prompt, so the footer must
	// not append a second copy of it as an "excerpt".
	if n := strings.Count(prompt, "Ship the nightly digest job"); n != 1 {
		t.Errorf("opening prompt appears %d times in the card body, want 1:\n%s", n, prompt)
	}

	// Idempotent: the transition can fire again (a re-tail reactivates the
	// session, the ticker completes it a second time) and must be a no-op.
	id2, inserted2 := CaptureSessionCard(db, sessionID)
	if inserted2 {
		t.Error("second CaptureSessionCard reported an insert; 'sess:<uuid>' must dedupe")
	}
	if id2 != id {
		t.Errorf("second call id = %d, want the original %d", id2, id)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM tasks`); got != 1 {
		t.Errorf("tasks = %d, want exactly 1 session card", got)
	}
}

// TestCaptureSessionCardSkippedWhenTodosCaptured: the fallback exists for
// sessions that left NOTHING. One that filed three todos is already on the board
// in detail; a whole-session card would duplicate it at a coarser grain.
func TestCaptureSessionCardSkippedWhenTodosCaptured(t *testing.T) {
	db := testDB(t)
	ingestFixture(t, db, "todo-capture-session.jsonl")
	sessionID := sessionIDByUUID(t, db, captureSessionUUID)
	if got := capturedTitles(t, db, sessionID); len(got) != 3 {
		t.Fatalf("setup captured %d cards, want 3", len(got))
	}

	if id, inserted := CaptureSessionCard(db, sessionID); inserted {
		t.Errorf("fallback card %d minted for a session that already captured todos", id)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM tasks WHERE capture_key LIKE 'sess:%'`); got != 0 {
		t.Errorf("sess: cards = %d, want 0", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM tasks`); got != 3 {
		t.Errorf("tasks = %d, want the 3 todo cards only", got)
	}
}

// TestCaptureSessionCardSkipRules: the fallback obeys the same skip rules as the
// todo hook, plus "the session did nothing" — a stub row with no assistant turn
// has no work to remember.
func TestCaptureSessionCardSkipRules(t *testing.T) {
	t.Run("system project", func(t *testing.T) {
		db := testDB(t)
		pinSystemProject(t, captureProjectPath)
		ingestFixture(t, db, "todoless-session.jsonl")
		if _, inserted := CaptureSessionCard(db, sessionIDByUUID(t, db, todolessSessionUUID)); inserted {
			t.Error("System-project session minted a fallback card")
		}
	})

	t.Run("dispatched run", func(t *testing.T) {
		db := testDB(t)
		ingestFixture(t, db, "todoless-session.jsonl")
		sessionID := sessionIDByUUID(t, db, todolessSessionUUID)
		mustExecCapture(t, db, `
			INSERT INTO tasks (project_id, title, prompt, created_at, dispatch_session_uuid)
			VALUES (?, 'the card being executed', 'p', '2026-07-10T11:00:00.000Z', ?)`,
			projectIDOf(t, db, todolessSessionUUID), todolessSessionUUID)
		if _, inserted := CaptureSessionCard(db, sessionID); inserted {
			t.Error("dispatched session minted a fallback card")
		}
	})

	t.Run("no assistant turn", func(t *testing.T) {
		db := testDB(t)
		seedCaptureSession(t, db, todolessSessionUUID) // session row, no transcript
		if _, inserted := CaptureSessionCard(db, sessionIDByUUID(t, db, todolessSessionUUID)); inserted {
			t.Error("a session with no assistant turn minted a card")
		}
	})

	t.Run("unknown session", func(t *testing.T) {
		db := testDB(t)
		if _, inserted := CaptureSessionCard(db, 9999); inserted {
			t.Error("a missing session minted a card")
		}
	})
}

// TestCaptureSessionCardOnStatusTicker wires hook B end to end: the ticker ages
// a quiet session to 'completed', the fallback card lands, and exactly ONE
// task_updated frame reaches the bus — with none on the ticker's next pass.
func TestCaptureSessionCardOnStatusTicker(t *testing.T) {
	db := testDB(t)
	ingestFixture(t, db, "todoless-session.jsonl")
	sessionID := sessionIDByUUID(t, db, todolessSessionUUID)
	// The fixture's own timestamps already read as long finished; put the row
	// back to 'idle' so the ticker has a transition to make.
	mustExecCapture(t, db, `UPDATE sessions SET status = 'idle' WHERE id = ?`, sessionID)

	bus := NewBus()
	notes, cancel := bus.Subscribe(32)
	defer cancel()

	p := NewPipeline(db, Config{ProjectsRoots: []string{t.TempDir()}}, bus)
	p.recomputeStatuses()

	var status string
	if err := db.QueryRow(`SELECT status FROM sessions WHERE id = ?`, sessionID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "completed" {
		t.Fatalf("session status = %q, want completed (the ticker had nothing to do)", status)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM tasks WHERE capture_key = ?`,
		"sess:"+todolessSessionUUID); got != 1 {
		t.Fatalf("session cards = %d, want exactly 1", got)
	}

	taskFrames := drainTaskFrames(t, notes)
	if len(taskFrames) != 1 {
		t.Fatalf("task_updated frames = %d, want exactly 1 per real insert", len(taskFrames))
	}
	if taskFrames[0].SessionID != sessionID || taskFrames[0].TaskID == 0 {
		t.Errorf("frame = %+v, want the captured card of session %d", taskFrames[0], sessionID)
	}

	// Second pass: the session is already 'completed', so there is no
	// transition and therefore nothing on the wire.
	p.recomputeStatuses()
	if extra := drainTaskFrames(t, notes); len(extra) != 0 {
		t.Errorf("second ticker pass published %d task frames, want 0", len(extra))
	}
	if got := count(t, db, `SELECT COUNT(*) FROM tasks`); got != 1 {
		t.Errorf("tasks = %d, want 1", got)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func mustExecCapture(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

// seedCaptureSession mints the project + session rows a transcript WOULD create,
// without running capture — so a test can establish state (a dispatch link, a
// stub session) that must already exist when the transcript is read.
func seedCaptureSession(t *testing.T, db *sql.DB, uuid string) {
	t.Helper()
	projectID, _, err := UpsertProject(db, captureProjectPath, "2026-07-10T09:00:00.000Z", "")
	if err != nil {
		t.Fatal(err)
	}
	mustExecCapture(t, db, `
		INSERT INTO sessions (project_id, session_uuid, status, started_at, cwd, source)
		VALUES (?, ?, 'active', '2026-07-10T09:00:00.000Z', ?, 'jsonl')`,
		projectID, uuid, captureProjectPath)
}

func projectIDOf(t *testing.T, db *sql.DB, sessionUUID string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(
		`SELECT project_id FROM sessions WHERE session_uuid = ?`, sessionUUID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// todoBlock builds the assistant tool_use block a TodoWrite call arrives as.
func todoBlock(t *testing.T, contents ...string) contentBlock {
	t.Helper()
	type todo struct {
		Content    string `json:"content"`
		Status     string `json:"status"`
		ActiveForm string `json:"activeForm"`
	}
	todos := make([]todo, 0, len(contents))
	for _, c := range contents {
		todos = append(todos, todo{Content: c, Status: "pending", ActiveForm: c})
	}
	raw, err := json.Marshal(map[string]any{"todos": todos})
	if err != nil {
		t.Fatal(err)
	}
	return contentBlock{Type: "tool_use", ID: "toolu_test", Name: "TodoWrite", Input: raw}
}

func copyFixtureFile(t *testing.T, name, dst string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtures, name))
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, dst, string(b))
}

// drainTaskFrames collects the task_updated notifications currently queued,
// ignoring the session/event traffic the same bus carries.
func drainTaskFrames(t *testing.T, notes <-chan Notification) []Notification {
	t.Helper()
	var out []Notification
	for {
		select {
		case n, ok := <-notes:
			if !ok {
				return out
			}
			if n.Type == NoteTaskUpdated {
				out = append(out, n)
			}
		case <-time.After(200 * time.Millisecond):
			return out
		}
	}
}

// TestCaptureTodosSkipsEngineRunSessions is the self-capture defect from the
// execution-engine plan's evidence section. The capture exemption checked the
// DISPATCH arms only, so a phase-run or plan-run session matched neither branch and
// minted captured board cards from its own work: the executor's own todo list
// arriving on the board as new cards while the phase it belongs to was still
// running.
//
// The run_session_uuid columns are checked directly, not via the explicit
// task_sessions link phase 3 also writes, because the columns are authoritative the
// moment the run starts while a link can be missing for as long as ingest lags — and
// a predicate that guards the board must not depend on a race. The arms below
// therefore create NO task_sessions row.
func TestCaptureTodosSkipsEngineRunSessions(t *testing.T) {
	for _, tc := range []struct {
		name string
		link func(t *testing.T, db *sql.DB, projectID int64)
	}{
		{
			name: "phase run (epic_phases.run_session_uuid)",
			link: func(t *testing.T, db *sql.DB, projectID int64) {
				t.Helper()
				taskID := insertEngineWorkspaceTask(t, db, projectID)
				mustExecCapture(t, db, `
					INSERT INTO epic_phases
						(workspace_task_id, seq, name, doc_path, depends_on, run_state, run_session_uuid)
					VALUES (?, 1, 'Phase 1', '/ws/plan/phase-1.md', '[]', 'running', ?)`,
					taskID, dispatchedSessionUUID)
			},
		},
		{
			name: "plan run (plan_runs.run_session_uuid)",
			link: func(t *testing.T, db *sql.DB, projectID int64) {
				t.Helper()
				taskID := insertEngineWorkspaceTask(t, db, projectID)
				mustExecCapture(t, db, `
					INSERT INTO plan_runs (workspace_task_id, run_state, run_session_uuid)
					VALUES (?, 'running', ?)`, taskID, dispatchedSessionUUID)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := testDB(t)
			// Seed project + session first, so the run's uuid is on its row before the
			// transcript is read — the live ordering (the engine stamps, then the
			// transcript appears).
			seedCaptureSession(t, db, dispatchedSessionUUID)
			tc.link(t, db, projectIDOf(t, db, dispatchedSessionUUID))
			sessionID := sessionIDByUUID(t, db, dispatchedSessionUUID)

			ingestFixture(t, db, "dispatched-todo-session.jsonl")

			if got := capturedTitles(t, db, sessionID); len(got) != 0 {
				t.Errorf("engine-run session captured %d cards (%q), want 0", len(got), got)
			}
			if got := count(t, db, `SELECT COUNT(*) FROM tasks WHERE capture_key IS NOT NULL`); got != 0 {
				t.Errorf("captured rows = %d, want 0", got)
			}
		})
	}

	// The control: the SAME transcript with no engine run claiming it still captures.
	// Without this the two arms above would pass equally well if capture were broken
	// outright.
	t.Run("a plain interactive session still captures", func(t *testing.T) {
		db := testDB(t)
		seedCaptureSession(t, db, dispatchedSessionUUID)
		sessionID := sessionIDByUUID(t, db, dispatchedSessionUUID)

		ingestFixture(t, db, "dispatched-todo-session.jsonl")

		if got := capturedTitles(t, db, sessionID); len(got) == 0 {
			t.Error("an unclaimed session captured nothing — the exemption is over-matching")
		}
	})
}

// insertEngineWorkspaceTask writes the workspace task a phase/plan run hangs off
// (both run tables carry a real FK to tasks.id).
func insertEngineWorkspaceTask(t *testing.T, db *sql.DB, projectID int64) int64 {
	t.Helper()
	mustExecCapture(t, db, `
		INSERT INTO tasks (project_id, title, prompt, created_at, source, external_id)
		VALUES (?, 'the plan being executed', 'goal', '2026-07-10T09:00:00.000Z',
		        'workspace', '2026-07-10-the-plan')`, projectID)
	var id int64
	if err := db.QueryRow(`SELECT id FROM tasks ORDER BY id DESC LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
