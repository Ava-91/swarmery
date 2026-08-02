package ingest

// Board capture: turning what a live session DID into suggested Triage cards.
//
// Two hooks, both deliberately kept out of the ingest loop body so the loop
// keeps reading as "parse a transcript" and not "parse a transcript and also
// run the board":
//
//	Hook A — captureTodos, per assistant tool_use block. Every item of a
//	         TodoWrite call becomes one card.
//	Hook B — CaptureSessionCard, on a session's transition to a terminal
//	         status. A session that ended having produced NO todo cards gets a
//	         single card built from its first user prompt, so short "just do X"
//	         sessions are not silently lost.
//
// Three properties this file exists to guarantee:
//
//  1. It is invisible on the hot path. captureTodos rejects on the tool NAME
//     before it decodes anything; the ~30 other tools in a transcript pay one
//     string comparison per block and nothing else. The skip verdict for a
//     session is memoized, so a 500-line batch costs at most one skip query.
//
//  2. It never breaks ingest. Every failure here is logged at debug level and
//     swallowed. A board card is a convenience; a transcript is the record of
//     what happened. Losing the former must never cost the latter, and a
//     capture error must never abort — or stall — the tail batch.
//
//  3. It is permanently idempotent. capture_key ('todo:<uuid>:<sha1-12>' and
//     'sess:<uuid>') plus 0048's partial unique index mean a re-tail, a daemon
//     restart, an offset reset, or a full re-ingest of the same transcript all
//     converge on the same set of cards. Ingest replays constantly — anything
//     less would grow a fresh copy of every card on each sweep.

import (
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/taskcap"
)

const (
	// captureTitleLimit matches the board's own title budget (titleLimit).
	captureTitleLimit = 120
	// captureKeyHashLen is how much of the content SHA-1 goes into a todo's
	// capture_key. 12 hex chars = 48 bits, scoped to ONE session's todo list —
	// collision would need two todos in the same session whose normalized text
	// hashes alike, which is not a realistic failure for a handful of items.
	captureKeyHashLen = 12
	// todoPromptExcerpt / sessionPromptLimit bound the provenance text copied
	// onto a card. A todo card carries a short excerpt of the session's opening
	// prompt for orientation; a session card IS the opening prompt, so it gets
	// the larger budget. Both are further capped by what ingest stored on the
	// user_prompt event (payloadStrLimit).
	todoPromptExcerpt  = 2000
	sessionPromptLimit = 4000
	// todoStatusCompleted is the TodoWrite status that ends a todo's life and
	// therefore ends the life of the card captured from it (lifecycle signal 1).
	// Matched verbatim, not case-folded: it is a literal the transcript writer
	// emits, and a lenient match would only widen what can trip an auto-move.
	todoStatusCompleted = "completed"
	// captureTSFormat is the millisecond-Z stamp column_moved_at carries.
	//
	// It is a third copy of api.boardTSFormat / taskcap's tsFormat rather than a
	// shared constant because internal/api imports internal/ingest, so the two
	// existing copies sit on opposite sides of an import edge that cannot be
	// closed here (internal/dispatch, internal/verify and internal/notify each
	// keep their own for the same reason). The format has to agree: the board
	// compares and sorts column_moved_at LEXICALLY, so an auto-move stamped in a
	// different shape than the one a user drag writes would order wrong in the
	// column it lands in.
	captureTSFormat = "2006-01-02T15:04:05.000Z"
)

// todoWriteInput is the shape of a TodoWrite tool call's input. Decoded ONLY
// after the name gate — this struct never sees a Read or a Bash payload.
type todoWriteInput struct {
	Todos []struct {
		Content    string `json:"content"`
		Status     string `json:"status"`
		ActiveForm string `json:"activeForm"`
	} `json:"todos"`
}

// normalizeTodoContent is the capture_key normalization: trim, collapse every
// whitespace run to a single space, lowercase. It exists because the SAME todo
// is rewritten on every TodoWrite call of a session — the status flips
// pending→in_progress→completed and the wording drifts in whitespace and case —
// and all of those must hash to one key, or a five-step plan would deposit
// fifteen cards on the board.
func normalizeTodoContent(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// todoCaptureKey is 'todo:<session-uuid>:<first 12 hex of sha1(normalized)>'.
// SHA-1 is a dedup key, never a security claim.
func todoCaptureKey(sessionUUID, content string) string {
	sum := sha1.Sum([]byte(normalizeTodoContent(content)))
	return "todo:" + sessionUUID + ":" + hex.EncodeToString(sum[:])[:captureKeyHashLen]
}

// sessionCaptureKey is 'sess:<session-uuid>' — one fallback card per session,
// enforced by the index rather than by a read-then-write check.
func sessionCaptureKey(sessionUUID string) string { return "sess:" + sessionUUID }

// captureTodos is hook A: one Triage card per item of a TodoWrite call.
//
// Called from the assistant-block loop for EVERY tool_use block in the
// transcript, which is why the name gate is the first statement: a session
// makes thousands of tool calls and a handful of TodoWrite calls, so everything
// below the gate — the JSON decode of the tool input, the skip query, the
// inserts — must stay behind it.
//
// Inserts go through in.tx, the same transaction already writing this batch's
// turns and events: a card can never survive a batch that rolls back. The bus
// frames are deferred to capturedTaskIDs for the same reason.
func (in *ingester) captureTodos(b contentBlock, sidechain bool) {
	if b.Name != "TodoWrite" {
		return // hot path: no decode, no query, no allocation for other tools
	}
	// Sidechains are a subagent's private plan, not the operator's work; the
	// orchestrator transcript already carries whatever it was asked to do.
	if sidechain || in.sessionID == 0 || in.projectID == 0 || in.sessionUUID == "" {
		return
	}
	if in.captureSkipped() {
		return
	}
	var input todoWriteInput
	if len(b.Input) == 0 || json.Unmarshal(b.Input, &input) != nil {
		return
	}
	if len(input.Todos) == 0 {
		return
	}
	footer := captureFooter(in.tx, in.sessionID, in.sessionUUID, todoPromptExcerpt)
	sessionID := in.sessionID
	for _, td := range input.Todos {
		content := strings.TrimSpace(td.Content)
		if content == "" {
			continue
		}
		key := todoCaptureKey(in.sessionUUID, content)
		id, inserted, err := taskcap.InsertCapturedTask(in.tx, taskcap.Input{
			ProjectID:       in.projectID,
			Title:           clip(content, captureTitleLimit),
			Prompt:          content + footer,
			Origin:          "session",
			OriginSessionID: &sessionID,
			CaptureKey:      key,
		})
		if err != nil {
			// Errors here are schema- or storage-level, i.e. identical for every
			// remaining todo — log once and give the batch back to ingest rather
			// than repeating the same failure N times.
			log.Printf("debug: ingest: capture todo (session %d): %v", sessionID, err)
			return
		}
		if inserted {
			in.capturedTaskIDs = append(in.capturedTaskIDs, id)
			// A card minted a microsecond ago is in 'triage' by construction, so
			// the lifecycle move below could only ever be a no-op on it — even
			// when this very TodoWrite already reports the todo as completed
			// (a session whose whole plan is done before the first tail sees it).
			// Skipping it keeps the "one id appended per card per batch" property
			// arithmetic rather than a claim about SQL.
			continue
		}
		// Lifecycle signal 1 (phase 4): the source todo finished, so the card the
		// user ACCEPTED from it is finished too — in_progress → in_review.
		//
		// This runs on in.tx, the tail transaction, and must: store.Open caps the
		// pool at one connection, so reaching for the *sql.DB handle here would
		// block on a connection this very transaction is holding. The move
		// therefore commits or rolls back with the batch that observed it.
		if td.Status == todoStatusCompleted {
			if movedID, moved := moveCapturedToReview(in.tx, key); moved {
				in.capturedTaskIDs = append(in.capturedTaskIDs, movedID)
			}
		}
	}
}

// moveCapturedToReview moves ONE captured card in_progress → in_review, keyed by
// its capture_key, and reports whether the row REALLY moved.
//
// It takes dbtx rather than *sql.DB, and that is load-bearing: hook A calls it
// from inside the tail transaction (see captureTodos) while hook B's sweep runs
// post-commit on a transaction of its own. store.Open caps the pool at a single
// connection, so a *sql.DB parameter would force hook A to issue a query on a
// handle whose only connection its own open transaction holds — a deadlock, not
// an error. Both *sql.Tx and *sql.DB satisfy dbtx.
//
// The WHERE clause is the entire safety story of auto-move, and every conjunct
// earns its place:
//
//   - capture_key = ?     — only a CAPTURED card; the key is NULL on every
//     hand-written row and NULL never matches '='.
//   - board_column =
//     'in_progress'       — only work the user explicitly ACCEPTED. A card still
//     in 'triage'/'todo' is an unreviewed suggestion and does
//     not expire; one already in 'done'/'archived' is finished
//     and must never be resurrected into a live column.
//   - origin != 'manual'  — hand-written cards are untouchable. The dispatcher
//     runs its own state machine over the rows this excludes
//     (dispatch/service.go finishReview and friends). Belt and
//     braces with the key predicate above, deliberately: it is
//     asserted directly by
//     TestMoveCapturedToReviewNeverTouchesManualOrigin.
//   - worktree_path
//     IS NULL             — and origin alone is NOT enough, because origin is
//     immutable. A captured card the user reworks
//     (in_review → todo) is re-admitted by the dispatcher
//     (dispatch/service.go admit), which sets
//     board_column='in_progress', status='running' and a
//     worktree_path but CANNOT clear origin/capture_key/
//     origin_session_id — api/tasks_board.go rejects patching
//     them, by design: capture_key is the permanent idempotency
//     key behind 0048's partial unique index and clearing it
//     would let capture re-mint a duplicate card. So a live
//     dispatcher run keeps looking like a captured card, and a
//     re-tail of its source todo would flip it to in_review
//     while status='running' and the worktree is still held.
//     worktree_path is the dispatcher's OWN ownership marker:
//     liveWorktreeCount (dispatch/service.go) defines a live
//     slot as exactly `worktree_path IS NOT NULL AND
//     board_column='in_progress'`, and admit writes the column
//     in the same UPDATE that writes board_column, so there is
//     no window in between. With board_column already pinned
//     above, this conjunct makes the capture set the exact
//     complement of that live set — the disjointness the
//     origin bullet claims, now stated in the dispatcher's own
//     terms. A card the user accepted but never dispatched has
//     worktree_path NULL and still moves, which is the whole
//     feature.
//
// Being a single guarded UPDATE also makes it race-safe against a user dragging
// the same card concurrently (last write wins; the WS frame reconciles the UI)
// and idempotent on replay — the second pass sees board_column='in_review',
// matches nothing, reports changes()==0, and stays silent.
func moveCapturedToReview(q dbtx, captureKey string) (int64, bool) {
	res, err := q.Exec(`
		UPDATE tasks
		   SET board_column = 'in_review', column_moved_at = ?
		 WHERE capture_key = ?
		   AND board_column = 'in_progress'
		   AND origin != 'manual'
		   AND worktree_path IS NULL`,
		time.Now().UTC().Format(captureTSFormat), captureKey)
	if err != nil {
		log.Printf("debug: ingest: capture move to review (%s): %v", captureKey, err)
		return 0, false
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		// Replay, a card not yet accepted, a manual row, or a card the dispatcher
		// is currently running in a worktree.
		return 0, false
	}
	// capture_key is unique (0048's partial index), so this reads back the row
	// just moved. A failure here loses only the frame, never the move — the
	// dashboard's 60s reconcile picks the column up on its next pass.
	var id int64
	if err := q.QueryRow(`SELECT id FROM tasks WHERE capture_key = ?`, captureKey).Scan(&id); err != nil {
		log.Printf("debug: ingest: capture move lookup (%s): %v", captureKey, err)
		return 0, false
	}
	return id, true
}

// SweepSessionToReview is lifecycle signal 2: a session reached a terminal
// status, so every card it captured AND the user accepted moves to in_review.
// Returns the ids that really moved, in id order, so the caller publishes
// exactly one task_updated per moved card and nothing at all for a no-op sweep.
//
// Exported for the same reason CaptureSessionCard is: a session becomes terminal
// from two unrelated places — the ingest status ticker ages it to 'completed'
// (pipeline.recomputeStatuses) and the operator kill path flips it to 'killed'
// (internal/api.KillSession) — and neither should need more than a DB handle and
// a session id.
//
// SELECT-then-UPDATE inside ONE transaction, because the caller needs the ids
// and a guarded UPDATE cannot hand them back: doing the two reads outside a
// transaction would let a concurrent drag move a card between them and produce a
// frame for a row this sweep did not touch. The rows are drained and CLOSED
// before the UPDATE is issued — same single-connection reason as above, applied
// within the transaction rather than across it.
//
// Deliberately NOT gated on the skip rules CaptureSessionCard re-evaluates
// (System project, dispatched run): those decide whether a card may be MINTED,
// whereas this only ever touches rows a previous capture already minted and a
// human then accepted. The WHERE clause below is the whole guard, and it is
// strictly narrower than any skip rule could be.
//
// It carries `worktree_path IS NULL` for the same reason moveCapturedToReview
// does, and this signal needs it MORE: origin_session_id is as immutable as
// origin, so a captured card the user reworked and the dispatcher re-admitted
// still answers to its old session — and every session eventually goes terminal,
// so without this conjunct the sweep would flip a live dispatcher run to
// in_review the moment the session that once suggested it completed.
//
// Never returns an error: a board move is a convenience, and no failure here may
// disturb a status transition or fail an operator's kill.
func SweepSessionToReview(db *sql.DB, sessionID int64) []int64 {
	tx, err := db.Begin()
	if err != nil {
		log.Printf("debug: ingest: review sweep begin (session %d): %v", sessionID, err)
		return nil
	}
	defer tx.Rollback() // no-op once Commit below has succeeded

	rows, err := tx.Query(`
		SELECT id FROM tasks
		 WHERE origin_session_id = ?
		   AND board_column = 'in_progress'
		   AND origin != 'manual'
		   AND worktree_path IS NULL
		 ORDER BY id`, sessionID)
	if err != nil {
		log.Printf("debug: ingest: review sweep select (session %d): %v", sessionID, err)
		return nil
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			log.Printf("debug: ingest: review sweep scan (session %d): %v", sessionID, err)
			return nil
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close() // MUST precede the Exec below: one connection, one user at a time
	if err != nil {
		log.Printf("debug: ingest: review sweep rows (session %d): %v", sessionID, err)
		return nil
	}
	if len(ids) == 0 {
		return nil // the common case: this session had nothing accepted
	}
	if _, err := tx.Exec(`
		UPDATE tasks
		   SET board_column = 'in_review', column_moved_at = ?
		 WHERE origin_session_id = ?
		   AND board_column = 'in_progress'
		   AND origin != 'manual'
		   AND worktree_path IS NULL`,
		time.Now().UTC().Format(captureTSFormat), sessionID); err != nil {
		log.Printf("debug: ingest: review sweep update (session %d): %v", sessionID, err)
		return nil
	}
	if err := tx.Commit(); err != nil {
		log.Printf("debug: ingest: review sweep commit (session %d): %v", sessionID, err)
		return nil // rolled back — announce nothing
	}
	return ids
}

// captureSkipped reports whether THIS session may never produce cards,
// memoizing the verdict for the rest of the batch.
//
// The memo is per-ingester (one tail batch), not process-global, and that is
// the intended lifetime: a dispatch link can appear at any moment, so a verdict
// cached for the life of the daemon would keep capturing from a session that
// has since become a dispatched run. Re-deciding once per batch is cheap and
// always current.
func (in *ingester) captureSkipped() bool {
	if in.skipCapture != nil {
		return *in.skipCapture
	}
	skip := in.computeCaptureSkip()
	in.skipCapture = &skip
	return skip
}

func (in *ingester) computeCaptureSkip() bool {
	var path string
	if err := in.tx.QueryRow(
		`SELECT path FROM projects WHERE id = ?`, in.projectID).Scan(&path); err != nil {
		log.Printf("debug: ingest: capture skip check (project %d): %v", in.projectID, err)
		return true // cannot classify the session → do not put cards on the board
	}
	if isSystemProjectPath(path) {
		return true
	}
	return hasExplicitDispatchLink(in.tx, in.sessionUUID, in.sessionID)
}

// isSystemProjectPath reports whether a project row is the deliberate System
// project (~/.swarmery — see SystemDir): daemon-spawned headless runs, phase
// executors, telemetry sweeps. Their todos are the machine's own bookkeeping
// and would bury the operator's board under its own exhaust.
func isSystemProjectPath(path string) bool {
	sys := systemBase()
	return sys != "" && path == sys
}

// hasExplicitDispatchLink reports whether this session IS a dispatched run of a
// board task — either the uuid the dispatcher parked on tasks.dispatch_session_uuid
// before spawning, or a task_sessions row it reconciled afterwards with
// link_source='explicit' (the heuristic links are guesses and deliberately do
// not count). Such a session's work already has a card; capturing its todos
// would mint children of the card that spawned it.
//
// Returns true on a query error too: an unclassifiable session must not reach
// the board.
func hasExplicitDispatchLink(q dbtx, sessionUUID string, sessionID int64) bool {
	var one int
	err := q.QueryRow(`
		SELECT 1 FROM tasks WHERE dispatch_session_uuid = ?
		UNION ALL
		SELECT 1 FROM task_sessions WHERE session_id = ? AND link_source = 'explicit'
		LIMIT 1`, sessionUUID, sessionID).Scan(&one)
	switch {
	case err == nil:
		return true
	case errors.Is(err, sql.ErrNoRows):
		return false
	default:
		log.Printf("debug: ingest: capture dispatch-link check (session %d): %v", sessionID, err)
		return true
	}
}

// clip caps s at n RUNES, unlike the package's byte-wise truncate. Captured
// titles are rendered verbatim on the board, and the operator writes prompts in
// languages where a byte split lands mid-rune and shows as a replacement
// character; the phase's budget is 120 characters, so count characters.
func clip(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimRight(string(r[:n]), " ") + "…"
}

// firstUserPrompt returns the session's opening prompt, read back from the
// user_prompt event ingest already stored (rather than re-reading the
// transcript). Ordered by event id, which for the FIRST prompt is file order:
// the opener is written before anything else and nothing renumbers rows.
// Returns "" when the session has no prompt yet or the read fails.
func firstUserPrompt(q dbtx, sessionID int64, limit int) string {
	var content sql.NullString
	if err := q.QueryRow(`
		SELECT json_extract(payload, '$.content') FROM events
		 WHERE session_id = ? AND type = 'user_prompt'
		 ORDER BY id LIMIT 1`, sessionID).Scan(&content); err != nil {
		return ""
	}
	return clip(strings.TrimSpace(content.String), limit)
}

// captureFooter is the provenance block appended to every captured card's
// prompt: which session it came from, and — when excerptLimit > 0 — enough of
// that session's opening prompt to remind a human what the todo was FOR. A todo
// like "fix the retry helper" is meaningless on a board three days later
// without it. A session card passes 0: its prompt already IS the opening
// prompt, so the excerpt would just repeat the card body.
func captureFooter(q dbtx, sessionID int64, sessionUUID string, excerptLimit int) string {
	var b strings.Builder
	b.WriteString("\n\n---\nCaptured from session ")
	b.WriteString(sessionUUID)
	if excerptLimit <= 0 {
		return b.String()
	}
	if p := firstUserPrompt(q, sessionID, excerptLimit); p != "" {
		b.WriteString("\n\nThat session opened with:\n")
		b.WriteString(p)
	}
	return b.String()
}

// firstLine is the card title source for a session card: a first prompt is
// often a paragraph, and a board column shows one line.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// CaptureSessionCard is hook B: the fallback card for a session that ended
// having captured nothing.
//
// Exported because a session reaches a terminal status from more than one place
// (the ingest status ticker ages it to 'completed'; the operator kill path in
// internal/api flips it to 'killed'), and each of those sites should be able to
// call this with nothing but a DB handle and a session id. It is safe to call
// repeatedly and from anywhere: every skip rule is re-evaluated and the
// 'sess:<uuid>' key makes a second call a no-op.
//
// Runs against *sql.DB rather than a transaction — it fires after the status
// UPDATE has committed, so there is no batch to join and nothing to roll back.
//
// Returns the task id and whether a card was REALLY inserted, so the caller
// publishes exactly one task_updated per new card and none for a repeat call.
// Never returns an error: a fallback card is best-effort by construction, and
// no failure here may disturb a status transition.
func CaptureSessionCard(db *sql.DB, sessionID int64) (int64, bool) {
	var (
		sessionUUID string
		projectID   int64
		path        string
	)
	if err := db.QueryRow(`
		SELECT s.session_uuid, s.project_id, p.path
		  FROM sessions s JOIN projects p ON p.id = s.project_id
		 WHERE s.id = ?`, sessionID).Scan(&sessionUUID, &projectID, &path); err != nil {
		log.Printf("debug: ingest: session card lookup (session %d): %v", sessionID, err)
		return 0, false
	}
	if sessionUUID == "" || projectID == 0 {
		return 0, false
	}
	if isSystemProjectPath(path) || hasExplicitDispatchLink(db, sessionUUID, sessionID) {
		return 0, false
	}
	// Todos already captured → the session's work is on the board in detail and
	// a whole-session card would only duplicate it at a coarser grain.
	var one int
	err := db.QueryRow(
		`SELECT 1 FROM tasks WHERE capture_key LIKE ? LIMIT 1`,
		"todo:"+sessionUUID+":%").Scan(&one)
	switch {
	case err == nil:
		return 0, false
	case errors.Is(err, sql.ErrNoRows): // no todo cards — the fallback applies
	default:
		log.Printf("debug: ingest: session card todo check (session %d): %v", sessionID, err)
		return 0, false
	}
	// A session with no assistant turn produced nothing to remember — a stub row
	// from a hook POST, or a transcript that never got an answer.
	var assistantTurns int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM turns WHERE session_id = ? AND role = 'assistant'`,
		sessionID).Scan(&assistantTurns); err != nil || assistantTurns == 0 {
		return 0, false
	}
	prompt := firstUserPrompt(db, sessionID, sessionPromptLimit)
	if prompt == "" {
		return 0, false // nothing to title or describe the card with
	}
	id, inserted, err := taskcap.InsertCapturedTask(db, taskcap.Input{
		ProjectID:       projectID,
		Title:           clip(firstLine(prompt), captureTitleLimit),
		Prompt:          prompt + captureFooter(db, sessionID, sessionUUID, 0),
		Origin:          "session",
		OriginSessionID: &sessionID,
		CaptureKey:      sessionCaptureKey(sessionUUID),
	})
	if err != nil {
		log.Printf("debug: ingest: session card insert (session %d): %v", sessionID, err)
		return 0, false
	}
	return id, inserted
}
