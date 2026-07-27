// Epic rollup + doc-editor API (fusion phase 10 — DESIGN.md §2 items 9–10):
//
//	GET   /api/epics?projectId=                                  → epics + phases + rollup
//	GET   /api/epics/{taskId}/docs?path=                         → read a plan doc
//	PUT   /api/epics/{taskId}/docs?path=                         → write a plan doc (backup)
//	PATCH /api/epics/{taskId}/docs?path=  {line, done}           → flip one checkbox by line index
//
// An "epic" is a workspace task (source='workspace') whose plan/ dir the
// wsingest scanner parsed into epic_phases. Reads are self-wiring over h.DB
// (like presets.go / project_meta.go). The doc endpoints turn the workspace
// folder into invisible infrastructure — plans are read and edited from the
// platform; the confinement fence keeps every path strictly under that task's
// plan/ dir (EvalSymlinks + prefix check), and writes take a timestamped backup
// first (mirroring the System write surface). All writes carry the same
// requireLocalOrigin D4 hardening as every other mutating endpoint.
// NOTE: the POST /activate route was removed in interactive-planning-v2 phase 4
// (Board is exclusively for tasks created on the board; plan phases run directly
// via phase 5's run mechanism). Historical activated_board_task_id links are
// preserved in the DB and still served read-only in the epic list DTO.

package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ── DTOs ────────────────────────────────────────────────────────────────────

// epicPhaseDTO is one phase row (camelCase, mirrored in web/src/api/types.ts).
type epicPhaseDTO struct {
	ID              int64   `json:"id"`
	Seq             int     `json:"seq"`
	Name            string  `json:"name"`
	DocPath         string  `json:"docPath"`
	DocRelPath      string  `json:"docRelPath"` // path relative to plan/ — the ?path= value
	DependsOn       []int   `json:"dependsOn"`
	CheckboxesDone  int     `json:"checkboxesDone"`
	CheckboxesTotal int     `json:"checkboxesTotal"`
	ActivatedAt     *string `json:"activatedAt"`
	// The external_id of the board task an activation minted (null until activated).
	BoardTaskExternalID *string `json:"boardTaskExternalId"`
	BoardTaskID         *int64  `json:"boardTaskId"`
	BoardColumn         *string `json:"boardColumn"`
}

// epicRollupDTO is a checkbox rollup across all of an epic's phases.
type epicRollupDTO struct {
	Done  int     `json:"done"`
	Total int     `json:"total"`
	Pct   float64 `json:"pct"` // 0..100, 0 when total==0 (no divide-by-zero)
}

// epicDTO is one epic (workspace task) with its phases and rollup.
type epicDTO struct {
	TaskID      int64          `json:"taskId"`
	ExternalID  string         `json:"externalId"`
	ProjectID   int64          `json:"projectId"`
	ProjectSlug string         `json:"projectSlug"`
	Title       string         `json:"title"`
	Status      string         `json:"status"` // active | paused | done | archived (planStatus)
	StartedAt   *string        `json:"startedAt"`
	PlanDir     string         `json:"planDir"`
	Phases      []epicPhaseDTO `json:"phases"`
	Rollup      epicRollupDTO  `json:"rollup"`
}

// wsPlanPayload is the plan_updated WS payload (frozen once shipped) — a thin
// cache-invalidation hint, not data: clients refetch GET /api/epics.
type wsPlanPayload struct {
	TaskID    int64 `json:"taskId"`
	ProjectID int64 `json:"projectId"`
}

// planUpdatedPayload resolves the plan_updated payload for one workspace task
// (the same task→project resolution listEpics performs). nil when the row is
// gone — the WS layer then skips the frame.
func (h *Handler) planUpdatedPayload(taskID int64) (*wsPlanPayload, error) {
	var p wsPlanPayload
	err := h.DB.QueryRow(
		`SELECT id, project_id FROM tasks WHERE id = ? AND source = 'workspace'`,
		taskID).Scan(&p.TaskID, &p.ProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// planStatus derives the plan lifecycle state. Precedence: zone > README >
// rollup — an archived task is "archived" whatever its README says, a paused
// README beats a complete rollup, and a full rollup reads "done" even before
// the plan is archived. epicDTO.Status is always one of these four values.
func planStatus(archived bool, taskStatus string, done, total int) string {
	switch {
	case archived:
		return "archived"
	case taskStatus == "paused":
		return "paused"
	case total > 0 && done == total:
		return "done"
	default:
		return "active"
	}
}

// ── GET /api/epics ──────────────────────────────────────────────────────────

// listEpics returns every workspace task that has ≥1 epic_phase, optionally
// scoped by projectId, newest first, each with its phases and checkbox rollup.
func (h *Handler) listEpics(w http.ResponseWriter, r *http.Request) {
	q := `
		SELECT t.id, COALESCE(t.external_id,''), t.project_id, p.slug, t.title,
		       t.status, t.archived_at IS NOT NULL, t.started_at,
		       (SELECT path FROM task_artifacts WHERE task_id = t.id AND kind = 'plan')
		FROM tasks t
		JOIN projects p ON p.id = t.project_id
		WHERE t.source = 'workspace'
		  AND EXISTS (SELECT 1 FROM epic_phases e WHERE e.workspace_task_id = t.id)`
	var args []any
	if pid := r.URL.Query().Get("projectId"); pid != "" {
		q += ` AND t.project_id = ?`
		args = append(args, pid)
	}
	q += ` ORDER BY t.started_at DESC, t.id DESC`

	rows, err := h.DB.Query(q, args...)
	if err != nil {
		writeErr(w, err)
		return
	}
	// Materialize the epic rows FIRST, then close the cursor — the SQLite pool
	// is single-connection, so hydrating phases (a nested Query) while this
	// cursor is open would deadlock. Second pass runs the per-epic queries.
	out := []epicDTO{}
	archived := []bool{} // parallel to out — feeds the planStatus derivation
	for rows.Next() {
		var e epicDTO
		var arch bool
		var planDir sql.NullString
		if err := rows.Scan(&e.TaskID, &e.ExternalID, &e.ProjectID, &e.ProjectSlug,
			&e.Title, &e.Status, &arch, &e.StartedAt, &planDir); err != nil {
			rows.Close()
			writeErr(w, err)
			return
		}
		e.PlanDir = planDir.String
		out = append(out, e)
		archived = append(archived, arch)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeErr(w, err)
		return
	}

	for i := range out {
		phases, rollup, err := h.epicPhases(out[i].TaskID, out[i].PlanDir)
		if err != nil {
			writeErr(w, err)
			return
		}
		out[i].Phases = phases
		out[i].Rollup = rollup
		// Normalize the raw tasks.status (running|paused|done) into the plan
		// lifecycle contract: active | paused | done | archived.
		out[i].Status = planStatus(archived[i], out[i].Status, rollup.Done, rollup.Total)
	}
	writeJSON(w, out, nil)
}

// epicPhases loads one epic's phases (joined to the board task an activation
// minted) plus the checkbox rollup. planDir is used to compute each phase's
// path relative to plan/ (the ?path= the doc endpoints accept).
func (h *Handler) epicPhases(taskID int64, planDir string) ([]epicPhaseDTO, epicRollupDTO, error) {
	rows, err := h.DB.Query(`
		SELECT e.id, e.seq, e.name, e.doc_path, e.depends_on,
		       e.checkboxes_total, e.checkboxes_done, e.activated_at,
		       e.activated_board_task_id, bt.external_id, bt.board_column
		FROM epic_phases e
		LEFT JOIN tasks bt ON bt.id = e.activated_board_task_id
		WHERE e.workspace_task_id = ?
		ORDER BY e.seq, e.id`, taskID)
	if err != nil {
		return nil, epicRollupDTO{}, err
	}
	defer rows.Close()

	phases := []epicPhaseDTO{}
	var rollup epicRollupDTO
	for rows.Next() {
		var (
			p           epicPhaseDTO
			depsJSON    string
			boardTaskID sql.NullInt64
			boardExtID  sql.NullString
			boardCol    sql.NullString
		)
		if err := rows.Scan(&p.ID, &p.Seq, &p.Name, &p.DocPath, &depsJSON,
			&p.CheckboxesTotal, &p.CheckboxesDone, &p.ActivatedAt,
			&boardTaskID, &boardExtID, &boardCol); err != nil {
			return nil, epicRollupDTO{}, err
		}
		p.DependsOn = decodeIntList(depsJSON)
		p.DocRelPath = relToPlan(planDir, p.DocPath)
		if boardTaskID.Valid {
			p.BoardTaskID = &boardTaskID.Int64
		}
		if boardExtID.Valid {
			p.BoardTaskExternalID = &boardExtID.String
		}
		if boardCol.Valid {
			p.BoardColumn = &boardCol.String
		}
		rollup.Done += p.CheckboxesDone
		rollup.Total += p.CheckboxesTotal
		phases = append(phases, p)
	}
	if rollup.Total > 0 {
		rollup.Pct = float64(rollup.Done) / float64(rollup.Total) * 100
	}
	return phases, rollup, rows.Err()
}

// decodeIntList parses a JSON array of ints; [] on empty/garbage.
func decodeIntList(s string) []int {
	out := []int{}
	if strings.TrimSpace(s) == "" {
		return out
	}
	if err := json.Unmarshal([]byte(s), &out); err != nil || out == nil {
		return []int{}
	}
	return out
}

// relToPlan returns doc's path relative to planDir, or the basename when it is
// not under planDir (best-effort — the doc endpoints re-confine anyway).
func relToPlan(planDir, doc string) string {
	if planDir == "" {
		return filepath.Base(doc)
	}
	if rel, err := filepath.Rel(planDir, doc); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return filepath.Base(doc)
}

// ── plan-doc editor: GET/PUT/PATCH /api/epics/{taskId}/docs?path= ───────────

// planDocMaxBytes caps a plan doc write (generous — plans are prose).
const planDocMaxBytes = 1 << 20 // 1 MiB

// resolvePlanDoc confines ?path= to the task's plan/ dir and returns the
// absolute file path. The plan dir comes from task_artifacts (kind='plan').
// Confinement: both the plan dir and the target are resolved through
// EvalSymlinks (the dir must exist; the file may not yet, so its PARENT is
// resolved) and the target must be strictly under the plan dir. A traversal or
// symlink escape yields ErrPathEscape.
var errPathEscape = errors.New("path escapes the plan directory")

func (h *Handler) resolvePlanDoc(taskID int64, rel string) (string, error) {
	var planDir string
	err := h.DB.QueryRow(
		`SELECT path FROM task_artifacts WHERE task_id = ? AND kind = 'plan'`, taskID).Scan(&planDir)
	if errors.Is(err, sql.ErrNoRows) {
		return "", sql.ErrNoRows
	}
	if err != nil {
		return "", err
	}

	rootAbs, err := filepath.EvalSymlinks(planDir)
	if err != nil {
		return "", err
	}
	// Join rel onto the plan dir; a leading "/" or ".." must not escape. Clean
	// first, then verify the resolved parent stays under the root.
	rel = strings.TrimPrefix(filepath.Clean("/"+rel), "/") // strip any leading slash, normalize ..
	target := filepath.Join(rootAbs, rel)

	// Resolve the target's PARENT (the file itself may not exist on a fresh
	// write) and re-check containment against the real root.
	parentReal, err := filepath.EvalSymlinks(filepath.Dir(target))
	if err != nil {
		return "", errPathEscape // a non-existent/again-symlinked parent → refuse
	}
	final := filepath.Join(parentReal, filepath.Base(target))
	if final != rootAbs && !strings.HasPrefix(final, rootAbs+string(os.PathSeparator)) {
		return "", errPathEscape
	}
	if !strings.HasSuffix(strings.ToLower(final), ".md") {
		return "", errPathEscape // only markdown plan docs are editable
	}
	// If the file itself EXISTS, resolve its full symlink chain and re-check
	// containment — a symlink INSIDE plan/ pointing OUT (its parent resolves to
	// the plan dir, so the check above passes) must not leak the target.
	if realFinal, err := filepath.EvalSymlinks(final); err == nil {
		if realFinal != rootAbs && !strings.HasPrefix(realFinal, rootAbs+string(os.PathSeparator)) {
			return "", errPathEscape
		}
	}
	return final, nil
}

// planDocResponse is the GET/PUT body: the content + its relative path.
type planDocResponse struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	// Backup is the on-disk backup path a PUT/PATCH wrote (absent on GET).
	Backup string `json:"backup,omitempty"`
}

// getPlanDoc — GET /api/epics/{taskId}/docs?path=. Read a plan doc.
func (h *Handler) getPlanDoc(w http.ResponseWriter, r *http.Request) {
	taskID, ok := parseTaskIDParam(w, r)
	if !ok {
		return
	}
	rel := r.URL.Query().Get("path")
	if strings.TrimSpace(rel) == "" {
		writeClientErr(w, http.StatusBadRequest, "path query param required")
		return
	}
	path, err := h.resolvePlanDoc(taskID, rel)
	if err != nil {
		writePlanDocErr(w, err)
		return
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			writeClientErr(w, http.StatusNotFound, "doc not found")
			return
		}
		writeErr(w, err)
		return
	}
	writeJSON(w, planDocResponse{Path: rel, Content: string(body)}, nil)
}

// putPlanDoc — PUT /api/epics/{taskId}/docs?path= {content}. Overwrite a plan
// doc after taking a timestamped backup next to it. requireLocalOrigin.
func (h *Handler) putPlanDoc(w http.ResponseWriter, r *http.Request) {
	taskID, ok := parseTaskIDParam(w, r)
	if !ok {
		return
	}
	rel := r.URL.Query().Get("path")
	if strings.TrimSpace(rel) == "" {
		writeClientErr(w, http.StatusBadRequest, "path query param required")
		return
	}
	var reqBody struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, planDocMaxBytes+4096)).Decode(&reqBody); err != nil {
		writeClientErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(reqBody.Content) > planDocMaxBytes {
		writeClientErr(w, http.StatusRequestEntityTooLarge, "doc too large")
		return
	}
	path, err := h.resolvePlanDoc(taskID, rel)
	if err != nil {
		writePlanDocErr(w, err)
		return
	}
	backup, err := writePlanDocFile(path, reqBody.Content)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, planDocResponse{Path: rel, Content: reqBody.Content, Backup: backup}, nil)
}

// checkboxLineRe matches an acceptance checkbox line and captures its state.
var checkboxLineRe = regexp.MustCompile(`^(\s*[-*]\s+\[)( |x|X)(\]\s.*)$`)

// patchPlanDoc — PATCH /api/epics/{taskId}/docs?path= {line, done}. Flip one
// checkbox by 0-based line index (the exact `- [ ]`↔`- [x]` line). Takes a
// backup first; the next wsingest rescan folds the new count into the rollup.
// requireLocalOrigin.
func (h *Handler) patchPlanDoc(w http.ResponseWriter, r *http.Request) {
	taskID, ok := parseTaskIDParam(w, r)
	if !ok {
		return
	}
	rel := r.URL.Query().Get("path")
	if strings.TrimSpace(rel) == "" {
		writeClientErr(w, http.StatusBadRequest, "path query param required")
		return
	}
	var reqBody struct {
		Line *int  `json:"line"`
		Done *bool `json:"done"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&reqBody); err != nil {
		writeClientErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if reqBody.Line == nil || reqBody.Done == nil {
		writeClientErr(w, http.StatusBadRequest, "line and done are required")
		return
	}
	path, err := h.resolvePlanDoc(taskID, rel)
	if err != nil {
		writePlanDocErr(w, err)
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			writeClientErr(w, http.StatusNotFound, "doc not found")
			return
		}
		writeErr(w, err)
		return
	}
	// Split preserving the trailing-newline shape: strings.Split keeps a final
	// "" for a trailing \n, which Join restores exactly.
	lines := strings.Split(string(raw), "\n")
	i := *reqBody.Line
	if i < 0 || i >= len(lines) {
		writeClientErr(w, http.StatusBadRequest, "line index out of range")
		return
	}
	m := checkboxLineRe.FindStringSubmatch(lines[i])
	if m == nil {
		writeClientErr(w, http.StatusBadRequest, "line is not a checkbox")
		return
	}
	mark := " "
	if *reqBody.Done {
		mark = "x"
	}
	lines[i] = m[1] + mark + m[3]

	backup, err := writePlanDocFile(path, strings.Join(lines, "\n"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, planDocResponse{Path: rel, Content: strings.Join(lines, "\n"), Backup: backup}, nil)
}

// writePlanDocFile backs up the current file (when it exists) next to it under
// a `.backups/<ts>/` dir, then writes content. Returns the backup path ("" when
// the file did not exist yet). The backup dir is inside plan/, so it stays
// under the same confinement root and travels with the workspace git repo.
func writePlanDocFile(path, content string) (string, error) {
	backup := ""
	if cur, err := os.ReadFile(path); err == nil {
		ts := time.Now().UTC().Format("2006-01-02T15-04-05Z")
		bdir := filepath.Join(filepath.Dir(path), ".backups", ts)
		if err := os.MkdirAll(bdir, 0o755); err != nil {
			return "", err
		}
		backup = filepath.Join(bdir, filepath.Base(path))
		if err := os.WriteFile(backup, cur, 0o644); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return backup, nil
}

// parseTaskIDParam parses {taskId}; writes a 400 and returns ok=false on failure.
func parseTaskIDParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("taskId"), 10, 64)
	if err != nil {
		writeClientErr(w, http.StatusBadRequest, "invalid task id")
		return 0, false
	}
	return id, true
}

// writePlanDocErr maps confinement/lookup errors to the right status.
func writePlanDocErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errPathEscape):
		writeClientErr(w, http.StatusBadRequest, "invalid path")
	case errors.Is(err, sql.ErrNoRows):
		writeClientErr(w, http.StatusNotFound, "no plan directory for this task")
	default:
		writeErr(w, err)
	}
}
