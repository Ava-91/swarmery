// Package taskcap owns the single write path for CAPTURED board cards — cards
// a machine minted from a session rather than a human typing them into the
// board UI.
//
// It exists as its own package for one structural reason: both writers need it
// and they sit on opposite sides of an import edge. internal/api imports
// internal/ingest (AttachBus takes an *ingest.Bus), so internal/ingest can
// never import internal/api — the capture helper cannot live in either of
// them. taskcap therefore depends on nothing but internal/store (the bottom of
// the import graph, where the board-card constructor lives), which lets the
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
	"errors"
	"fmt"
	"strings"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
)

// DB is the storage surface an insert needs. Both *sql.DB and *sql.Tx satisfy
// it, which is the whole point: the API layer captures against the pool while
// ingest captures inside the transaction that is already writing the
// transcript's turns and events, so a card can never outlive a rolled-back
// tail batch.
type DB = store.DB

const (
	// NormalPriority mirrors internal/api's priorityLabels["normal"] on the
	// existing INTEGER priority scale (0001_init.sql). Captured cards are
	// suggestions — they never jump the queue. api.TestCapturedPriorityMatchesBoardDefault
	// pins the two together (taskcap cannot import api to assert it here).
	NormalPriority = store.NormalPriority

	// tsFormat matches the millisecond-Z style of api.boardTSFormat so a
	// captured row's created_at sorts against hand-created ones lexically.
	tsFormat = store.BoardTSFormat

	// QuoteLimit caps the opening-prompt quote a captured card carries
	// (Input.OriginQuote), in runes. Enough to say what the session was FOR;
	// short enough to render as a chip on the card and to ride into a dispatched
	// prompt without crowding the task itself.
	QuoteLimit = 400
)

// captureOrigins is the subset of the provenance set a CAPTURE may carry.
// 'manual' is the default every hand-written card has and 'verify-fix' is the
// verifier's fix-chain marker — neither is a capture, and InsertCapturedTask
// rejects both. The full closed set lives in store.ValidOrigin, which
// internal/api.validOrigin also reads, so the HTTP validation and the write
// path can never disagree about it.
var captureOrigins = map[string]bool{
	"session": true,
	"llm":     true,
}

// ValidOrigin reports whether o is in the closed provenance set
// (manual | session | llm | verify-fix). Pure.
func ValidOrigin(o string) bool { return store.ValidOrigin(o) }

// Input is the payload of a capture insert: the parts a captured card actually
// varies. Everything else is fixed by construction (see InsertCapturedTask).
type Input struct {
	ProjectID       int64
	Title           string
	Prompt          string
	Origin          string // 'session' | 'llm' — 'manual' is not a capture
	OriginSessionID *int64
	// OriginTurnUUID is the transcript record the card was minted from; OriginQuote
	// is the session's opening prompt (clip it to QuoteLimit); OriginFiles are the
	// files the session had touched by then. Provenance columns (0066) — they are
	// NOT folded into Prompt, so the board can render them on their own and the
	// dispatcher can add them to a run exactly once.
	OriginTurnUUID string
	OriginQuote    string
	OriginFiles    []string
	CaptureKey     string // idempotency key; required
}

// NewExternalID mints a "T-" + 6-char base36 card id (store.NewExternalID).
func NewExternalID() (string, error) { return store.NewExternalID() }

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
// normal priority, status='queued', minted external_id. The row itself is
// written by store.InsertBoardTask — the same constructor every board card
// goes through — so a captured card can never lack what a manual one has.
//
// Callers publish task_updated only when inserted is true — a replay changed
// nothing, and a WS frame for a no-op would make every re-tail look like board
// activity.
func InsertCapturedTask(db DB, in Input) (int64, bool, error) {
	key := strings.TrimSpace(in.CaptureKey)
	if strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.Prompt) == "" {
		return 0, false, errors.New("captured task: title and prompt are required")
	}
	if key == "" {
		return 0, false, errors.New("captured task: captureKey is required")
	}
	if !captureOrigins[in.Origin] {
		return 0, false, fmt.Errorf("captured task: invalid origin %q (want session|llm)", in.Origin)
	}
	return store.InsertBoardTask(db, store.BoardTaskInput{
		ProjectID:       in.ProjectID,
		Title:           in.Title,
		Prompt:          in.Prompt,
		Priority:        NormalPriority,
		Column:          "triage",
		Origin:          in.Origin,
		OriginSessionID: in.OriginSessionID,
		OriginTurnUUID:  in.OriginTurnUUID,
		OriginQuote:     in.OriginQuote,
		OriginFiles:     in.OriginFiles,
		CaptureKey:      key,
	})
}
