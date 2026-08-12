package prune

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
)

// prNow anchors the pass; every fixture age is relative to it.
var prNow = time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)

func revDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "revisions.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// insertRevision seeds one revision (+1 file with content) and returns its id.
// decidedAt "" leaves the row undecided (staged shape).
func insertRevision(t *testing.T, db *sql.DB, status, createdAt, decidedAt string) int64 {
	t.Helper()
	var decided any
	var decidedBy any
	if decidedAt != "" {
		decided, decidedBy = decidedAt, "operator"
	}
	res, err := db.Exec(`
		INSERT INTO plan_revisions (workspace_task_id, plan_dir, status, reason, created_at, decided_at, decided_by)
		VALUES (1, '/ws/plan', ?, 'r', ?, ?, ?)`, status, createdAt, decided, decidedBy)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if _, err := db.Exec(`
		INSERT INTO plan_revision_files (revision_id, doc_path, action, base_hash, proposed, pre_image)
		VALUES (?, 'phase-1.md', 'update', 'h', 'proposed body', 'pre body')`, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func revisionRow(t *testing.T, db *sql.DB, id int64) (status string, decidedBy sql.NullString, proposed, preImage sql.NullString) {
	t.Helper()
	if err := db.QueryRow(`
		SELECT r.status, r.decided_by, f.proposed, f.pre_image
		  FROM plan_revisions r JOIN plan_revision_files f ON f.revision_id = r.id
		 WHERE r.id = ?`, id).Scan(&status, &decidedBy, &proposed, &preImage); err != nil {
		t.Fatal(err)
	}
	return
}

func ago(d time.Duration) string { return prNow.Add(-d).UTC().Format(time.RFC3339) }

func TestPruneRevisions_SupersedesStaleStaged(t *testing.T) {
	db := revDB(t)
	stale := insertRevision(t, db, "staged", ago(15*24*time.Hour), "")
	fresh := insertRevision(t, db, "staged", ago(24*time.Hour), "")

	st, err := PruneRevisions(db, prNow)
	if err != nil {
		t.Fatalf("PruneRevisions: %v", err)
	}
	if st.Superseded != 1 {
		t.Fatalf("Superseded = %d, want 1", st.Superseded)
	}

	status, decidedBy, proposed, _ := revisionRow(t, db, stale)
	if status != "superseded" || decidedBy.String != "system" {
		t.Fatalf("stale staged → status %q decided_by %q, want superseded/system", status, decidedBy.String)
	}
	if !proposed.Valid {
		t.Fatal("supersede must keep the file content — only the 90-day window nulls it")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM plan_revisions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("prune deleted a revision row (count=%d) — rows are the audit trail", count)
	}

	if status, _, _, _ := revisionRow(t, db, fresh); status != "staged" {
		t.Fatalf("fresh staged revision moved to %q", status)
	}
}

func TestPruneRevisions_NullsOldDecidedContent(t *testing.T) {
	db := revDB(t)
	old := insertRevision(t, db, "applied", ago(120*24*time.Hour), ago(91*24*time.Hour))
	recent := insertRevision(t, db, "rejected", ago(60*24*time.Hour), ago(30*24*time.Hour))
	staged := insertRevision(t, db, "staged", ago(2*24*time.Hour), "")

	st, err := PruneRevisions(db, prNow)
	if err != nil {
		t.Fatalf("PruneRevisions: %v", err)
	}
	if st.ContentNulled != 1 {
		t.Fatalf("ContentNulled = %d, want 1", st.ContentNulled)
	}

	status, decidedBy, proposed, preImage := revisionRow(t, db, old)
	if proposed.Valid || preImage.Valid {
		t.Fatal("91-day-old decided revision kept its proposed/pre_image")
	}
	if status != "applied" || decidedBy.String != "operator" {
		t.Fatalf("content nulling changed the decision row: %q/%q", status, decidedBy.String)
	}

	if _, _, proposed, preImage := revisionRow(t, db, recent); !proposed.Valid || !preImage.Valid {
		t.Fatal("30-day-old decided revision lost content inside the retention window")
	}
	if _, _, proposed, _ := revisionRow(t, db, staged); !proposed.Valid {
		t.Fatal("undecided staged revision lost content")
	}

	// A second pass is a no-op — the pass converges.
	st, err = PruneRevisions(db, prNow)
	if err != nil {
		t.Fatal(err)
	}
	if st.Superseded != 0 || st.ContentNulled != 0 {
		t.Fatalf("second pass not idempotent: %+v", st)
	}
}

func TestPruneRevisions_SupersededContentAgesFromDecision(t *testing.T) {
	db := revDB(t)
	// Staged 200 days ago: the supersede stamps decided_at = NOW, so its
	// content survives this pass and only ages out 90 days from the decision.
	id := insertRevision(t, db, "staged", ago(200*24*time.Hour), "")

	if _, err := PruneRevisions(db, prNow); err != nil {
		t.Fatal(err)
	}
	status, _, proposed, _ := revisionRow(t, db, id)
	if status != "superseded" || !proposed.Valid {
		t.Fatalf("supersede pass: status %q, content kept=%v — want superseded with content", status, proposed.Valid)
	}

	if _, err := PruneRevisions(db, prNow.Add(91*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, proposed, _ := revisionRow(t, db, id); proposed.Valid {
		t.Fatal("content survived 91 days past the system decision")
	}
}
