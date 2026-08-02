// Task board API (fusion phase 1 — task queue): promotes the read-only tasks
// table into a dispatchable board queue. This file owns the WRITE surface
// (POST create, PATCH move/edit) and the board LIST/detail reads; the existing
// tasks.go keeps the workspace 14-day summary read API unchanged.
//
// Board rows created here carry source='queue' (the existing "created from the
// dashboard" enum value, 0006_workspaces.sql) and workspace_id=NULL, so they
// stay disjoint from workspace-ingested rows (source='workspace', unique on
// (workspace_id, external_id)): they never leak into the workspace summary
// query (WHERE source='workspace') and never hit the workspace upsert
// constraint (its WHERE workspace_id IS NOT NULL excludes NULL rows).
//
// Row storage is inline SQL through h.DB, matching approval_rules.go / retro.go
// (this codebase has no store.Store method layer for API-managed tables;
// single-writer discipline is the one daemon process, not a struct). The
// plan's TaskPatch/validation semantics live here as pure helper funcs
// (normalizePriority, board-column set, transition check, JSON round-trip) so
// they are unit-testable without a DB.
package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/ingest"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/staleness"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/wsingest"
)

// boardTSFormat matches the millisecond-Z style of the other API timestamps.
const boardTSFormat = "2006-01-02T15:04:05.000Z"

// boardColumns is the closed set of kanban columns (Fusion builtin:coding
// semantics). Validated in Go; the migration defaults existing rows to triage.
var boardColumns = map[string]bool{
	"triage":      true,
	"todo":        true,
	"in_progress": true,
	"in_review":   true,
	"done":        true,
	"archived":    true,
}

// priorityLabels maps the accepted string tokens to the existing INTEGER
// priority scale (0001_init.sql, default 5). urgent < high < normal < low so
// idx_tasks_queue(status, priority, created_at) keeps meaningful ordering.
var priorityLabels = map[string]int{
	"urgent": 1,
	"high":   3,
	"normal": 5,
	"low":    7,
}

// priorityFromInt is the inverse used when serializing a row back to a DTO.
// Unknown/legacy values (e.g. the raw default 5 from workspace rows) map to the
// nearest label, defaulting to "normal".
func priorityFromInt(p int) string {
	switch {
	case p <= 1:
		return "urgent"
	case p <= 3:
		return "high"
	case p <= 5:
		return "normal"
	default:
		return "low"
	}
}

// normalizePriority validates a priority token and returns its integer form.
// Empty string → normal (the default). Pure; unit-tested.
func normalizePriority(token string) (int, error) {
	token = strings.TrimSpace(strings.ToLower(token))
	if token == "" {
		return priorityLabels["normal"], nil
	}
	v, ok := priorityLabels[token]
	if !ok {
		return 0, fmt.Errorf("invalid priority %q (want urgent|high|normal|low)", token)
	}
	return v, nil
}

// validColumn reports whether c is in the closed board set. Pure; unit-tested.
func validColumn(c string) bool { return boardColumns[c] }

// legalTransition enforces the two board rules from the phase doc: any→archived
// is always allowed; done→in_progress is rejected (recovery rehome is
// dispatcher-owned, not user-facing); everything else is permissive. Pure;
// unit-tested.
func legalTransition(from, to string) error {
	if to == "archived" {
		return nil
	}
	if from == "done" && to == "in_progress" {
		return errors.New("illegal transition done→in_progress (recovery is dispatcher-owned)")
	}
	return nil
}

// marshalStringList renders a []string as a compact JSON array for storage.
// nil → "[]". Pure; unit-tested via round-trip.
func marshalStringList(xs []string) (string, error) {
	if xs == nil {
		return "[]", nil
	}
	b, err := json.Marshal(xs)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// unmarshalStringList parses a stored JSON array back to []string. Empty/NULL
// storage → empty slice. Pure; unit-tested via round-trip.
func unmarshalStringList(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return []string{}, nil
	}
	var xs []string
	if err := json.Unmarshal([]byte(s), &xs); err != nil {
		return nil, err
	}
	if xs == nil {
		xs = []string{}
	}
	return xs, nil
}

// newBoardExternalID mints a "T-" + 6-char base36 card id. The tasks.id INTEGER
// PK is autoincremented by SQLite; this string is the external_id (the shape the
// dispatcher and commit trailers reference).
func newBoardExternalID() (string, error) {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return "T-" + string(buf), nil
}

// boardTaskDTO is the full board task shape (POST/PATCH response, board list
// item, and the task_updated WS payload). camelCase JSON, mirrored in
// web/src/api/types.ts.
type boardTaskDTO struct {
	ID            int64    `json:"id"`
	ExternalID    string   `json:"externalId"`
	ProjectID     int64    `json:"projectId"`
	ProjectSlug   *string  `json:"projectSlug"`
	Title         string   `json:"title"`
	Prompt        string   `json:"prompt"`
	Priority      string   `json:"priority"` // urgent|high|normal|low
	Status        string   `json:"status"`   // existing lifecycle column (queued|running|…)
	BoardColumn   string   `json:"boardColumn"`
	Paused        bool     `json:"paused"`
	UserPaused    bool     `json:"userPaused"`
	Dependencies  []string `json:"dependencies"`
	Model         *string  `json:"model"`
	Playbook      *string  `json:"playbook"` // selected recipe name (null = default 'standard')
	FileScope     []string `json:"fileScope"`
	Branch        *string  `json:"branch"`
	WorktreePath  *string  `json:"worktreePath"`
	DispatchError *string  `json:"dispatchError"`
	RetryCount    int      `json:"retryCount"`
	VerifyVerdict *string  `json:"verifyVerdict"`
	VerifyDetail  *string  `json:"verifyDetail"`
	// Derived, never stored: whether a task that claims to be running actually is,
	// and on what evidence. Computed per request by internal/staleness — a cached
	// verdict would be stale exactly when it matters, since the state it reads
	// changes outside the daemon. Empty when the task does not claim to be running.
	Staleness       string  `json:"staleness,omitempty"`
	StalenessReason string  `json:"stalenessReason,omitempty"`
	ColumnMovedAt   *string `json:"columnMovedAt"`
	CreatedAt       string  `json:"createdAt"`
}

const boardTaskSelect = `
	SELECT t.id, t.external_id, t.project_id, p.slug, t.title, t.prompt,
	       t.priority, t.status, t.board_column, t.paused, t.user_paused,
	       t.dependencies, t.model, t.playbook, t.file_scope, t.branch, t.worktree_path,
	       t.dispatch_error, t.retry_count, t.verify_verdict, t.verify_detail,
	       t.column_moved_at, t.created_at
	FROM tasks t JOIN projects p ON p.id = t.project_id`

func scanBoardTask(scan func(...any) error, d *boardTaskDTO) error {
	var (
		priority           int
		paused, userPaused int64
		deps, scope        string
		externalID         sql.NullString
	)
	if err := scan(&d.ID, &externalID, &d.ProjectID, &d.ProjectSlug, &d.Title, &d.Prompt,
		&priority, &d.Status, &d.BoardColumn, &paused, &userPaused,
		&deps, &d.Model, &d.Playbook, &scope, &d.Branch, &d.WorktreePath,
		&d.DispatchError, &d.RetryCount, &d.VerifyVerdict, &d.VerifyDetail,
		&d.ColumnMovedAt, &d.CreatedAt); err != nil {
		return err
	}
	d.ExternalID = externalID.String
	d.Priority = priorityFromInt(priority)
	d.Paused = paused != 0
	d.UserPaused = userPaused != 0
	var err error
	if d.Dependencies, err = unmarshalStringList(deps); err != nil {
		return err
	}
	if d.FileScope, err = unmarshalStringList(scope); err != nil {
		return err
	}
	return nil
}

// boardTaskByID hydrates one board task (used by POST/PATCH responses and the
// task_updated WS payload). Returns (nil, nil) when the row is gone.
func (h *Handler) boardTaskByID(id int64) (*boardTaskDTO, error) {
	var d boardTaskDTO
	err := scanBoardTask(h.DB.QueryRow(boardTaskSelect+` WHERE t.id = ?`, id).Scan, &d)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// publishTaskUpdated notifies WS subscribers a board task row changed. No-op
// when the bus is not attached (serve --no-ingest).
func publishTaskUpdated(id int64) {
	if wsBus != nil {
		wsBus.Publish(ingest.Notification{Type: ingest.NoteTaskUpdated, TaskID: id})
	}
}

// publishTaskDeleted notifies WS subscribers a board task row is gone. The
// project id rides along because the row can no longer be read back.
func publishTaskDeleted(id, projectID int64) {
	if wsBus != nil {
		wsBus.Publish(ingest.Notification{
			Type: ingest.NoteTaskDeleted, TaskID: id, ProjectID: projectID,
		})
	}
}

// GET /api/board/tasks?projectId=&boardColumn= — board rows (source='queue'),
// newest first. Both filters optional. Distinct from GET /api/tasks (the
// workspace 14-day summary).
func (h *Handler) listBoardTasks(w http.ResponseWriter, r *http.Request) {
	q := boardTaskSelect + ` WHERE t.source = 'queue'`
	var args []any
	if pid := r.URL.Query().Get("projectId"); pid != "" {
		q += ` AND t.project_id = ?`
		args = append(args, pid)
	}
	if col := r.URL.Query().Get("boardColumn"); col != "" {
		if !validColumn(col) {
			http.Error(w, `{"error":"unknown boardColumn"}`, http.StatusBadRequest)
			return
		}
		q += ` AND t.board_column = ?`
		args = append(args, col)
	}
	q += ` ORDER BY t.id DESC`
	rows, err := h.DB.Query(q, args...)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer rows.Close()
	out := []boardTaskDTO{}
	for rows.Next() {
		var d boardTaskDTO
		if err := scanBoardTask(rows.Scan, &d); err != nil {
			writeErr(w, err)
			return
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, out, err)
		return
	}
	// Staleness is joined in Go rather than folded into boardTaskSelect: that select
	// is shared with the single-task handler, and widening it would make every
	// caller pay for four extra aggregates. A failure here degrades the field to
	// empty and never fails the list — a missing derived hint must not cost the
	// board its data.
	annotateStaleness(h.DB, out)
	writeJSON(w, out, nil)
}

// annotateStaleness fills the derived staleness fields in place. Best-effort by
// design: the verdict is a hint for a human, and the board must render without it.
//
// NOTE for whoever extends this: the board lists only source='queue' rows
// (see listBoardTasks), so workspace tasks — which is where every stuck task in the
// live database actually sits — never reach this function. `swarmery stale` is the
// surface that covers them.
func annotateStaleness(db *sql.DB, out []boardTaskDTO) {
	if len(out) == 0 {
		return
	}
	verdicts, err := staleness.Load(db, 0)
	if err != nil {
		log.Printf("warning: api: staleness annotate: %v", err)
		return
	}
	byID := make(map[int64]staleness.Verdict, len(verdicts))
	for _, v := range verdicts {
		byID[v.TaskID] = v.Verdict
	}
	for i := range out {
		v, ok := byID[out[i].ID]
		if !ok || v.Kind == staleness.KindLive {
			continue // a live task carries no hint; absence is the signal
		}
		out[i].Staleness = string(v.Kind)
		out[i].StalenessReason = v.Reason
	}
}

// POST /api/board/tasks {projectId, title, prompt, priority?, model?,
// fileScope?, dependencies?, boardColumn?} → 201 DTO. requireLocalOrigin.
func (h *Handler) createBoardTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProjectID    int64    `json:"projectId"`
		Title        string   `json:"title"`
		Prompt       string   `json:"prompt"`
		Priority     string   `json:"priority"`
		Model        *string  `json:"model"`
		Playbook     *string  `json:"playbook"`
		FileScope    []string `json:"fileScope"`
		Dependencies []string `json:"dependencies"`
		BoardColumn  string   `json:"boardColumn"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(body.Title)
	prompt := strings.TrimSpace(body.Prompt)
	if title == "" || prompt == "" {
		http.Error(w, `{"error":"title and prompt are required"}`, http.StatusBadRequest)
		return
	}
	column := body.BoardColumn
	if column == "" {
		column = "triage"
	}
	if !validColumn(column) {
		http.Error(w, `{"error":"unknown boardColumn"}`, http.StatusBadRequest)
		return
	}
	priority, err := normalizePriority(body.Priority)
	if err != nil {
		badRequest(w, err)
		return
	}
	scopeJSON, err := marshalStringList(body.FileScope)
	if err != nil {
		badRequest(w, err)
		return
	}
	depsJSON, err := marshalStringList(body.Dependencies)
	if err != nil {
		badRequest(w, err)
		return
	}
	// Project must exist.
	var one int
	err = h.DB.QueryRow(`SELECT 1 FROM projects WHERE id = ?`, body.ProjectID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, `{"error":"unknown project id"}`, http.StatusBadRequest)
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	// Playbook (fusion phase 13): optional recipe name; when set it must resolve
	// in the registry for this project (built-in or project-local). NULL ⇒ the
	// default 'standard' recipe. An unresolvable name is a client bug (400).
	playbook, ok := h.resolvePlaybookName(w, body.ProjectID, body.Playbook)
	if !ok {
		return
	}
	extID, err := newBoardExternalID()
	if err != nil {
		writeErr(w, err)
		return
	}
	now := time.Now().UTC().Format(boardTSFormat)
	var movedAt any
	if column != "triage" {
		movedAt = now // an explicit non-default landing column counts as a move
	}
	res, err := h.DB.Exec(`
		INSERT INTO tasks (project_id, title, prompt, priority, status, created_at,
		                   source, external_id, board_column, model, playbook, file_scope,
		                   dependencies, column_moved_at)
		VALUES (?, ?, ?, ?, 'queued', ?, 'queue', ?, ?, ?, ?, ?, ?, ?)`,
		body.ProjectID, title, prompt, priority, now,
		extID, column, body.Model, playbook, scopeJSON, depsJSON, movedAt)
	if err != nil {
		writeErr(w, err)
		return
	}
	id, _ := res.LastInsertId()
	d, err := h.boardTaskByID(id)
	if err != nil {
		writeErr(w, err)
		return
	}
	publishTaskUpdated(id)
	// A task created directly into todo is immediately dispatchable — trigger the
	// event fast path (Poke is a no-op when created into triage or when the
	// dispatcher is not attached).
	pokeDispatch()
	writeJSONStatus(w, http.StatusCreated, d)
}

// PATCH /api/board/tasks/{id} — accepts the user-editable TaskPatch fields
// (boardColumn, title, prompt, priority, model, fileScope, dependencies,
// paused, userPaused). Dispatcher-owned fields (branch, verdict, …) are NOT
// settable here. Moving column emits task_updated. requireLocalOrigin.
func (h *Handler) patchBoardTask(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, `{"error":"invalid task id"}`, http.StatusBadRequest)
		return
	}
	var body struct {
		BoardColumn  *string   `json:"boardColumn"`
		Title        *string   `json:"title"`
		Prompt       *string   `json:"prompt"`
		Priority     *string   `json:"priority"`
		Model        *string   `json:"model"`
		Playbook     *string   `json:"playbook"`
		FileScope    *[]string `json:"fileScope"`
		Dependencies *[]string `json:"dependencies"`
		Paused       *bool     `json:"paused"`
		UserPaused   *bool     `json:"userPaused"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}

	// Load current row (board-scoped) for transition validation + 404.
	cur, err := h.boardTaskByID(id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if cur == nil {
		http.Error(w, `{"error":"task not found"}`, http.StatusNotFound)
		return
	}

	var sets []string
	var args []any
	columnChanged := false

	if body.BoardColumn != nil {
		to := *body.BoardColumn
		if !validColumn(to) {
			http.Error(w, `{"error":"unknown boardColumn"}`, http.StatusBadRequest)
			return
		}
		if err := legalTransition(cur.BoardColumn, to); err != nil {
			badRequest(w, err)
			return
		}
		if to != cur.BoardColumn {
			sets = append(sets, "board_column = ?")
			args = append(args, to)
			columnChanged = true
		}
	}
	if body.Title != nil {
		t := strings.TrimSpace(*body.Title)
		if t == "" {
			http.Error(w, `{"error":"title cannot be empty"}`, http.StatusBadRequest)
			return
		}
		sets = append(sets, "title = ?")
		args = append(args, t)
	}
	if body.Prompt != nil {
		p := strings.TrimSpace(*body.Prompt)
		if p == "" {
			http.Error(w, `{"error":"prompt cannot be empty"}`, http.StatusBadRequest)
			return
		}
		sets = append(sets, "prompt = ?")
		args = append(args, p)
	}
	if body.Priority != nil {
		v, err := normalizePriority(*body.Priority)
		if err != nil {
			badRequest(w, err)
			return
		}
		sets = append(sets, "priority = ?")
		args = append(args, v)
	}
	if body.Model != nil {
		sets = append(sets, "model = ?")
		args = append(args, nullableStr(*body.Model))
	}
	if body.Playbook != nil {
		// A blank string clears the selection (→ default 'standard'); a non-blank
		// name must resolve in the registry for this project.
		playbook, ok := h.resolvePlaybookName(w, cur.ProjectID, body.Playbook)
		if !ok {
			return
		}
		sets = append(sets, "playbook = ?")
		args = append(args, playbook)
	}
	if body.FileScope != nil {
		scopeJSON, err := marshalStringList(*body.FileScope)
		if err != nil {
			badRequest(w, err)
			return
		}
		sets = append(sets, "file_scope = ?")
		args = append(args, scopeJSON)
	}
	if body.Dependencies != nil {
		depsJSON, err := marshalStringList(*body.Dependencies)
		if err != nil {
			badRequest(w, err)
			return
		}
		sets = append(sets, "dependencies = ?")
		args = append(args, depsJSON)
	}
	if body.Paused != nil {
		sets = append(sets, "paused = ?")
		args = append(args, boolToInt(*body.Paused))
	}
	if body.UserPaused != nil {
		sets = append(sets, "user_paused = ?")
		args = append(args, boolToInt(*body.UserPaused))
	}

	if columnChanged {
		sets = append(sets, "column_moved_at = ?")
		args = append(args, time.Now().UTC().Format(boardTSFormat))
	}

	if len(sets) == 0 {
		// Nothing to change — return current state (idempotent no-op).
		writeJSON(w, cur, nil)
		return
	}

	args = append(args, id)
	if _, err := h.DB.Exec(
		`UPDATE tasks SET `+strings.Join(sets, ", ")+` WHERE id = ? AND source = 'queue'`, args...); err != nil {
		writeErr(w, err)
		return
	}
	d, err := h.boardTaskByID(id)
	if err != nil {
		writeErr(w, err)
		return
	}
	publishTaskUpdated(id)
	// Dispatcher hooks (fusion phase 3): a terminal move reclaims the worktree;
	// then poke so the scheduler reacts to the fast path — a move to todo, an
	// unpause, or a dependency reaching done/archived (FN-3895: unblocking
	// dependents must be an event, not the 15s sweep). Both are no-ops when the
	// dispatcher is not attached.
	if dispatchSvc != nil && columnChanged && (d.BoardColumn == "done" || d.BoardColumn == "archived") {
		dispatchSvc.RemoveWorktreeFor(id)
	}
	// A move to done resolves the plan phase this task was minted from (if any):
	// check its doc's remaining acceptance boxes so plan progress follows the
	// board verdict. Archived is excluded — archiving can mean "abandoned".
	if columnChanged && d.BoardColumn == "done" {
		if n, err := wsingest.TickPhaseChecklist(h.DB, id); err != nil {
			log.Printf("warn: api: tick phase checklist (task %d): %v", id, err)
		} else if n > 0 {
			log.Printf("api: task %d done — ticked %d phase checkbox(es)", id, n)
		}
	}
	pokeDispatch()
	writeJSON(w, d, nil)
}

// DELETE /api/board/tasks/{id} → 204. Permanently removes a board row the user
// no longer wants — the escape hatch Archive is not: an archived task still
// shows up in the column and in every dependency picker. Board-scoped: only
// source='queue' rows are deletable (a workspace-ingested task is owned by the
// disk, deleting the row would just resurrect it on the next scan) — anything
// else is a 404.
//
// A RUNNING task is refused with 409 rather than force-killed: the row is the
// only handle the daemon has on a live headless agent (its process group, its
// worktree, its session link), so dropping it would orphan all three. Stop it
// first by moving it to done/archived, which reclaims the worktree.
func (h *Handler) deleteBoardTask(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, `{"error":"invalid task id"}`, http.StatusBadRequest)
		return
	}
	var source, status string
	var projectID int64
	var worktreePath sql.NullString
	err = h.DB.QueryRow(
		`SELECT source, status, project_id, worktree_path FROM tasks WHERE id = ?`, id).
		Scan(&source, &status, &projectID, &worktreePath)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, `{"error":"task not found"}`, http.StatusNotFound)
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	if source != "queue" {
		http.Error(w, `{"error":"task not found"}`, http.StatusNotFound)
		return
	}
	if status == "running" {
		http.Error(w,
			`{"error":"task is running — move it to done or archived first (that stops it and reclaims the worktree)"}`,
			http.StatusConflict)
		return
	}
	// Reclaim a worktree the row still holds. A task can carry one without being
	// running (a failed admission, a crash before the terminal move) and the row
	// is about to stop being reachable, so this is the last chance to free it.
	// Best-effort: a failed removal is logged inside the dispatcher and must not
	// block the delete, otherwise a stale worktree makes the card undeletable.
	if dispatchSvc != nil && strings.TrimSpace(worktreePath.String) != "" {
		dispatchSvc.RemoveWorktreeFor(id)
	}

	tx, err := h.DB.Begin()
	if err != nil {
		writeErr(w, err)
		return
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit
	// task_sessions predates ON DELETE CASCADE (0006_workspaces.sql) and the
	// store opens SQLite with foreign_keys(1), so its link rows must go first or
	// the row delete fails on a task that ever ran. Every other child table
	// (retro, verification, epic_phases, plan_runs) declares CASCADE.
	if _, err := tx.Exec(`DELETE FROM task_sessions WHERE task_id = ?`, id); err != nil {
		writeErr(w, err)
		return
	}
	// epic_phases.activated_board_task_id is a soft reference (no FK), so it
	// would dangle. Clearing it releases the phase to be activated again.
	if _, err := tx.Exec(
		`UPDATE epic_phases SET activated_board_task_id = NULL WHERE activated_board_task_id = ?`, id); err != nil {
		writeErr(w, err)
		return
	}
	res, err := tx.Exec(`DELETE FROM tasks WHERE id = ? AND source = 'queue'`, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		http.Error(w, `{"error":"task not found"}`, http.StatusNotFound)
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, err)
		return
	}
	publishTaskDeleted(id, projectID)
	w.WriteHeader(http.StatusNoContent)
}

// badRequest writes a 400 with the error's message as JSON.
func badRequest(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
