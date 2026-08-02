// Package extract turns ONE session into suggested Triage cards with a model.
//
// It is the deliberate opposite of internal/ingest's capture hooks in every
// dimension that matters:
//
//	ingest capture          extract
//	------------------------------------------------------------------
//	automatic, every batch  on demand, one operator click
//	deterministic           a paid model run
//	free                    costs a session
//	origin='session'        origin='llm'
//
// That difference is the whole reason this exists as a separate, optional path:
// deterministic capture reads what a session WROTE DOWN (its TodoWrite list),
// so a session that did substantial work without ever writing a todo produces
// one coarse fallback card. This package reads what the session SAID and asks a
// model to name the leftovers. It is advisory and manual-trigger only — there
// is no ticker, no automatic pass, and nothing here runs unless a human presses
// the button.
//
// What it shares with capture is everything that touches the board:
// taskcap.InsertCapturedTask is still the only write path, capture_key still
// makes re-runs idempotent, and internal/ingest.CaptureSkipReason is still the
// one predicate that decides whether a session may mint cards at all. Phase 4's
// lifecycle sync then applies to these cards for free — it keys on
// `origin != 'manual'` + origin_session_id, both of which taskcap sets here.
package extract

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/ingest"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/taskcap"
)

// captureKeyHashLen is how much of the title SHA-1 goes into a capture_key.
// 12 hex chars = 48 bits, scoped to ONE session's extraction — matches
// internal/ingest's todo keys, and collision would need two tasks in the same
// session whose normalized titles hash alike.
const captureKeyHashLen = 12

var (
	// ErrAlreadyRunning is returned when an extraction is already in flight for
	// the session. Single-flight is in-memory (see Service.running) rather than
	// DB-backed like internal/verify's: an extraction leaves no run row to hold
	// a unique index against, and the guard only needs to outlive the request.
	ErrAlreadyRunning = errors.New("extract: an extraction is already running for this session")
	// ErrSessionNotFound is returned for an unknown session id.
	ErrSessionNotFound = errors.New("extract: session not found")
)

// ErrSkipped reports that the session may not mint cards at all, carrying the
// reason internal/ingest gave so the operator sees WHY the button did nothing.
type ErrSkipped struct{ Reason string }

func (e *ErrSkipped) Error() string { return "extract: session not eligible for capture: " + e.Reason }

// Service owns on-demand extraction: single-flight per session, the digest, the
// model run, the parse, and the idempotent insert. Async execution is the
// caller's job, mirroring internal/verify and internal/improve.
type Service struct {
	DB  *sql.DB
	Run Runner
	// Notify emits task_updated (wired to api.publishTaskUpdated) so a new card
	// reaches the board over the FROZEN WS bus — no new message type.
	Notify func(taskID int64)

	mu      sync.Mutex
	running map[int64]struct{}
}

// NewService builds a Service with the production runner.
func NewService(db *sql.DB) *Service {
	return &Service{DB: db, Run: ClaudeRunner{}}
}

// acquire takes the single-flight slot for sessionID, reporting whether it was
// free. The map is lazily built so a zero-value Service (tests construct one by
// literal) works without a constructor.
func (s *Service) acquire(sessionID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, busy := s.running[sessionID]; busy {
		return false
	}
	if s.running == nil {
		s.running = make(map[int64]struct{})
	}
	s.running[sessionID] = struct{}{}
	return true
}

func (s *Service) release(sessionID int64) {
	s.mu.Lock()
	delete(s.running, sessionID)
	s.mu.Unlock()
}

// Running reports whether an extraction is in flight for sessionID. Exported so
// the HTTP layer can answer 409 in its synchronous pre-flight without taking
// the slot (the real guard is still acquire — this is a courtesy check, and a
// race between the two only means the second caller gets its 409 from
// ExtractTasks instead of from the pre-flight).
func (s *Service) Running(sessionID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, busy := s.running[sessionID]
	return busy
}

// Eligible reports whether sessionID exists and may mint cards, returning the
// skip reason when it may not. It is the pre-flight the endpoint runs to answer
// 404/409 synchronously; ExtractTasks re-checks both, so this is never the only
// guard.
func (s *Service) Eligible(sessionID int64) (reason string, ok bool, err error) {
	var one int
	switch err := s.DB.QueryRow(`SELECT 1 FROM sessions WHERE id = ?`, sessionID).Scan(&one); {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, ErrSessionNotFound
	case err != nil:
		return "", false, err
	}
	if reason, skip := ingest.CaptureSkipReason(s.DB, sessionID); skip {
		return reason, false, nil
	}
	return "", true, nil
}

// ExtractTasks runs one extraction pass over a session and returns how many
// cards it REALLY inserted.
//
// Blocking: the caller decides whether to spawn it. The count is the point of
// the return value — the operator pressed a button and the answer to "what did
// that do" is a number, so this cannot be fire-and-forget the way
// internal/verify's stamp-a-verdict flow can.
//
// The count is inserts, not parsed tasks: a re-run over an unchanged session
// re-derives the same capture_keys, every insert is a no-op, and the honest
// answer is 0 new. That is the idempotency the whole capture design rests on,
// and it is what makes the button safe to press twice.
func (s *Service) ExtractTasks(ctx context.Context, sessionID int64) (int, error) {
	if !s.acquire(sessionID) {
		return 0, ErrAlreadyRunning
	}
	defer s.release(sessionID)

	var (
		sessionUUID string
		projectID   int64
	)
	err := s.DB.QueryRow(
		`SELECT session_uuid, project_id FROM sessions WHERE id = ?`, sessionID).
		Scan(&sessionUUID, &projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrSessionNotFound
	}
	if err != nil {
		return 0, err
	}
	if sessionUUID == "" || projectID == 0 {
		return 0, ErrSessionNotFound
	}
	// Re-checked here and not only in the endpoint's pre-flight: a dispatch link
	// can appear between the two (the dispatcher parks dispatch_session_uuid
	// before it spawns), and the check that must win is the one closest to the
	// insert.
	if reason, skip := ingest.CaptureSkipReason(s.DB, sessionID); skip {
		return 0, &ErrSkipped{Reason: reason}
	}

	digest, err := Digest(s.DB, sessionID)
	if err != nil {
		return 0, fmt.Errorf("extract: digest (session %d): %w", sessionID, err)
	}
	raw, err := s.Run.Run(ctx, buildPrompt(digest))
	if err != nil {
		return 0, err
	}
	tasks, dropped, err := parseTasks(raw)
	if err != nil {
		return 0, err
	}
	if dropped > 0 {
		// No silent caps: the operator's toast reports what landed, and the log
		// reports what the model produced and this refused.
		log.Printf("warning: extract: session %d: dropped %d malformed/overflow task(s) from the model answer", sessionID, dropped)
	}

	inserted := 0
	for _, t := range tasks {
		id, isNew, err := taskcap.InsertCapturedTask(s.DB, taskcap.Input{
			ProjectID:       projectID,
			Title:           t.Title,
			Prompt:          t.Prompt + extractFooter(sessionUUID),
			Origin:          "llm",
			OriginSessionID: &sessionID,
			CaptureKey:      CaptureKey(sessionUUID, t.Title),
		})
		if err != nil {
			// Storage- or schema-level, i.e. identical for every remaining task:
			// report what already landed rather than repeating the failure N times.
			return inserted, fmt.Errorf("extract: insert card (session %d): %w", sessionID, err)
		}
		if !isNew {
			continue // replay of a previous extraction — not board activity
		}
		inserted++
		if s.Notify != nil {
			s.Notify(id)
		}
	}
	return inserted, nil
}

// normalizeTitle is the capture_key normalization: collapse every whitespace
// run to a single space, lowercase. It exists so that a re-run whose model
// re-words a title only in case or spacing converges on the SAME key instead of
// depositing a second copy of the card.
//
// A deliberate near-copy of internal/ingest.normalizeTodoContent rather than a
// shared helper: the two keyspaces are independent by design ('todo:' vs
// 'llm:'), and coupling them would mean a change made for one capture path
// silently re-keys — and therefore duplicates — every card of the other.
func normalizeTitle(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// CaptureKey is 'llm:<session-uuid>:<first 12 hex of sha1(normalized title)>'.
// SHA-1 is a dedup key, never a security claim. Exported so tests can assert
// idempotency against the same key the service writes.
func CaptureKey(sessionUUID, title string) string {
	sum := sha1.Sum([]byte(normalizeTitle(title)))
	return "llm:" + sessionUUID + ":" + hex.EncodeToString(sum[:])[:captureKeyHashLen]
}

// extractFooter is the provenance block appended to an extracted card's prompt.
// It names the model pass explicitly — unlike a captured todo, this text was
// WRITTEN by a model rather than by the operator, and a card acted on three
// days later should say so.
func extractFooter(sessionUUID string) string {
	return "\n\n---\nExtracted by a model pass over session " + sessionUUID
}
