// Package taskcap owns the single write path for CAPTURED board cards — cards
// a machine minted from a session rather than a human typing them into the
// board UI.
//
// It exists as its own package for one structural reason: both writers need it
// and they sit on opposite sides of an import edge. internal/api imports
// internal/ingest (AttachBus takes an *ingest.Bus), so internal/ingest can
// never import internal/api — the capture helper cannot live in either of
// them. taskcap therefore depends on nothing but database/sql, which lets the
// API layer (LLM/manual capture endpoints) and the ingest layer (live
// transcript capture) share ONE definition of what a captured card is.
//
// The package is deliberately storage-only: it inserts a row and reports
// whether the row was new. It never touches the notification bus — publishing
// belongs to the caller, which knows its own transaction and delivery rules
// (the API publishes immediately; ingest publishes only after its tail
// transaction commits).
package taskcap

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DB is the storage surface an insert needs. Both *sql.DB and *sql.Tx satisfy
// it, which is the whole point: the API layer captures against the pool while
// ingest captures inside the transaction that is already writing the
// transcript's turns and events, so a card can never outlive a rolled-back
// tail batch.
type DB interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

const (
	// NormalPriority mirrors internal/api's priorityLabels["normal"] on the
	// existing INTEGER priority scale (0001_init.sql). Captured cards are
	// suggestions — they never jump the queue. api.TestCapturedPriorityMatchesBoardDefault
	// pins the two together (taskcap cannot import api to assert it here).
	NormalPriority = 5

	// tsFormat matches the millisecond-Z style of api.boardTSFormat so a
	// captured row's created_at sorts against hand-created ones lexically.
	tsFormat = "2006-01-02T15:04:05.000Z"

	// externalIDAlphabet / externalIDLen mint the "T-xxxxxx" card id that the
	// dispatcher and commit trailers reference.
	externalIDAlphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	externalIDLen      = 6
)

// origins is the closed set of task provenances (migration 0048). 'manual' is
// the default every hand-written card carries and is NOT a capture origin —
// InsertCapturedTask rejects it. internal/api.validOrigin delegates here so the
// HTTP validation and the write path can never disagree about the set.
var origins = map[string]bool{
	"manual":  true,
	"session": true,
	"llm":     true,
}

// ValidOrigin reports whether o is in the closed provenance set. Pure.
func ValidOrigin(o string) bool { return origins[o] }

// Input is the payload of a capture insert: the parts a captured card actually
// varies. Everything else is fixed by construction (see InsertCapturedTask).
type Input struct {
	ProjectID       int64
	Title           string
	Prompt          string
	Origin          string // 'session' | 'llm' — 'manual' is not a capture
	OriginSessionID *int64
	CaptureKey      string // idempotency key; required
}

// NewExternalID mints a "T-" + 6-char base36 card id. The tasks.id INTEGER PK
// is autoincremented by SQLite; this string is the external_id.
func NewExternalID() (string, error) {
	buf := make([]byte, externalIDLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = externalIDAlphabet[int(buf[i])%len(externalIDAlphabet)]
	}
	return "T-" + string(buf), nil
}

// InsertCapturedTask inserts a suggested board card; returns (id, inserted=false)
// when capture_key already exists (idempotent replay).
//
// This is the ONLY write path for non-manual cards: capture re-reads the same
// transcript on every re-tail and restart, so "insert" has to mean "insert once
// per capture_key" or a session would grow a fresh copy of its cards each
// sweep. The dedupe is the partial unique index from 0048 rather than a
// read-then-write check, so two capture passes racing on the same key can only
// produce one row.
//
// Fixed by construction: source='queue' (board rows, disjoint from workspace
// ingest), board_column='triage' (a suggestion is not yet accepted work),
// normal priority, status='queued', minted external_id.
//
// Callers publish task_updated only when inserted is true — a replay changed
// nothing, and a WS frame for a no-op would make every re-tail look like board
// activity.
func InsertCapturedTask(db DB, in Input) (int64, bool, error) {
	title := strings.TrimSpace(in.Title)
	prompt := strings.TrimSpace(in.Prompt)
	key := strings.TrimSpace(in.CaptureKey)
	if title == "" || prompt == "" {
		return 0, false, errors.New("captured task: title and prompt are required")
	}
	if key == "" {
		return 0, false, errors.New("captured task: captureKey is required")
	}
	if in.Origin == "manual" || !ValidOrigin(in.Origin) {
		return 0, false, fmt.Errorf("captured task: invalid origin %q (want session|llm)", in.Origin)
	}
	extID, err := NewExternalID()
	if err != nil {
		return 0, false, err
	}
	now := time.Now().UTC().Format(tsFormat)
	// The partial index's predicate must be repeated in the conflict target —
	// SQLite needs it to know which index the upsert is aimed at. A bare
	// ON CONFLICT(capture_key) does not compile against a partial index.
	res, err := db.Exec(`
		INSERT INTO tasks (project_id, title, prompt, priority, status, created_at,
		                   source, external_id, board_column, file_scope, dependencies,
		                   origin, origin_session_id, capture_key)
		VALUES (?, ?, ?, ?, 'queued', ?, 'queue', ?, 'triage', '[]', '[]', ?, ?, ?)
		ON CONFLICT(capture_key) WHERE capture_key IS NOT NULL DO NOTHING`,
		in.ProjectID, title, prompt, NormalPriority, now,
		extID, in.Origin, in.OriginSessionID, key)
	if err != nil {
		return 0, false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, false, err
	}
	if n == 0 {
		// Replay: the card already exists. Hand back its id so the caller can
		// still link to it (a re-capture is a no-op, not a failure).
		var id int64
		if err := db.QueryRow(`SELECT id FROM tasks WHERE capture_key = ?`, key).Scan(&id); err != nil {
			return 0, false, err
		}
		return id, false, nil
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}
