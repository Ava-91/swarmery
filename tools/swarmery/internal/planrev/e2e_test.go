package planrev_test

// End-to-end exercise of the plan-revision surface (phase 5): a real temp
// workspace scanned by wsingest, a revise session driven through the SAME
// transcript seam production uses (planning.OnSessionTurns), staging
// validation, the shared live-diff helper, the conflict abort, and the atomic
// apply with rename carry — no HTTP, no headless claude, no fixed sleeps.

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/planning"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/planrev"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/wsingest"
)

const e2eREADME = `# E2E Plan

| # | Phase | Doc | Depends on |
|---|-------|-----|------------|
| 1 | Store | ` + "`phase-1-store.md`" + ` | — |
| 2 | API | ` + "`phase-2-api.md`" + ` | 1 |
| 3 | UI | ` + "`phase-3-ui.md`" + ` | 2 |
`

const e2eProposedREADME = `# E2E Plan

| # | Phase | Doc | Depends on |
|---|-------|-----|------------|
| 1 | Store | ` + "`phase-1-store.md`" + ` | — |
| 2 | API | ` + "`phase-2-api.md`" + ` | 1 |
| 3 | Review | ` + "`phase-3-review.md`" + ` | 2 |
| 4 | Rollout | ` + "`phase-4-rollout.md`" + ` | 3 |
`

func openDoc(title string) string {
	return "# " + title + "\n\n- [ ] criterion A\n- [ ] criterion B\n\n## Completion Report\n"
}

const tickedPhase1 = "# Store\n\n- [x] criterion A\n- [x] criterion B\n\n## Completion Report\nShipped.\n"

// e2eFixture builds a REAL workspace task dir under a temp root, scans it with
// wsingest (the production ingest path — no hand-stitched rows), and returns
// the db, the scanner, the resolved ids and the live plan dir.
func e2eFixture(t *testing.T) (db *sql.DB, scanner *wsingest.Scanner, taskID, projectID int64, planDir string) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "e2e.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	root := t.TempDir()
	taskDir := filepath.Join(root, "e2e", "workspace", "working", "2026", "08", "11", "e2e-plan")
	planDir = filepath.Join(taskDir, "plan")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(taskDir, "README.md"): "# Task: E2E epic\n\n" +
			"- **Статус**: active\n- **Старт**: 2026-08-11 · **Завершено**: —\n- **Ціль**: e2e goal\n",
		filepath.Join(planDir, "README.md"):        e2eREADME,
		filepath.Join(planDir, "phase-1-store.md"): tickedPhase1,
		filepath.Join(planDir, "phase-2-api.md"):   openDoc("API"),
		filepath.Join(planDir, "phase-3-ui.md"):    openDoc("UI"),
	}
	for path, body := range files {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	scanner = wsingest.New(db, wsingest.Config{WorkspaceRoot: root})
	if _, err := scanner.Scan(); err != nil {
		t.Fatalf("workspace scan: %v", err)
	}
	if err := db.QueryRow(
		`SELECT id, project_id FROM tasks WHERE external_id = '2026-08-11-e2e-plan'`).
		Scan(&taskID, &projectID); err != nil {
		t.Fatalf("scanned task row: %v", err)
	}
	var phases int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM epic_phases WHERE workspace_task_id = ?`, taskID).Scan(&phases); err != nil {
		t.Fatal(err)
	}
	if phases != 3 {
		t.Fatalf("scan indexed %d epic_phases, want 3", phases)
	}
	return db, scanner, taskID, projectID, planDir
}

// reviseSvc builds the planning service exactly as tests drive it: inline, no
// runner (OnSessionTurns never spawns), dead process probe.
func reviseSvc(t *testing.T, db *sql.DB) *planning.Service {
	t.Helper()
	s := planning.NewService(db, nil)
	s.Go = func(fn func()) { fn() }
	s.FindRun = func(string) (int, bool) { return 0, false }
	s.ScratchRoot = filepath.Join(t.TempDir(), "revisions")
	return s
}

// writeScratch (re)writes the scratch dir contents for the session.
func writeScratch(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

var completionRe = regexp.MustCompile(`(?mi)^##\s+Completion Report\s*$`)

func TestE2E_ReviseStageDiffConflictApply(t *testing.T) {
	db, scanner, taskID, projectID, planDir := e2eFixture(t)
	svc := reviseSvc(t, db)

	// Snapshot the live bytes the proposal will be staged against.
	origPhase1 := readFile(t, filepath.Join(planDir, "phase-1-store.md"))
	origPhase2 := readFile(t, filepath.Join(planDir, "phase-2-api.md"))
	origPhase3 := readFile(t, filepath.Join(planDir, "phase-3-ui.md"))
	origREADME := readFile(t, filepath.Join(planDir, "README.md"))

	// Revise session: wizard row + ingested transcript ending in the sentinel —
	// the exact seam production staging runs through (OnSessionTurns).
	const uuid = "uuid-e2e-revise"
	mustExec(t, db, `
		INSERT INTO planning_sessions(project_id, session_uuid, status, idea, mode, revise_task_id, running_plan, created_at, updated_at)
		VALUES(?, ?, 'generating', 'phase 3 needs a review split', 'revise', ?, '{"title":"E2E"}', '2026-08-11T00:00:00Z', '2026-08-11T00:00:00Z')`,
		projectID, uuid, taskID)
	mustExec(t, db, `
		INSERT INTO sessions(id, project_id, session_uuid, status, cwd, started_at, source)
		VALUES(901, ?, ?, 'active', '/repo/e2e', '2026-08-11T00:00:00Z', 'jsonl')`, projectID, uuid)

	scratchDir := filepath.Join(svc.ScratchRoot, uuid)
	proposedPhase2 := "# API v2\n\n- [ ] criterion A\n- [ ] criterion B\n- [ ] criterion C\n\n## Completion Report\n"
	proposedPhase3 := "# Review\n\n- [ ] criterion A\n- [ ] criterion B\n\n## Completion Report\n"
	proposedPhase4 := "# Rollout\n\n- [ ] criterion A\n\n## Completion Report\n"

	// Step 3-4a: the proposal touches the TICKED phase 1 → the whole revision
	// must be rejected, nothing inserted, scratch kept, session resumable.
	const badManifest = `{"reason":"split review out","summary":{"title":"E2E"},"files":[
		{"path":"phase-1-store.md","action":"update"},
		{"path":"phase-2-api.md","action":"update"},
		{"path":"phase-3-review.md","action":"rename","renameFrom":"phase-3-ui.md"},
		{"path":"phase-4-rollout.md","action":"create"},
		{"path":"README.md","action":"update"}]}`
	writeScratch(t, scratchDir, map[string]string{
		"revision.json":      badManifest,
		"phase-1-store.md":   tickedPhase1,
		"phase-2-api.md":     proposedPhase2,
		"phase-3-review.md":  proposedPhase3,
		"phase-4-rollout.md": proposedPhase4,
		"README.md":          e2eProposedREADME,
	})
	mustExec(t, db, `INSERT INTO turns(session_id, seq, role, started_at, text)
		VALUES(901, 1, 'assistant', '2026-08-11T00:01:00Z', ?)`,
		"Proposal written.\n\nREVISION STAGED: "+scratchDir)
	svc.OnSessionTurns(uuid)

	var status string
	var rawReply sql.NullString
	if err := db.QueryRow(
		`SELECT status, raw_reply FROM planning_sessions WHERE session_uuid=?`, uuid).
		Scan(&status, &rawReply); err != nil {
		t.Fatal(err)
	}
	if status != "awaiting_answer" {
		t.Fatalf("after ticked-phase proposal: session status = %q, want awaiting_answer", status)
	}
	if !rawReply.Valid || !strings.Contains(rawReply.String, "REVISION REJECTED") ||
		!strings.Contains(rawReply.String, "phase-1-store.md") {
		t.Fatalf("rejection not surfaced to the operator: %q", rawReply.String)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM plan_revisions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("rejected proposal inserted %d revisions, want 0", n)
	}
	if _, err := os.Stat(filepath.Join(scratchDir, "revision.json")); err != nil {
		t.Fatalf("scratch dir was not kept on rejection: %v", err)
	}

	// Step 4b: drop the ticked-phase entry, re-stage — now it must land, with
	// the correct base_hash per file.
	const goodManifest = `{"reason":"split review out","summary":{"title":"E2E"},"files":[
		{"path":"phase-2-api.md","action":"update"},
		{"path":"phase-3-review.md","action":"rename","renameFrom":"phase-3-ui.md"},
		{"path":"phase-4-rollout.md","action":"create"},
		{"path":"README.md","action":"update"}]}`
	writeScratch(t, scratchDir, map[string]string{"revision.json": goodManifest})
	if err := os.Remove(filepath.Join(scratchDir, "phase-1-store.md")); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO turns(session_id, seq, role, started_at, text)
		VALUES(901, 2, 'assistant', '2026-08-11T00:02:00Z', ?)`,
		"Amended.\n\nREVISION STAGED: "+scratchDir)
	svc.OnSessionTurns(uuid)

	rev, err := planrev.LatestStaged(db, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if rev == nil {
		t.Fatal("amended proposal did not stage a revision")
	}
	if rev.PlanDir != planDir || rev.Origin != planrev.OriginOperator || rev.SessionUUID != uuid {
		t.Fatalf("staged revision = %+v", rev)
	}
	wantBase := map[string]string{
		"phase-2-api.md":     planrev.Sha256Hex([]byte(origPhase2)),
		"phase-3-review.md":  planrev.Sha256Hex([]byte(origPhase3)), // rename: base = the SOURCE bytes
		"phase-4-rollout.md": "",                                    // create: no base
		"README.md":          planrev.Sha256Hex([]byte(origREADME)),
	}
	if len(rev.Files) != len(wantBase) {
		t.Fatalf("staged %d files, want %d", len(rev.Files), len(wantBase))
	}
	for _, f := range rev.Files {
		if want, ok := wantBase[f.DocPath]; !ok || f.BaseHash != want {
			t.Errorf("file %s base_hash = %q, want %q", f.DocPath, f.BaseHash, want)
		}
	}
	if err := db.QueryRow(
		`SELECT status FROM planning_sessions WHERE session_uuid=?`, uuid).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "done" {
		t.Fatalf("session status after staging = %q, want done", status)
	}
	if _, err := os.Stat(scratchDir); !os.IsNotExist(err) {
		t.Fatalf("scratch dir not removed after successful staging (err=%v)", err)
	}

	// Step 5: the GET-shaped diff — the same helper the handler serves.
	diffs := planrev.LiveDiffs(rev)
	if len(diffs) != 4 {
		t.Fatalf("LiveDiffs returned %d files, want 4", len(diffs))
	}
	byPath := map[string]planrev.FileDiff{}
	for _, d := range diffs {
		byPath[d.DocPath] = d
		if d.Stale {
			t.Errorf("file %s reports stale=true before any live edit", d.DocPath)
		}
	}
	if d := byPath["phase-2-api.md"]; !strings.Contains(d.Diff, "+- [ ] criterion C") {
		t.Errorf("phase-2 diff misses the added criterion:\n%s", d.Diff)
	}
	if d := byPath["phase-3-review.md"]; d.RenameFrom != "phase-3-ui.md" ||
		!strings.Contains(d.Diff, "-# UI") || !strings.Contains(d.Diff, "+# Review") {
		t.Errorf("rename diff not computed against the source bytes:\n%s", d.Diff)
	}
	if d := byPath["phase-4-rollout.md"]; !strings.Contains(d.Diff, "+# Rollout") {
		t.Errorf("create diff is not a pure add:\n%s", d.Diff)
	}
	if d := byPath["README.md"]; !strings.Contains(d.Diff, "phase-4-rollout.md") {
		t.Errorf("README diff misses the new table row:\n%s", d.Diff)
	}

	// Step 6: drift one live doc → Apply must 409-shape with that doc in the
	// conflict list and change NOTHING.
	drifted := origPhase2 + "\n- [ ] criterion D (live edit)\n"
	if err := os.WriteFile(filepath.Join(planDir, "phase-2-api.md"), []byte(drifted), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = planrev.Apply(db, rev.ID, time.Now, nil)
	var cerr *planrev.ConflictError
	if !errors.As(err, &cerr) {
		t.Fatalf("Apply over drifted doc: err = %v, want ConflictError", err)
	}
	if len(cerr.Conflicts) != 1 || cerr.Conflicts[0].DocPath != "phase-2-api.md" {
		t.Fatalf("conflicts = %+v, want exactly phase-2-api.md", cerr.Conflicts)
	}
	if got := readFile(t, filepath.Join(planDir, "phase-2-api.md")); got != drifted {
		t.Fatal("conflicted Apply wrote to the drifted doc")
	}
	if _, err := os.Stat(filepath.Join(planDir, "phase-4-rollout.md")); !os.IsNotExist(err) {
		t.Fatal("conflicted Apply created the new doc")
	}
	if _, err := os.Stat(filepath.Join(planDir, "phase-3-ui.md")); err != nil {
		t.Fatal("conflicted Apply renamed the doc")
	}
	if got := readFile(t, filepath.Join(planDir, "README.md")); got != origREADME {
		t.Fatal("conflicted Apply rewrote the README")
	}
	if rev2, _ := planrev.Get(db, rev.ID); rev2 == nil || rev2.Status != planrev.StatusStaged {
		t.Fatalf("revision left status %v after conflict, want staged", rev2)
	}

	// Step 7: restore the doc; give the renamed phase daemon-owned run state
	// that MUST survive; Apply with the production rescan.
	if err := os.WriteFile(filepath.Join(planDir, "phase-2-api.md"), []byte(origPhase2), 0o644); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `UPDATE epic_phases SET run_state='failed', run_branch='swarm/phase-77', run_error='exit 1'
		WHERE workspace_task_id=? AND doc_path=?`, taskID, filepath.Join(planDir, "phase-3-ui.md"))

	applied, err := planrev.Apply(db, rev.ID, time.Now, func(string) {
		if _, err := scanner.Scan(); err != nil {
			t.Errorf("post-apply rescan: %v", err)
		}
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied != 4 {
		t.Fatalf("Apply reported %d files, want 4", applied)
	}

	// Files landed exactly as proposed; the ticked phase is byte-identical.
	if got := readFile(t, filepath.Join(planDir, "phase-1-store.md")); got != origPhase1 {
		t.Fatal("apply touched the ticked phase 1 doc")
	}
	if got := readFile(t, filepath.Join(planDir, "phase-2-api.md")); got != proposedPhase2 {
		t.Fatalf("phase-2 after apply:\n%s", got)
	}
	if got := readFile(t, filepath.Join(planDir, "phase-3-review.md")); got != proposedPhase3 {
		t.Fatalf("renamed phase-3 after apply:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(planDir, "phase-3-ui.md")); !os.IsNotExist(err) {
		t.Fatal("rename left the source doc behind")
	}
	if got := readFile(t, filepath.Join(planDir, "phase-4-rollout.md")); got != proposedPhase4 {
		t.Fatalf("created phase-4 after apply:\n%s", got)
	}
	if got := readFile(t, filepath.Join(planDir, "README.md")); got != e2eProposedREADME {
		t.Fatalf("README after apply:\n%s", got)
	}

	// The renamed phase's row kept its daemon-owned run state across the
	// explicit doc_path move + the rescan.
	var runState, runBranch string
	if err := db.QueryRow(
		`SELECT run_state, COALESCE(run_branch,'') FROM epic_phases WHERE workspace_task_id=? AND doc_path=?`,
		taskID, filepath.Join(planDir, "phase-3-review.md")).Scan(&runState, &runBranch); err != nil {
		t.Fatalf("renamed phase row: %v", err)
	}
	if runState != "failed" || runBranch != "swarm/phase-77" {
		t.Fatalf("renamed phase run state = %q/%q, want failed/swarm/phase-77", runState, runBranch)
	}

	// Post-apply README table parses with THE scanner's parser, no dangling deps.
	phases, err := wsingest.ParsePlanTable(readFile(t, filepath.Join(planDir, "README.md")))
	if err != nil {
		t.Fatalf("post-apply README table: %v", err)
	}
	if len(phases) != 4 {
		t.Fatalf("post-apply table has %d phases, want 4", len(phases))
	}
	seqs := map[int]bool{}
	for _, p := range phases {
		seqs[p.Seq] = true
	}
	for _, p := range phases {
		if _, err := os.Stat(filepath.Join(planDir, p.Doc)); err != nil {
			t.Errorf("table Doc %s does not resolve on disk: %v", p.Doc, err)
		}
		for _, dep := range p.DependsOn {
			if !seqs[dep] {
				t.Errorf("phase %d depends on %d, which is not a table row", p.Seq, dep)
			}
		}
	}

	// Every phase doc still carries the Completion Report contract.
	entries, err := os.ReadDir(planDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "phase-") {
			continue
		}
		if !completionRe.MatchString(readFile(t, filepath.Join(planDir, e.Name()))) {
			t.Errorf("%s lost its ## Completion Report section", e.Name())
		}
	}

	// Decision + audit trail stamped.
	final, err := planrev.Get(db, rev.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != planrev.StatusApplied || final.DecidedBy != "operator" || final.DecidedAt == "" {
		t.Fatalf("final revision = status %q decided_by %q at %q", final.Status, final.DecidedBy, final.DecidedAt)
	}
	for _, f := range final.Files {
		if f.AppliedHash == "" {
			t.Errorf("file %s has no applied_hash", f.DocPath)
		}
	}
}
