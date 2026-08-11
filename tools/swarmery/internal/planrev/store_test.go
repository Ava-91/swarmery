package planrev

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "planrev.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func stagedRevision(taskID int64) Revision {
	return Revision{
		WorkspaceTaskID: taskID,
		PlanDir:         "/ws/working/2026/08/11/task/plan",
		SessionUUID:     "sess-1",
		Origin:          OriginOperator,
		Reason:          "tighten phase 3",
		Summary:         `{"phases":3}`,
		CreatedAt:       "2026-08-11T10:00:00Z",
	}
}

func TestInsertGetRoundTrip(t *testing.T) {
	db := openDB(t)

	// Files staged out of order: Get must return them ordered by doc_path.
	files := []File{
		{DocPath: "phase-3-api.md", Action: ActionUpdate, BaseHash: Sha256Hex([]byte("old")), Proposed: "# phase 3 v2"},
		{DocPath: "README.md", Action: ActionUpdate, BaseHash: Sha256Hex([]byte("readme")), Proposed: "# plan v2"},
		{DocPath: "phase-4-ui.md", Action: ActionCreate, Proposed: "# phase 4"},
		{DocPath: "phase-2-store.md", Action: ActionRename, RenameFrom: "phase-2-db.md", Proposed: "# phase 2"},
		{DocPath: "phase-5-cleanup.md", Action: ActionDelete},
	}
	id, err := Insert(db, stagedRevision(7), files)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := Get(db, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("get returned nil for an existing revision")
	}
	if got.Status != StatusStaged {
		t.Errorf("status = %q, want %q", got.Status, StatusStaged)
	}
	if got.Origin != OriginOperator || got.Reason != "tighten phase 3" || got.Summary != `{"phases":3}` {
		t.Errorf("revision fields did not round-trip: %+v", got)
	}
	if got.SessionUUID != "sess-1" || got.CreatedAt != "2026-08-11T10:00:00Z" {
		t.Errorf("session/created fields did not round-trip: %+v", got)
	}
	if got.DecidedAt != "" || got.DecidedBy != "" || got.TriggerPhaseID != nil {
		t.Errorf("undecided revision carries decision fields: %+v", got)
	}

	wantOrder := []string{"README.md", "phase-2-store.md", "phase-3-api.md", "phase-4-ui.md", "phase-5-cleanup.md"}
	if len(got.Files) != len(wantOrder) {
		t.Fatalf("files = %d, want %d", len(got.Files), len(wantOrder))
	}
	for i, f := range got.Files {
		if f.DocPath != wantOrder[i] {
			t.Errorf("files[%d] = %q, want %q (doc_path order)", i, f.DocPath, wantOrder[i])
		}
	}
	if got.Files[1].RenameFrom != "phase-2-db.md" {
		t.Errorf("rename_from did not round-trip: %+v", got.Files[1])
	}
	if got.Files[4].Proposed != "" {
		t.Errorf("delete carries proposed content: %+v", got.Files[4])
	}
	if got.Files[2].Proposed != "# phase 3 v2" {
		t.Errorf("proposed did not round-trip: %+v", got.Files[2])
	}
}

func TestGetAbsent(t *testing.T) {
	db := openDB(t)
	got, err := Get(db, 999)
	if err != nil {
		t.Fatalf("get absent: %v", err)
	}
	if got != nil {
		t.Errorf("get absent = %+v, want nil", got)
	}
}

func TestInsertValidation(t *testing.T) {
	db := openDB(t)
	valid := File{DocPath: "phase-1.md", Action: ActionUpdate, Proposed: "x"}

	cases := []struct {
		name    string
		files   []File
		wantErr string
	}{
		{"empty file list", nil, ErrEmptyRevision.Error()},
		{"unknown action", []File{{DocPath: "a.md", Action: "overwrite", Proposed: "x"}}, "unknown action"},
		{"rename without rename_from", []File{{DocPath: "a.md", Action: ActionRename, Proposed: "x"}}, "no rename_from"},
		{"rename_from on non-rename", []File{{DocPath: "a.md", Action: ActionUpdate, RenameFrom: "b.md", Proposed: "x"}}, "must not set rename_from"},
		{"non-delete without content", []File{{DocPath: "a.md", Action: ActionCreate}}, "no proposed content"},
		{"delete with content", []File{{DocPath: "a.md", Action: ActionDelete, Proposed: "x"}}, "must not carry proposed"},
		{"absolute doc path", []File{{DocPath: "/etc/passwd", Action: ActionCreate, Proposed: "x"}}, "absolute"},
		{"dotdot doc path", []File{{DocPath: "../outside.md", Action: ActionCreate, Proposed: "x"}}, ".."},
		{"dotdot mid-path", []File{{DocPath: "sub/../../out.md", Action: ActionCreate, Proposed: "x"}}, ".."},
		{"dotdot rename_from", []File{{DocPath: "a.md", Action: ActionRename, RenameFrom: "../b.md", Proposed: "x"}}, ".."},
		{"one bad file poisons the batch", []File{valid, {DocPath: "b.md", Action: "bogus", Proposed: "x"}}, "unknown action"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Insert(db, stagedRevision(1), tc.files); err == nil {
				t.Fatal("insert accepted invalid input")
			} else if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
	if _, err := Insert(db, stagedRevision(1), []File{}); !errors.Is(err, ErrEmptyRevision) {
		t.Errorf("empty list error = %v, want ErrEmptyRevision", err)
	}

	// Nothing partial may survive a rejected batch.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM plan_revisions`).Scan(&n); err != nil {
		t.Fatalf("count revisions: %v", err)
	}
	if n != 0 {
		t.Errorf("%d revision rows exist after rejected inserts, want 0", n)
	}
}

func TestListByTaskNewestFirst(t *testing.T) {
	db := openDB(t)
	file := []File{{DocPath: "phase-1.md", Action: ActionUpdate, Proposed: "x"}}

	first, err := Insert(db, stagedRevision(3), file)
	if err != nil {
		t.Fatalf("insert first: %v", err)
	}
	if _, err := Decide(db, first, StatusSuperseded, "system", "2026-08-11T11:00:00Z"); err != nil {
		t.Fatalf("supersede first: %v", err)
	}
	second, err := Insert(db, stagedRevision(3), file)
	if err != nil {
		t.Fatalf("insert second: %v", err)
	}
	if _, err := Insert(db, stagedRevision(4), file); err != nil {
		t.Fatalf("insert other task: %v", err)
	}

	revs, err := ListByTask(db, 3)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(revs) != 2 {
		t.Fatalf("list = %d revisions, want 2 (other task excluded)", len(revs))
	}
	if revs[0].ID != second || revs[1].ID != first {
		t.Errorf("order = [%d, %d], want newest first [%d, %d]", revs[0].ID, revs[1].ID, second, first)
	}
	if len(revs[0].Files) != 1 {
		t.Errorf("listed revision has %d files, want 1 (history needs the doc count)", len(revs[0].Files))
	}
}

func TestLatestStaged(t *testing.T) {
	db := openDB(t)
	file := []File{{DocPath: "phase-1.md", Action: ActionUpdate, Proposed: "x"}}

	if got, err := LatestStaged(db, 5); err != nil || got != nil {
		t.Fatalf("latest staged of empty task = %+v, %v, want nil, nil", got, err)
	}
	first, err := Insert(db, stagedRevision(5), file)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := LatestStaged(db, 5)
	if err != nil {
		t.Fatalf("latest staged: %v", err)
	}
	if got == nil || got.ID != first {
		t.Fatalf("latest staged = %+v, want revision %d", got, first)
	}
	if _, err := Decide(db, first, StatusRejected, "operator", "2026-08-11T12:00:00Z"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if got, err := LatestStaged(db, 5); err != nil || got != nil {
		t.Fatalf("latest staged after reject = %+v, %v, want nil, nil", got, err)
	}
}

func TestDecideCAS(t *testing.T) {
	db := openDB(t)
	id, err := Insert(db, stagedRevision(9),
		[]File{{DocPath: "phase-1.md", Action: ActionUpdate, Proposed: "x"}})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	won, err := Decide(db, id, StatusApplied, "operator", "2026-08-11T13:00:00Z")
	if err != nil {
		t.Fatalf("first decide: %v", err)
	}
	if !won {
		t.Fatal("first decide lost the CAS on a staged revision")
	}
	won, err = Decide(db, id, StatusRejected, "operator", "2026-08-11T13:00:01Z")
	if err != nil {
		t.Fatalf("second decide: %v", err)
	}
	if won {
		t.Fatal("second decide won the CAS on an already-decided revision")
	}

	got, err := Get(db, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != StatusApplied || got.DecidedBy != "operator" || got.DecidedAt != "2026-08-11T13:00:00Z" {
		t.Errorf("losing decide mutated the row: %+v", got)
	}

	if _, err := Decide(db, id, StatusStaged, "operator", "2026-08-11T13:00:02Z"); err == nil {
		t.Error("decide accepted 'staged' — not a decision status")
	}
	if _, err := Decide(db, id, "bogus", "operator", "2026-08-11T13:00:03Z"); err == nil {
		t.Error("decide accepted an unknown status")
	}
}

func TestStampAppliedAndSetError(t *testing.T) {
	db := openDB(t)
	id, err := Insert(db, stagedRevision(11),
		[]File{{DocPath: "phase-1.md", Action: ActionUpdate, BaseHash: Sha256Hex([]byte("old")), Proposed: "new"}})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	rev, err := Get(db, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := StampApplied(tx, rev.Files[0].ID, "old", Sha256Hex([]byte("new"))); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	if err := StampApplied(tx, 9999, "x", "y"); err == nil {
		t.Error("stamp accepted a missing file row")
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	got, err := Get(db, id)
	if err != nil {
		t.Fatalf("get after stamp: %v", err)
	}
	if got.Files[0].AppliedHash != Sha256Hex([]byte("new")) {
		t.Errorf("applied_hash = %q, want hash of new content", got.Files[0].AppliedHash)
	}

	if err := SetError(db, id, "apply failed: base drift", "2026-08-11T14:00:00Z"); err != nil {
		t.Fatalf("set error: %v", err)
	}
	got, err = Get(db, id)
	if err != nil {
		t.Fatalf("get after set error: %v", err)
	}
	if got.Status != StatusFailed || got.Error != "apply failed: base drift" || got.DecidedBy != "system" {
		t.Errorf("set error did not mark the revision failed: %+v", got)
	}
}

func TestSha256HexEncoding(t *testing.T) {
	// Locked encoding: lowercase hex of the raw sha256 sum (sysscan's content_hash).
	if got := Sha256Hex([]byte("")); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("Sha256Hex(\"\") = %s", got)
	}
}
