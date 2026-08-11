package planrev

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/textdiff"
)

// ErrRevisionNotFound marks an Apply against a revision id with no row.
var ErrRevisionNotFound = errors.New("planrev: revision not found")

// ErrPhaseRunning aborts an Apply whose target docs include a phase that is
// currently executing — the run edits its own doc, so applying over it would
// race the executor.
var ErrPhaseRunning = errors.New("planrev: a target phase is running")

// Conflict is one file whose live bytes drifted from the base_hash captured at
// staging time. The shape mirrors internal/sysedit's 409 body.
type Conflict struct {
	DocPath  string `json:"docPath"`
	BaseHash string `json:"baseHash"`
	DiskHash string `json:"diskHash"`
	Diff     string `json:"diff"`
}

// ConflictError carries every stale file of an aborted Apply. No writes have
// been performed when it is returned.
type ConflictError struct {
	Conflicts []Conflict
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("planrev: %d file(s) changed on disk since staging", len(e.Conflicts))
}

// livePath is where a file's CURRENT bytes live before the apply: the rename
// source for a rename, the target itself for everything else.
func livePath(planDir string, f File) string {
	if f.Action == ActionRename {
		return filepath.Join(planDir, f.RenameFrom)
	}
	return filepath.Join(planDir, f.DocPath)
}

// readIfExists returns the file's bytes, or "" when it does not exist.
func readIfExists(path string) (string, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// atomicWrite lands content via planDir/.tmp-rev-<id>-<base> + os.Rename, so a
// crash mid-write never leaves a torn doc at the target path.
func atomicWrite(planDir, target string, revID int64, content string) error {
	tmp := filepath.Join(planDir, fmt.Sprintf(".tmp-rev-%d-%s", revID, filepath.Base(target)))
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Apply lands a staged revision on disk: run-guarded, conflict-guarded, atomic
// (any mid-sequence failure rolls every completed step back from its
// pre-image), history-preserving (renames move epic_phases.doc_path explicitly
// so daemon-owned run state survives the rescan even when the plan renumbers),
// and stamped (pre_image + applied_hash per file, status → applied). rescan is
// poked exactly once, after all writes, with the plan dir; nil ⇒ no-op.
// Returns the number of files applied.
func Apply(db *sql.DB, id int64, now func() time.Time, rescan func(planDir string)) (int, error) {
	// 1. Load + status gate.
	rev, err := Get(db, id)
	if err != nil {
		return 0, err
	}
	if rev == nil {
		return 0, ErrRevisionNotFound
	}
	if rev.Status != StatusStaged {
		return 0, ErrNotStaged
	}
	ts := now().UTC().Format(time.RFC3339)

	// 2. Run guard — no writes while any target phase executes.
	for _, f := range rev.Files {
		paths := []string{filepath.Join(rev.PlanDir, f.DocPath)}
		if f.Action == ActionRename {
			paths = append(paths, filepath.Join(rev.PlanDir, f.RenameFrom))
		}
		for _, p := range paths {
			var running int
			if err := db.QueryRow(`
				SELECT COUNT(*) FROM epic_phases
				 WHERE workspace_task_id = ? AND doc_path = ? AND run_state = 'running'`,
				rev.WorkspaceTaskID, p).Scan(&running); err != nil {
				return 0, fmt.Errorf("planrev: run guard %s: %w", f.DocPath, err)
			}
			if running > 0 {
				return 0, fmt.Errorf("%s: %w", f.DocPath, ErrPhaseRunning)
			}
		}
	}

	// 3. Conflict check — collect EVERY stale file, then abort with no writes.
	var conflicts []Conflict
	for _, f := range rev.Files {
		if f.BaseHash == "" {
			continue
		}
		live, err := readIfExists(livePath(rev.PlanDir, f))
		if err != nil {
			return 0, fmt.Errorf("planrev: conflict check %s: %w", f.DocPath, err)
		}
		diskHash := Sha256Hex([]byte(live))
		if diskHash != f.BaseHash {
			conflicts = append(conflicts, Conflict{
				DocPath:  f.DocPath,
				BaseHash: f.BaseHash,
				DiskHash: diskHash,
				// The staged base content is not stored, so the honest diff is
				// proposed vs disk — the header names say exactly that.
				Diff: textdiff.UnifiedDiff("proposed/"+f.DocPath, "disk/"+f.DocPath, f.Proposed, live),
			})
		}
	}
	if len(conflicts) > 0 {
		return 0, &ConflictError{Conflicts: conflicts}
	}

	// 4. Write phase. Deletes, then renames, then creates/updates — so a
	// rename (or create) into a freed name works. Every completed step records
	// an undo; on ANY error all of them run in reverse and the revision is
	// stamped 'failed'.
	var ordered []File
	for _, f := range rev.Files {
		if f.Action == ActionDelete {
			ordered = append(ordered, f)
		}
	}
	for _, f := range rev.Files {
		if f.Action == ActionRename {
			ordered = append(ordered, f)
		}
	}
	for _, f := range rev.Files {
		if f.Action == ActionCreate || f.Action == ActionUpdate {
			ordered = append(ordered, f)
		}
	}

	var undos []func() error
	rollback := func() {
		for i := len(undos) - 1; i >= 0; i-- {
			if uerr := undos[i](); uerr != nil {
				log.Printf("error: planrev: rollback revision %d: %v", id, uerr)
			}
		}
	}
	fail := func(step string, err error) (int, error) {
		rollback()
		msg := fmt.Sprintf("apply %s: %v", step, err)
		if serr := SetError(db, id, msg, ts); serr != nil {
			log.Printf("error: planrev: mark revision %d failed: %v", id, serr)
		}
		return 0, fmt.Errorf("planrev: revision %d: %s", id, msg)
	}

	preImages := make(map[int64]string, len(ordered))
	for _, f := range ordered {
		target := filepath.Join(rev.PlanDir, f.DocPath)
		switch f.Action {
		case ActionDelete:
			pre, err := os.ReadFile(target)
			if err != nil {
				return fail("delete "+f.DocPath, err)
			}
			preImages[f.ID] = string(pre)
			if err := os.Remove(target); err != nil {
				return fail("delete "+f.DocPath, err)
			}
			undos = append(undos, func() error { return os.WriteFile(target, pre, 0o644) })

		case ActionRename:
			from := filepath.Join(rev.PlanDir, f.RenameFrom)
			pre, err := os.ReadFile(from)
			if err != nil {
				return fail("rename "+f.RenameFrom, err)
			}
			preImages[f.ID] = string(pre)
			if _, err := os.Stat(target); err == nil {
				return fail("rename "+f.RenameFrom, fmt.Errorf("target %s already exists", f.DocPath))
			}
			if err := os.Rename(from, target); err != nil {
				return fail("rename "+f.RenameFrom, err)
			}
			undos = append(undos, func() error { return os.Rename(target, from) })
			if err := atomicWrite(rev.PlanDir, target, id, f.Proposed); err != nil {
				return fail("rename-write "+f.DocPath, err)
			}
			undos = append(undos, func() error { return os.WriteFile(target, pre, 0o644) })

		case ActionCreate:
			if _, err := os.Stat(target); err == nil {
				return fail("create "+f.DocPath, fmt.Errorf("target already exists"))
			}
			preImages[f.ID] = ""
			if err := atomicWrite(rev.PlanDir, target, id, f.Proposed); err != nil {
				return fail("create "+f.DocPath, err)
			}
			undos = append(undos, func() error { return os.Remove(target) })

		case ActionUpdate:
			pre, err := os.ReadFile(target)
			if err != nil {
				return fail("update "+f.DocPath, err)
			}
			preImages[f.ID] = string(pre)
			if err := atomicWrite(rev.PlanDir, target, id, f.Proposed); err != nil {
				return fail("update "+f.DocPath, err)
			}
			undos = append(undos, func() error { return os.WriteFile(target, pre, 0o644) })
		}
	}

	// 5+6. One transaction: move epic_phases.doc_path for every rename BEFORE
	// the rescan (carryAcrossRenames only matches 1:1 on seq — a renumbering
	// revision would drop run_state/run_branch/run_checkboxes_* without this),
	// stamp every file's audit trail, and CAS the decision.
	tx, err := db.Begin()
	if err != nil {
		return fail("begin stamp", err)
	}
	for _, f := range rev.Files {
		if f.Action != ActionRename {
			continue
		}
		if _, err := tx.Exec(`
			UPDATE epic_phases SET doc_path = ? WHERE workspace_task_id = ? AND doc_path = ?`,
			filepath.Join(rev.PlanDir, f.DocPath), rev.WorkspaceTaskID,
			filepath.Join(rev.PlanDir, f.RenameFrom)); err != nil {
			tx.Rollback()
			return fail("carry rename "+f.DocPath, err)
		}
	}
	for _, f := range rev.Files {
		applied := ""
		if f.Action != ActionDelete {
			applied = Sha256Hex([]byte(f.Proposed))
		}
		if err := StampApplied(tx, f.ID, preImages[f.ID], applied); err != nil {
			tx.Rollback()
			return fail("stamp "+f.DocPath, err)
		}
	}
	won, err := decide(tx, id, StatusApplied, "operator", ts)
	if err != nil {
		tx.Rollback()
		return fail("decide", err)
	}
	if err := tx.Commit(); err != nil {
		return fail("commit stamp", err)
	}
	if !won {
		// A concurrent reject won the CAS after the writes landed. The disk is
		// the truth — the files ARE applied — so this returns success and
		// leaves the loud trace instead of tearing the plan back down.
		log.Printf("error: planrev: revision %d was decided concurrently during apply — files are on disk, decision row kept", id)
	}

	// 7. Rescan once, after everything — the Plans page never renders a
	// half-applied plan.
	if rescan != nil {
		rescan(rev.PlanDir)
	}
	return len(rev.Files), nil
}
