// Package retroanalysis turns a retrospective digest into a saved, human-
// gated analysis of the agent system.
//
// The shape of the feature is a state machine with one deliberate human gate
// in the middle:
//
//	running → proposed → accepted → planned
//	   ↓          ↓
//	 failed   dismissed
//
// Nothing downstream — no repository, no workspace, no planning session —
// happens before an operator moves a row to `accepted`. That gate is the
// product, not a formality: the analysis is model-written prose about how the
// system should change, and prose is exactly the artifact that most deserves
// to be argued with before it becomes work.
package retroanalysis

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Sentinel errors the API layer maps onto status codes.
var (
	// ErrAnalysisRunning — one analysis at a time, globally. Concurrent runs
	// would race on the same window and hand the operator two competing
	// documents to gate.
	ErrAnalysisRunning = errors.New("an analysis is already running")
	// ErrNotFound — no such analysis row.
	ErrNotFound = errors.New("analysis not found")
	// ErrBadTransition — the requested status change is not in the lifecycle.
	ErrBadTransition = errors.New("illegal analysis status transition")
)

// Analysis is one saved row.
type Analysis struct {
	ID                  int64   `json:"id"`
	WindowFrom          string  `json:"windowFrom"`
	WindowTo            string  `json:"windowTo"`
	Scope               string  `json:"scope"`
	DigestSHA256        string  `json:"digestSha256"`
	Markdown            string  `json:"markdown"`
	Citations           int64   `json:"citations"`
	Status              string  `json:"status"`
	Error               string  `json:"error"`
	PlanningSessionUUID string  `json:"planningSessionUuid"`
	CreatedAt           string  `json:"createdAt"`
	DecidedAt           *string `json:"decidedAt"`
}

// Service owns the analysis lifecycle.
type Service struct {
	DB     *sql.DB
	Runner Runner
	// Go, when non-nil, replaces `go fn()` for the async run — the test seam
	// that makes an httptest round trip deterministic (mirrors the improveGo
	// seam in internal/api).
	Go func(func())
	// Now, when non-nil, replaces time.Now (tests pin timestamps).
	Now func() time.Time

	mu      sync.Mutex
	running bool
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// tsFormat matches the RFC3339 UTC form the rest of the schema stores.
const tsFormat = "2006-01-02T15:04:05.000Z"

func (s *Service) stamp() string { return s.now().UTC().Format(tsFormat) }

// Start inserts a `running` row and generates the analysis for it.
//
// The insert happens synchronously so the caller can answer with a real id and
// the UI has something to poll immediately; only the headless run is deferred.
// Returns ErrAnalysisRunning when one is already in flight.
func (s *Service) Start(from, to, scope, digest, digestSHA string) (int64, error) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return 0, ErrAnalysisRunning
	}
	s.running = true
	s.mu.Unlock()

	res, err := s.DB.Exec(
		`INSERT INTO retro_analyses (window_from, window_to, scope, digest_sha256, status, created_at)
		 VALUES (?, ?, ?, ?, 'running', ?)`,
		from, to, nullIfEmpty(scope), digestSHA, s.stamp())
	if err != nil {
		s.release()
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		s.release()
		return 0, err
	}

	run := func() {
		defer s.release()
		s.generate(id, digest)
	}
	if s.Go != nil {
		s.Go(run)
	} else {
		go run()
	}
	return id, nil
}

func (s *Service) release() {
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
}

// Running reports whether an analysis is in flight right now.
func (s *Service) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// generate runs the model and resolves the row to `proposed` or `failed`.
// It never returns an error: the row IS the error channel, because the caller
// has already answered 202 and the operator watches the row, not a log.
func (s *Service) generate(id int64, digest string) {
	out, err := s.Runner.Run(context.Background(), BuildPrompt(digest))
	if err != nil {
		s.fail(id, err.Error())
		return
	}
	md := strings.TrimSpace(out)
	citations, verr := Validate(md, AllowedCitations(digest))
	if verr != nil {
		// Keep the rejected text on the row. An operator debugging a refusal
		// needs to see what the model actually said, and a failure that hides
		// its input is a failure you cannot learn from.
		s.failWithBody(id, verr.Error(), md)
		return
	}
	if _, err := s.DB.Exec(
		`UPDATE retro_analyses SET status='proposed', markdown=?, citations=?, error=NULL WHERE id=?`,
		md, citations, id); err != nil {
		s.fail(id, "persisting the analysis failed: "+err.Error())
	}
}

func (s *Service) fail(id int64, reason string) { s.failWithBody(id, reason, "") }

func (s *Service) failWithBody(id int64, reason, md string) {
	if _, err := s.DB.Exec(
		`UPDATE retro_analyses SET status='failed', error=?, markdown=?, decided_at=? WHERE id=?`,
		reason, md, s.stamp(), id); err != nil {
		// Nothing better is available: the row is the error channel and the
		// channel itself just broke.
		fmt.Printf("error: retroanalysis: marking %d failed: %v\n", id, err)
	}
}

const selectCols = `id, window_from, window_to, COALESCE(scope, ''), digest_sha256, markdown,
	citations, status, COALESCE(error, ''), COALESCE(planning_session_uuid, ''),
	created_at, decided_at`

func scanAnalysis(row interface{ Scan(...any) error }) (*Analysis, error) {
	var a Analysis
	var decided sql.NullString
	if err := row.Scan(&a.ID, &a.WindowFrom, &a.WindowTo, &a.Scope, &a.DigestSHA256,
		&a.Markdown, &a.Citations, &a.Status, &a.Error, &a.PlanningSessionUUID,
		&a.CreatedAt, &decided); err != nil {
		return nil, err
	}
	if decided.Valid {
		a.DecidedAt = &decided.String
	}
	return &a, nil
}

// Latest returns the newest analysis, optionally within a project scope.
// Returns (nil, nil) when there is none — an empty page is not an error.
//
// scope "" means fleet-wide and matches ONLY fleet-wide rows: a project's
// analysis reasons about a different slice of evidence, and showing it under
// an unscoped view would misattribute it.
func (s *Service) Latest(scope string) (*Analysis, error) {
	q := `SELECT ` + selectCols + ` FROM retro_analyses WHERE scope IS NULL`
	var args []any
	if scope != "" {
		q = `SELECT ` + selectCols + ` FROM retro_analyses WHERE scope = ?`
		args = []any{scope}
	}
	q += ` ORDER BY id DESC LIMIT 1`
	a, err := scanAnalysis(s.DB.QueryRow(q, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return a, err
}

// Get returns one analysis by id.
func (s *Service) Get(id int64) (*Analysis, error) {
	a, err := scanAnalysis(s.DB.QueryRow(`SELECT `+selectCols+` FROM retro_analyses WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

// legalDecision is the operator-driven half of the lifecycle. `planned` is NOT
// here: it is set by the planning handoff, which has to write the session uuid
// in the same breath, and letting a bare PATCH reach it would produce a
// 'planned' row pointing at no session.
func legalDecision(from, to string) bool {
	return from == "proposed" && (to == "accepted" || to == "dismissed")
}

// Decide moves a proposed analysis to accepted or dismissed.
func (s *Service) Decide(id int64, status string) (*Analysis, error) {
	cur, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if !legalDecision(cur.Status, status) {
		return nil, fmt.Errorf("%w: %s → %s", ErrBadTransition, cur.Status, status)
	}
	if _, err := s.DB.Exec(
		`UPDATE retro_analyses SET status=?, decided_at=? WHERE id=? AND status='proposed'`,
		status, s.stamp(), id); err != nil {
		return nil, err
	}
	return s.Get(id)
}

// MarkPlanned records that an accepted analysis started a planning session.
// Only `accepted` may reach `planned` — that is the SC-5 gate in code.
func (s *Service) MarkPlanned(id int64, sessionUUID string) (*Analysis, error) {
	cur, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if cur.Status != "accepted" {
		return nil, fmt.Errorf("%w: %s → planned", ErrBadTransition, cur.Status)
	}
	if _, err := s.DB.Exec(
		`UPDATE retro_analyses SET status='planned', planning_session_uuid=? WHERE id=? AND status='accepted'`,
		sessionUUID, id); err != nil {
		return nil, err
	}
	return s.Get(id)
}

// HealStale resolves rows a previous daemon left mid-run: the process that
// owned them is gone, so they can never finish. Best-effort, called at
// startup — same contract as provision.HealStale.
func (s *Service) HealStale() error {
	_, err := s.DB.Exec(
		`UPDATE retro_analyses SET status='failed', error=?, decided_at=? WHERE status='running'`,
		"the daemon restarted while this analysis was generating", s.stamp())
	return err
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
