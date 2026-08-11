package planrev

import (
	"database/sql"
	"fmt"
	"strings"
)

var validActions = map[string]bool{
	ActionCreate: true,
	ActionUpdate: true,
	ActionDelete: true,
	ActionRename: true,
}

var decidedStatuses = map[string]bool{
	StatusApplied:    true,
	StatusRejected:   true,
	StatusSuperseded: true,
	StatusFailed:     true,
}

// validateDocPath enforces the plan-dir-relative contract: the apply step joins
// the path onto plan_dir, so an absolute path or a ".." segment would escape it.
func validateDocPath(p string) error {
	if p == "" {
		return fmt.Errorf("planrev: empty doc path")
	}
	if strings.HasPrefix(p, "/") {
		return fmt.Errorf("planrev: doc path %q is absolute; must be plan-dir-relative", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return fmt.Errorf("planrev: doc path %q contains a %q segment", p, "..")
		}
	}
	return nil
}

func validateFile(f File) error {
	if !validActions[f.Action] {
		return fmt.Errorf("planrev: unknown action %q for %q", f.Action, f.DocPath)
	}
	if err := validateDocPath(f.DocPath); err != nil {
		return err
	}
	if f.Action == ActionRename {
		if f.RenameFrom == "" {
			return fmt.Errorf("planrev: rename of %q has no rename_from", f.DocPath)
		}
		if err := validateDocPath(f.RenameFrom); err != nil {
			return err
		}
	} else if f.RenameFrom != "" {
		return fmt.Errorf("planrev: action %q for %q must not set rename_from", f.Action, f.DocPath)
	}
	if f.Action != ActionDelete && f.Proposed == "" {
		return fmt.Errorf("planrev: action %q for %q has no proposed content", f.Action, f.DocPath)
	}
	if f.Action == ActionDelete && f.Proposed != "" {
		return fmt.Errorf("planrev: delete of %q must not carry proposed content", f.DocPath)
	}
	return nil
}

// nullable maps "" → NULL so optional TEXT columns stay NULL, matching the
// schema's comments (base_hash NULL for create, proposed NULL for delete, …).
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Insert stages a revision and its files in one transaction and returns the
// new revision id. An empty file list is rejected with ErrEmptyRevision; every
// file is validated against the closed action set and the path contract.
func Insert(db *sql.DB, rev Revision, files []File) (int64, error) {
	if len(files) == 0 {
		return 0, ErrEmptyRevision
	}
	for _, f := range files {
		if err := validateFile(f); err != nil {
			return 0, err
		}
	}
	status := rev.Status
	if status == "" {
		status = StatusStaged
	}
	origin := rev.Origin
	if origin == "" {
		origin = OriginOperator
	}

	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("planrev: begin insert: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.Exec(`
		INSERT INTO plan_revisions
			(workspace_task_id, plan_dir, session_uuid, status, origin,
			 trigger_phase_id, reason, summary, error, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rev.WorkspaceTaskID, rev.PlanDir, nullable(rev.SessionUUID), status, origin,
		rev.TriggerPhaseID, rev.Reason, nullable(rev.Summary), nullable(rev.Error), rev.CreatedAt)
	if err != nil {
		return 0, fmt.Errorf("planrev: insert revision: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("planrev: revision id: %w", err)
	}
	for _, f := range files {
		if _, err := tx.Exec(`
			INSERT INTO plan_revision_files
				(revision_id, doc_path, action, rename_from, base_hash, proposed)
			VALUES (?, ?, ?, ?, ?, ?)`,
			id, f.DocPath, f.Action, nullable(f.RenameFrom), nullable(f.BaseHash), nullable(f.Proposed)); err != nil {
			return 0, fmt.Errorf("planrev: insert file %s: %w", f.DocPath, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("planrev: commit insert: %w", err)
	}
	return id, nil
}

const revisionCols = `id, workspace_task_id, plan_dir, session_uuid, status, origin,
	trigger_phase_id, reason, summary, error, created_at, decided_at, decided_by`

func scanRevision(row interface{ Scan(...any) error }) (*Revision, error) {
	var r Revision
	var sessionUUID, summary, errMsg, decidedAt, decidedBy sql.NullString
	var triggerPhase sql.NullInt64
	if err := row.Scan(&r.ID, &r.WorkspaceTaskID, &r.PlanDir, &sessionUUID, &r.Status, &r.Origin,
		&triggerPhase, &r.Reason, &summary, &errMsg, &r.CreatedAt, &decidedAt, &decidedBy); err != nil {
		return nil, err
	}
	r.SessionUUID = sessionUUID.String
	r.Summary = summary.String
	r.Error = errMsg.String
	r.DecidedAt = decidedAt.String
	r.DecidedBy = decidedBy.String
	if triggerPhase.Valid {
		v := triggerPhase.Int64
		r.TriggerPhaseID = &v
	}
	return &r, nil
}

func loadFiles(db *sql.DB, revisionID int64) ([]File, error) {
	rows, err := db.Query(`
		SELECT id, doc_path, action, rename_from, base_hash, proposed, applied_hash
		FROM plan_revision_files WHERE revision_id = ? ORDER BY doc_path`, revisionID)
	if err != nil {
		return nil, fmt.Errorf("planrev: query files of %d: %w", revisionID, err)
	}
	defer rows.Close()
	var files []File
	for rows.Next() {
		var f File
		var renameFrom, baseHash, proposed, appliedHash sql.NullString
		if err := rows.Scan(&f.ID, &f.DocPath, &f.Action, &renameFrom, &baseHash, &proposed, &appliedHash); err != nil {
			return nil, fmt.Errorf("planrev: scan file of %d: %w", revisionID, err)
		}
		f.RenameFrom = renameFrom.String
		f.BaseHash = baseHash.String
		f.Proposed = proposed.String
		f.AppliedHash = appliedHash.String
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("planrev: iterate files of %d: %w", revisionID, err)
	}
	return files, nil
}

// Get returns the revision with its files (ordered by doc_path), or nil, nil
// when it does not exist.
func Get(db *sql.DB, id int64) (*Revision, error) {
	r, err := scanRevision(db.QueryRow(
		`SELECT `+revisionCols+` FROM plan_revisions WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("planrev: get revision %d: %w", id, err)
	}
	if r.Files, err = loadFiles(db, r.ID); err != nil {
		return nil, err
	}
	return r, nil
}

// ListByTask returns a task's revisions newest first, files included (the
// history list renders a "N docs changed" line per revision).
func ListByTask(db *sql.DB, taskID int64) ([]Revision, error) {
	rows, err := db.Query(
		`SELECT `+revisionCols+` FROM plan_revisions WHERE workspace_task_id = ? ORDER BY id DESC`, taskID)
	if err != nil {
		return nil, fmt.Errorf("planrev: list revisions of task %d: %w", taskID, err)
	}
	defer rows.Close()
	var revs []Revision
	for rows.Next() {
		r, err := scanRevision(rows)
		if err != nil {
			return nil, fmt.Errorf("planrev: scan revision of task %d: %w", taskID, err)
		}
		revs = append(revs, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("planrev: iterate revisions of task %d: %w", taskID, err)
	}
	for i := range revs {
		if revs[i].Files, err = loadFiles(db, revs[i].ID); err != nil {
			return nil, err
		}
	}
	return revs, nil
}

// LatestStaged returns the task's open revision — the one a task may have
// staged at a time — or nil, nil when none is staged.
func LatestStaged(db *sql.DB, taskID int64) (*Revision, error) {
	r, err := scanRevision(db.QueryRow(
		`SELECT `+revisionCols+` FROM plan_revisions
		 WHERE workspace_task_id = ? AND status = ? ORDER BY id DESC LIMIT 1`,
		taskID, StatusStaged))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("planrev: latest staged of task %d: %w", taskID, err)
	}
	if r.Files, err = loadFiles(db, r.ID); err != nil {
		return nil, err
	}
	return r, nil
}

// Decide moves a staged revision to a terminal status. It is a CAS on
// status='staged': false, nil means a concurrent Apply/Reject already won.
func Decide(db *sql.DB, id int64, status, decidedBy, ts string) (bool, error) {
	if !decidedStatuses[status] {
		return false, fmt.Errorf("planrev: %q is not a decision status", status)
	}
	res, err := db.Exec(`
		UPDATE plan_revisions SET status = ?, decided_by = ?, decided_at = ?
		WHERE id = ? AND status = ?`,
		status, decidedBy, ts, id, StatusStaged)
	if err != nil {
		return false, fmt.Errorf("planrev: decide revision %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("planrev: decide revision %d: %w", id, err)
	}
	return n > 0, nil
}

// StampApplied records a file's apply audit trail (the bytes replaced and the
// hash actually written) inside the apply transaction.
func StampApplied(tx *sql.Tx, fileID int64, preImage, appliedHash string) error {
	res, err := tx.Exec(`
		UPDATE plan_revision_files SET pre_image = ?, applied_hash = ? WHERE id = ?`,
		nullable(preImage), nullable(appliedHash), fileID)
	if err != nil {
		return fmt.Errorf("planrev: stamp file %d: %w", fileID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("planrev: stamp file %d: %w", fileID, err)
	}
	if n == 0 {
		return fmt.Errorf("planrev: stamp file %d: no such file row", fileID)
	}
	return nil
}

// SetError moves a revision to 'failed' with the failure detail.
func SetError(db *sql.DB, id int64, msg, ts string) error {
	if _, err := db.Exec(`
		UPDATE plan_revisions SET status = ?, error = ?, decided_at = ?, decided_by = 'system'
		WHERE id = ?`,
		StatusFailed, msg, ts, id); err != nil {
		return fmt.Errorf("planrev: set error on revision %d: %w", id, err)
	}
	return nil
}
