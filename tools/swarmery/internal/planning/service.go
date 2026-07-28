// Package planning implements Planning Mode (fusion phase 8): "idea → structured
// plan". POST /api/projects/{id}/planning spawns a headless `claude -p
// --session-id <uuid>` planner run in the project's directory; it asks
// clarifying questions as reply text (the phase-8 spike proved AskUserQuestion
// does NOT fire the permission hook under `-p`), the user answers via the
// existing session-resume chat, and the run writes a plan into the private
// workspace which wsingest surfaces as a workspace task row the user can
// "activate" into board tasks.
//
// No new tables (phase-8 spec): the run IS a normal session (ingested normally),
// so single-flight state lives in-memory — a map[projectID]run keyed by project,
// guarded by one mutex, exactly the idiom of api/session_message.go's msgInFlight.
// A daemon restart clears in-flight planning (the orphaned claude process has
// either finished writing its plan or the user re-triggers). The pre-generated
// session uuid is reconciled to the ingested sessions row lazily (SessionID in
// the status snapshot), mirroring dispatch's dispatch_session_uuid → session_id.
package planning

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"sync"
	"time"
)

// Sentinel errors mapped to HTTP statuses by the api layer.
var (
	// ErrActive: a planner run is already in flight for this project (409).
	ErrActive = errors.New("a planning run is already active for this project")
	// ErrProjectNotFound: no project row for the given id (404).
	ErrProjectNotFound = errors.New("project not found")
	// ErrNoPath: the project has no filesystem path to run the planner in (409).
	ErrNoPath = errors.New("project has no known path to plan in")
)

// run is one in-flight planner: its cancel (aborts the child claude), start
// time (drives the live "planning… (Ns)" indicator), and the pre-generated
// session uuid (the explicit task↔session link, reconciled lazily on read).
type run struct {
	cancel    context.CancelFunc
	startedAt time.Time
	uuid      string
}

// Service owns the planner-run lifecycle: single-flight admission, spawn, and
// the status snapshot. Notify (wired to api.publishSessionUpdated) is emitted at
// the run's edges so an open Planning page flips its Start button live — reusing
// the FROZEN session_updated frame, no new WS type.
type Service struct {
	DB   *sql.DB
	Run  Runner
	UUID func() string    // session-uuid generator (test seam; default newUUID)
	now  func() time.Time // clock (test seam; default time.Now)
	Go   func(func())     // async-spawn seam (nil ⇒ real `go`); mirrors improveGo
	// Notify emits a session_updated for a project's in-flight change. The api
	// layer has no session id at spawn time (the row is minted later by ingest),
	// so this is keyed by PROJECT id; the api adapter republishes the project's
	// sessions. nil ⇒ no live nudge (guarded).
	Notify func(projectID int64)
	// ResumeInFlight reports whether the api layer is running a `claude -r`
	// resume for a session uuid — the second half of processAlive (the wizard's
	// raw-fallback parse and stale reconcile must not fire mid-resume). Wired by
	// api.AttachPlanning; nil ⇒ no resume tracking (bare unit tests).
	ResumeInFlight func(sessionUUID string) bool

	mu     sync.Mutex
	active map[int64]run // projectID → in-flight planner
}

// NewService builds a planning service. The caller wires DB + Run (ClaudeRunner);
// UUID/now/Go default to production impls.
func NewService(db *sql.DB, r Runner) *Service {
	return &Service{
		DB:     db,
		Run:    r,
		UUID:   newUUID,
		now:    time.Now,
		active: make(map[int64]run),
	}
}

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Service) spawn(fn func()) {
	wrapped := func() {
		// A panic in a planner goroutine must never take the daemon down —
		// recover + log (mirrors spawnImprove / dispatch.spawn).
		defer func() {
			if r := recover(); r != nil {
				log.Printf("error: planning: goroutine panic recovered: %v", r)
			}
		}()
		fn()
	}
	if s.Go != nil {
		s.Go(wrapped)
		return
	}
	go wrapped()
}

func (s *Service) notify(projectID int64) {
	if s.Notify != nil {
		s.Notify(projectID)
	}
}

// Status is one project's planner state (GET /api/projects/{id}/planning).
type Status struct {
	Active bool `json:"active"`
	// SessionUUID is the pre-generated planner session uuid (present while
	// active), so the page can link to /sessions/{uuid} and match the
	// transcript even before the numeric row is minted.
	SessionUUID string `json:"sessionUuid"`
	// SessionID is the numeric sessions row id once the transcript/hook has
	// minted it (null until then) — the page filters approvals + reads turns by
	// it. Resolved lazily from session_uuid, mirroring dispatch's link.
	SessionID *int64 `json:"sessionId"`
	// StartedAt is the RFC3339 start of the in-flight run, for a live timer.
	StartedAt *string `json:"startedAt"`
}

// Start admits a planner run for a project: single-flight (ErrActive when one is
// already in flight), project + path validation, then spawns the headless run
// and returns the pre-generated session uuid so the caller answers 202
// immediately. The run's own goroutine owns exit handling and slot release.
func (s *Service) Start(projectID int64, idea string) (sessionUUID string, err error) {
	// Validate the project + resolve its path BEFORE taking a slot (a phantom
	// project or a pathless one is a clean client error, not a wedged slot).
	var path sql.NullString
	qerr := s.DB.QueryRow(`SELECT path FROM projects WHERE id = ?`, projectID).Scan(&path)
	if errors.Is(qerr, sql.ErrNoRows) {
		return "", ErrProjectNotFound
	}
	if qerr != nil {
		return "", qerr
	}
	if !path.Valid || path.String == "" {
		return "", ErrNoPath
	}

	s.mu.Lock()
	if _, busy := s.active[projectID]; busy {
		s.mu.Unlock()
		return "", ErrActive
	}
	uuid := s.UUID()
	ctx, cancel := context.WithCancel(context.Background())
	s.active[projectID] = run{cancel: cancel, startedAt: s.clock(), uuid: uuid}
	s.mu.Unlock()

	// Durable wizard row (phase 2). Supersede any still-open previous wizard
	// first — the newest row IS the project's wizard (newestWizard), so leaving
	// an old awaiting_answer row open would resurrect it once this run ends.
	now := s.ts()
	if s.markCancelled(projectID) {
		log.Printf("planning: project=%d superseded an open wizard", projectID)
	}
	if _, err := s.DB.Exec(
		`INSERT INTO planning_sessions(project_id, session_uuid, status, idea, created_at, updated_at)
		 VALUES(?,?,?,?,?,?)`,
		projectID, uuid, StatusGenerating, idea, now, now); err != nil {
		// Non-fatal: the run still executes; the wizard just has no durable row
		// (OnSessionTurns will no-op on the uuid miss).
		log.Printf("error: planning: insert wizard row project=%d uuid=%s: %v", projectID, uuid, err)
	}

	log.Printf("planning: start project=%d uuid=%s cwd=%q (%d chars idea)", projectID, uuid, path.String, len(idea))
	s.notify(projectID) // active=true → page shows the run

	spec := RunSpec{Prompt: BuildPrompt(idea), SessionUUID: uuid, Cwd: path.String}
	s.spawn(func() { s.runAndHandle(ctx, cancel, projectID, spec) })
	return uuid, nil
}

// runAndHandle executes the planner run to completion and always releases the
// slot. The transcript is the source of truth for the resulting turns/plan
// (ingested independently); here we only log the outcome and re-emit the frozen
// session_updated at the run's edges.
func (s *Service) runAndHandle(ctx context.Context, cancel context.CancelFunc, projectID int64, spec RunSpec) {
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.active, projectID)
		s.mu.Unlock()
		s.notify(projectID) // active=false → page re-enables Start
		// Settle pass (phase 2): ingest may have tailed the final transcript
		// lines BEFORE the process exited, in which case the raw-fallback path
		// was still gated on processAlive and no further bus notification will
		// arrive — re-run the parse now that the slot is released. Idempotent
		// (question turns / terminal states no-op).
		s.OnSessionTurns(spec.SessionUUID)
	}()

	run, err := s.Run.Start(ctx, spec)
	if err != nil {
		log.Printf("error: planning: run project=%d uuid=%s could not start: %v", projectID, spec.SessionUUID, err)
		s.markFailed(spec.SessionUUID)
		return
	}
	switch {
	case run.TimedOut:
		log.Printf("warning: planning: run project=%d uuid=%s timed out", projectID, spec.SessionUUID)
		s.markFailed(spec.SessionUUID) // guarded on 'generating' — never clobbers cancelled
	case run.ExitCode != 0:
		log.Printf("warning: planning: run project=%d uuid=%s exited %d: %s", projectID, spec.SessionUUID, run.ExitCode, run.Stderr)
		s.markFailed(spec.SessionUUID)
	default:
		log.Printf("planning: run project=%d uuid=%s completed in %s", projectID, spec.SessionUUID, run.Duration)
	}
}

// Cancel aborts the project's wizard: stamps every open planning_sessions row
// cancelled AND kills an in-flight child claude if one is running. Returns
// whether anything was cancelled (a process or an open row — an awaiting
// wizard with no live process is still dismissible). The stamp happens BEFORE
// the kill so runAndHandle's markFailed (guarded on status='generating')
// cannot race the cancellation into 'failed'.
func (s *Service) Cancel(projectID int64) bool {
	stamped := s.markCancelled(projectID)
	s.mu.Lock()
	r, ok := s.active[projectID]
	s.mu.Unlock()
	if ok {
		r.cancel()
	}
	if stamped {
		s.notify(projectID)
	}
	return ok || stamped
}

// Snapshot builds the status for a project: active flag, the pre-generated uuid,
// the numeric session id (resolved lazily from the uuid once ingest/the hook
// mints the row), and the start time.
func (s *Service) Snapshot(projectID int64) Status {
	s.mu.Lock()
	r, active := s.active[projectID]
	s.mu.Unlock()
	if !active {
		return Status{Active: false}
	}
	st := Status{Active: true, SessionUUID: r.uuid}
	started := r.startedAt.UTC().Format(time.RFC3339)
	st.StartedAt = &started
	// Lazily reconcile the numeric session id (the row is minted by ingest / the
	// permission hook after spawn). A miss just leaves SessionID nil — the page
	// falls back to the uuid until it resolves, same as dispatch's link.
	var sid int64
	if err := s.DB.QueryRow(`SELECT id FROM sessions WHERE session_uuid = ?`, r.uuid).Scan(&sid); err == nil {
		st.SessionID = &sid
	} else if !errors.Is(err, sql.ErrNoRows) {
		log.Printf("error: planning: resolve session id for uuid %s: %v", r.uuid, err)
	}
	return st
}
