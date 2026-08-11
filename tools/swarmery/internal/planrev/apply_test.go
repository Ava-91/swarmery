package planrev

import (
	"bytes"
	"database/sql"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/wsingest"
)

func fixedNow() time.Time { return time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC) }

func writeDoc(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readDoc(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func docAbsent(t *testing.T, dir, name string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
		t.Errorf("%s should not exist (err=%v)", name, err)
	}
}

func revisionStatus(t *testing.T, db *sql.DB, id int64) string {
	t.Helper()
	var s string
	if err := db.QueryRow(`SELECT status FROM plan_revisions WHERE id = ?`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// noTmpLeftovers asserts the atomic-write staging files were all consumed.
func noTmpLeftovers(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-rev-") {
			t.Errorf("stale tmp file left behind: %s", e.Name())
		}
	}
}

func stagedRevisionAt(taskID int64, planDir string) Revision {
	r := stagedRevision(taskID)
	r.PlanDir = planDir
	return r
}

func TestApplyHappyPath(t *testing.T) {
	db := openDB(t)
	planDir := t.TempDir()
	writeDoc(t, planDir, "README.md", "# plan v1")
	writeDoc(t, planDir, "phase-2-db.md", "# phase 2 v1")
	writeDoc(t, planDir, "phase-5-cleanup.md", "bye")

	files := []File{
		{DocPath: "README.md", Action: ActionUpdate, BaseHash: Sha256Hex([]byte("# plan v1")), Proposed: "# plan v2"},
		{DocPath: "phase-2-store.md", Action: ActionRename, RenameFrom: "phase-2-db.md",
			BaseHash: Sha256Hex([]byte("# phase 2 v1")), Proposed: "# phase 2 v2"},
		{DocPath: "phase-4-ui.md", Action: ActionCreate, Proposed: "# phase 4"},
		{DocPath: "phase-5-cleanup.md", Action: ActionDelete, BaseHash: Sha256Hex([]byte("bye"))},
	}
	id, err := Insert(db, stagedRevisionAt(7, planDir), files)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	rescans := 0
	var rescanDir string
	createdAtRescan := false
	rescan := func(dir string) {
		rescans++
		rescanDir = dir
		// The rescan must fire AFTER all writes: the created doc is visible.
		if _, err := os.Stat(filepath.Join(planDir, "phase-4-ui.md")); err == nil {
			createdAtRescan = true
		}
	}

	n, err := Apply(db, id, fixedNow, rescan)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if n != 4 {
		t.Errorf("files applied = %d, want 4", n)
	}

	if got := readDoc(t, planDir, "README.md"); got != "# plan v2" {
		t.Errorf("README.md = %q, want update applied", got)
	}
	if got := readDoc(t, planDir, "phase-2-store.md"); got != "# phase 2 v2" {
		t.Errorf("phase-2-store.md = %q, want rename+content applied", got)
	}
	if got := readDoc(t, planDir, "phase-4-ui.md"); got != "# phase 4" {
		t.Errorf("phase-4-ui.md = %q, want create applied", got)
	}
	docAbsent(t, planDir, "phase-2-db.md")
	docAbsent(t, planDir, "phase-5-cleanup.md")
	noTmpLeftovers(t, planDir)

	if rescans != 1 {
		t.Errorf("rescan fired %d times, want exactly 1", rescans)
	}
	if rescanDir != planDir {
		t.Errorf("rescan dir = %q, want %q", rescanDir, planDir)
	}
	if !createdAtRescan {
		t.Error("rescan fired before the writes landed")
	}

	rev, err := Get(db, id)
	if err != nil {
		t.Fatal(err)
	}
	if rev.Status != StatusApplied || rev.DecidedBy != "operator" || rev.DecidedAt == "" {
		t.Errorf("decision = %s/%s/%s, want applied/operator/stamped", rev.Status, rev.DecidedBy, rev.DecidedAt)
	}

	// pre_image + applied_hash stamped per file.
	wantPre := map[string]string{
		"README.md":          "# plan v1",
		"phase-2-store.md":   "# phase 2 v1",
		"phase-4-ui.md":      "", // create — nothing was replaced
		"phase-5-cleanup.md": "bye",
	}
	wantApplied := map[string]string{
		"README.md":          Sha256Hex([]byte("# plan v2")),
		"phase-2-store.md":   Sha256Hex([]byte("# phase 2 v2")),
		"phase-4-ui.md":      Sha256Hex([]byte("# phase 4")),
		"phase-5-cleanup.md": "", // delete — nothing was written
	}
	rows, err := db.Query(`SELECT doc_path, pre_image, applied_hash FROM plan_revision_files WHERE revision_id = ?`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var doc string
		var pre, applied sql.NullString
		if err := rows.Scan(&doc, &pre, &applied); err != nil {
			t.Fatal(err)
		}
		if pre.String != wantPre[doc] {
			t.Errorf("%s pre_image = %q, want %q", doc, pre.String, wantPre[doc])
		}
		if applied.String != wantApplied[doc] {
			t.Errorf("%s applied_hash = %q, want %q", doc, applied.String, wantApplied[doc])
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestApplyConflictPerformsNoWrites(t *testing.T) {
	db := openDB(t)
	planDir := t.TempDir()
	writeDoc(t, planDir, "a.md", "one")
	writeDoc(t, planDir, "b.md", "two")

	files := []File{
		{DocPath: "a.md", Action: ActionUpdate, BaseHash: Sha256Hex([]byte("one")), Proposed: "ONE"},
		// Staged against content that is NOT what is on disk.
		{DocPath: "b.md", Action: ActionUpdate, BaseHash: Sha256Hex([]byte("stale-two")), Proposed: "TWO"},
	}
	id, err := Insert(db, stagedRevisionAt(7, planDir), files)
	if err != nil {
		t.Fatal(err)
	}

	rescans := 0
	_, err = Apply(db, id, fixedNow, func(string) { rescans++ })
	var cerr *ConflictError
	if !errors.As(err, &cerr) {
		t.Fatalf("err = %v, want *ConflictError", err)
	}
	if len(cerr.Conflicts) != 1 {
		t.Fatalf("conflicts = %d, want 1", len(cerr.Conflicts))
	}
	c := cerr.Conflicts[0]
	if c.DocPath != "b.md" || c.BaseHash != Sha256Hex([]byte("stale-two")) || c.DiskHash != Sha256Hex([]byte("two")) {
		t.Errorf("conflict = %+v, want b.md base/disk hashes", c)
	}
	if !strings.Contains(c.Diff, "proposed/b.md") || !strings.Contains(c.Diff, "disk/b.md") {
		t.Errorf("diff header should name proposed vs disk, got:\n%s", c.Diff)
	}

	// NO writes, no decision, no rescan.
	if got := readDoc(t, planDir, "a.md"); got != "one" {
		t.Errorf("a.md = %q — conflict must abort before ANY write", got)
	}
	if got := readDoc(t, planDir, "b.md"); got != "two" {
		t.Errorf("b.md = %q, want untouched", got)
	}
	if s := revisionStatus(t, db, id); s != StatusStaged {
		t.Errorf("status = %q, want still staged", s)
	}
	if rescans != 0 {
		t.Errorf("rescan fired %d times on the conflict path", rescans)
	}
}

func TestApplyRefusesRunningPhase(t *testing.T) {
	db := openDB(t)
	planDir := t.TempDir()
	writeDoc(t, planDir, "a.md", "v1")

	id, err := Insert(db, stagedRevisionAt(7, planDir), []File{
		{DocPath: "a.md", Action: ActionUpdate, BaseHash: Sha256Hex([]byte("v1")), Proposed: "v2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO epic_phases (workspace_task_id, seq, name, doc_path, run_state)
		VALUES (7, 1, 'A', ?, 'running')`, filepath.Join(planDir, "a.md")); err != nil {
		t.Fatal(err)
	}

	_, err = Apply(db, id, fixedNow, nil)
	if !errors.Is(err, ErrPhaseRunning) {
		t.Fatalf("err = %v, want ErrPhaseRunning", err)
	}
	if got := readDoc(t, planDir, "a.md"); got != "v1" {
		t.Errorf("a.md = %q — run guard must abort before any write", got)
	}
	if s := revisionStatus(t, db, id); s != StatusStaged {
		t.Errorf("status = %q, want still staged", s)
	}
}

func TestApplyNotFoundAndNotStaged(t *testing.T) {
	db := openDB(t)

	if _, err := Apply(db, 12345, fixedNow, nil); !errors.Is(err, ErrRevisionNotFound) {
		t.Errorf("unknown id: err = %v, want ErrRevisionNotFound", err)
	}

	planDir := t.TempDir()
	writeDoc(t, planDir, "a.md", "v1")
	id, err := Insert(db, stagedRevisionAt(7, planDir), []File{
		{DocPath: "a.md", Action: ActionUpdate, BaseHash: Sha256Hex([]byte("v1")), Proposed: "v2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decide(db, id, StatusRejected, "operator", "2026-08-11T11:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(db, id, fixedNow, nil); !errors.Is(err, ErrNotStaged) {
		t.Errorf("rejected revision: err = %v, want ErrNotStaged", err)
	}
}

// TestApplyRollsBackOnMidSequenceFailure injects a failure on the 3rd of 4
// writes (a create whose target name is unexpectedly occupied on disk) and
// asserts every file is byte-identical to its pre-revision content and the
// revision is stamped 'failed' with the error.
func TestApplyRollsBackOnMidSequenceFailure(t *testing.T) {
	db := openDB(t)
	planDir := t.TempDir()
	writeDoc(t, planDir, "a.md", "AAA")
	writeDoc(t, planDir, "b.md", "BBB")
	writeDoc(t, planDir, "d.md", "stray!") // occupies the create target → write 3 fails
	writeDoc(t, planDir, "e.md", "EEE")

	files := []File{
		{DocPath: "a.md", Action: ActionDelete, BaseHash: Sha256Hex([]byte("AAA"))},
		{DocPath: "c.md", Action: ActionRename, RenameFrom: "b.md", BaseHash: Sha256Hex([]byte("BBB")), Proposed: "CCC"},
		{DocPath: "d.md", Action: ActionCreate, Proposed: "DDD"},
		{DocPath: "e.md", Action: ActionUpdate, BaseHash: Sha256Hex([]byte("EEE")), Proposed: "EEE2"},
	}
	id, err := Insert(db, stagedRevisionAt(7, planDir), files)
	if err != nil {
		t.Fatal(err)
	}

	rescans := 0
	_, err = Apply(db, id, fixedNow, func(string) { rescans++ })
	if err == nil || !strings.Contains(err.Error(), "create d.md") {
		t.Fatalf("err = %v, want the injected create failure", err)
	}

	// Every completed step rolled back from its pre-image.
	if got := readDoc(t, planDir, "a.md"); got != "AAA" {
		t.Errorf("a.md = %q, want delete rolled back to AAA", got)
	}
	if got := readDoc(t, planDir, "b.md"); got != "BBB" {
		t.Errorf("b.md = %q, want rename rolled back to BBB", got)
	}
	docAbsent(t, planDir, "c.md")
	if got := readDoc(t, planDir, "d.md"); got != "stray!" {
		t.Errorf("d.md = %q, want the stray file untouched", got)
	}
	if got := readDoc(t, planDir, "e.md"); got != "EEE" {
		t.Errorf("e.md = %q, want the never-reached update untouched", got)
	}
	noTmpLeftovers(t, planDir)

	rev, err := Get(db, id)
	if err != nil {
		t.Fatal(err)
	}
	if rev.Status != StatusFailed || rev.Error == "" {
		t.Errorf("revision = %s (error=%q), want failed with the error recorded", rev.Status, rev.Error)
	}
	if rescans != 0 {
		t.Errorf("rescan fired %d times on the failure path", rescans)
	}
}

// TestApplyRenameCarriesPhaseStateAcrossRescan applies a revision that
// RENUMBERS a phase (seq 2 → 3 via rename, with a new doc taking seq 2) and
// asserts the daemon-owned run state survives the subsequent real wsingest
// rescan. carryAcrossRenames alone would NOT have carried it (its match is
// 1:1 on seq) — the explicit doc_path move in Apply is what preserves it.
func TestApplyRenameCarriesPhaseStateAcrossRescan(t *testing.T) {
	db := openDB(t)
	root := t.TempDir()
	taskDir := filepath.Join(root, "demo", "workspace", "working", "2026", "08", "11", "demo")
	planDir := filepath.Join(taskDir, "plan")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	card := "# Task: Demo epic\n\n" +
		"- **Статус**: active\n- **Старт**: 2026-08-11 · **Завершено**: —\n- **Ціль**: demo goal\n"
	writeDoc(t, taskDir, "README.md", card)
	readmeV1 := "# Demo plan\n\n| # | Phase | Doc | Depends on |\n|---|---|---|---|\n" +
		"| 1 | Store | `phase-1-store.md` | — |\n" +
		"| 2 | API | `phase-2-api.md` | 1 |\n"
	writeDoc(t, planDir, "README.md", readmeV1)
	writeDoc(t, planDir, "phase-1-store.md", "# store\n- [x] done\n")
	apiV1 := "# api\n- [x] a\n- [x] b\n- [x] c\n"
	writeDoc(t, planDir, "phase-2-api.md", apiV1)

	scanner := wsingest.New(db, wsingest.Config{WorkspaceRoot: root})
	if _, err := scanner.Scan(); err != nil {
		t.Fatalf("initial scan: %v", err)
	}
	var taskID int64
	if err := db.QueryRow(`SELECT id FROM tasks WHERE source = 'workspace'`).Scan(&taskID); err != nil {
		t.Fatalf("workspace task not indexed: %v", err)
	}
	apiAbs := filepath.Join(planDir, "phase-2-api.md")
	if _, err := db.Exec(`
		UPDATE epic_phases SET run_state='done', run_branch='swarm/phase-42',
		       run_session_uuid='sess-x', run_checkboxes_before=0, run_checkboxes_after=3,
		       activated_board_task_id=99
		 WHERE workspace_task_id = ? AND doc_path = ?`, taskID, apiAbs); err != nil {
		t.Fatal(err)
	}

	readmeV2 := "# Demo plan\n\n| # | Phase | Doc | Depends on |\n|---|---|---|---|\n" +
		"| 1 | Store | `phase-1-store.md` | — |\n" +
		"| 2 | Middle | `phase-2-middle.md` | 1 |\n" +
		"| 3 | API | `phase-3-api.md` | 2 |\n"
	id, err := Insert(db, stagedRevisionAt(taskID, planDir), []File{
		{DocPath: "README.md", Action: ActionUpdate, BaseHash: Sha256Hex([]byte(readmeV1)), Proposed: readmeV2},
		{DocPath: "phase-2-middle.md", Action: ActionCreate, Proposed: "# middle\n- [ ] new\n"},
		{DocPath: "phase-3-api.md", Action: ActionRename, RenameFrom: "phase-2-api.md",
			BaseHash: Sha256Hex([]byte(apiV1)), Proposed: apiV1},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Apply(db, id, fixedNow, func(string) {
		if _, err := scanner.Scan(); err != nil {
			t.Errorf("rescan: %v", err)
		}
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// The renamed doc's row kept every daemon-owned column, now at seq 3.
	var (
		seq, after int
		runState   string
		branch     sql.NullString
		boardTask  sql.NullInt64
	)
	newAbs := filepath.Join(planDir, "phase-3-api.md")
	if err := db.QueryRow(`
		SELECT seq, run_state, run_branch, run_checkboxes_after, activated_board_task_id
		  FROM epic_phases WHERE workspace_task_id = ? AND doc_path = ?`,
		taskID, newAbs).Scan(&seq, &runState, &branch, &after, &boardTask); err != nil {
		t.Fatalf("renamed phase row: %v", err)
	}
	if seq != 3 {
		t.Errorf("seq = %d, want renumbered to 3", seq)
	}
	if runState != "done" || branch.String != "swarm/phase-42" || after != 3 || boardTask.Int64 != 99 {
		t.Errorf("run state dropped across rename+rescan: state=%s branch=%s after=%d board=%d",
			runState, branch.String, after, boardTask.Int64)
	}
	// The old path is gone; the new middle phase is indexed idle.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM epic_phases WHERE doc_path = ?`, apiAbs).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("old doc_path row still present after carry+rescan")
	}
	var midState string
	if err := db.QueryRow(`
		SELECT run_state FROM epic_phases WHERE workspace_task_id = ? AND doc_path = ?`,
		taskID, filepath.Join(planDir, "phase-2-middle.md")).Scan(&midState); err != nil {
		t.Fatalf("new middle phase not indexed: %v", err)
	}
	if midState != "idle" {
		t.Errorf("new phase run_state = %q, want idle", midState)
	}
}

// TestApplyConcurrentRejectLeavesDiskApplied loses the decision CAS to a
// reject that lands mid-apply: the write still succeeds (disk is the truth),
// the decision row keeps the reject, and the collision is logged loudly.
func TestApplyConcurrentRejectLeavesDiskApplied(t *testing.T) {
	db := openDB(t)
	planDir := t.TempDir()
	writeDoc(t, planDir, "a.md", "v1")

	id, err := Insert(db, stagedRevisionAt(7, planDir), []File{
		{DocPath: "a.md", Action: ActionUpdate, BaseHash: Sha256Hex([]byte("v1")), Proposed: "v2"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Apply reads the clock once, right after its staged check — the seam
	// where a concurrent reject can slip in.
	rejectingNow := func() time.Time {
		if _, err := Decide(db, id, StatusRejected, "operator", "2026-08-11T11:59:00Z"); err != nil {
			t.Fatalf("concurrent reject: %v", err)
		}
		return fixedNow()
	}

	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(os.Stderr)

	n, err := Apply(db, id, rejectingNow, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if n != 1 {
		t.Errorf("files = %d, want 1", n)
	}
	if got := readDoc(t, planDir, "a.md"); got != "v2" {
		t.Errorf("a.md = %q — the write landed, disk is the truth", got)
	}
	if s := revisionStatus(t, db, id); s != StatusRejected {
		t.Errorf("status = %q, want the concurrent reject kept", s)
	}
	if !strings.Contains(logBuf.String(), "decided concurrently") {
		t.Errorf("expected a loud log about the lost CAS, got: %s", logBuf.String())
	}
}
