package planning

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/planrev"
)

// ── fixtures ──

const reviseTaskID = int64(10)

// livePlanREADME is the fixture plan's phase-sequencing table (3 phases).
const livePlanREADME = `# Test Plan

| # | Phase | Doc | Depends on |
|---|-------|-----|------------|
| 1 | Store | ` + "`phase-1-store.md`" + ` | — |
| 2 | API | ` + "`phase-2-api.md`" + ` | 1 |
| 3 | UI | ` + "`phase-3-ui.md`" + ` | 2 |
`

func phaseDoc(title string) string {
	return "# " + title + "\n\n- [ ] criterion A\n- [ ] criterion B\n\n## Completion Report\n"
}

// revisePlanFixture builds a live plan dir on disk plus the DB rows a revise
// session needs: workspace task 10 → plan artifact → 3 epic_phases (phase 1
// fully ticked = DONE, phases 2-3 open).
func revisePlanFixture(t *testing.T, db *sql.DB) (planDir string) {
	t.Helper()
	planDir = filepath.Join(t.TempDir(), "plan")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(planDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md", livePlanREADME)
	write("phase-1-store.md", "# Store\n\n- [x] criterion A\n- [x] criterion B\n\n## Completion Report\nShipped.\n")
	write("phase-2-api.md", phaseDoc("API"))
	write("phase-3-ui.md", phaseDoc("UI"))

	if _, err := db.Exec(
		`INSERT INTO tasks(id, project_id, title, prompt, created_at, source)
		 VALUES(?, 1, 'Test Plan', '', '2026-01-01T00:00:00Z', 'workspace')`, reviseTaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO task_artifacts(task_id, kind, path, content_hash, parsed_at)
		 VALUES(?, 'plan', ?, 'h', '2026-01-01T00:00:00Z')`, reviseTaskID, planDir); err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct {
		seq         int
		name, doc   string
		done, total int
	}{
		{1, "Store", "phase-1-store.md", 2, 2},
		{2, "API", "phase-2-api.md", 0, 2},
		{3, "UI", "phase-3-ui.md", 0, 2},
	} {
		if _, err := db.Exec(
			`INSERT INTO epic_phases(workspace_task_id, seq, name, doc_path, checkboxes_total, checkboxes_done)
			 VALUES(?, ?, ?, ?, ?, ?)`,
			reviseTaskID, p.seq, p.name, filepath.Join(planDir, p.doc), p.total, p.done); err != nil {
			t.Fatal(err)
		}
	}
	return planDir
}

// reviseService builds an inline service with a scratch root and a dead
// process probe (raw-fallback paths must not shell out to ps).
func reviseService(t *testing.T, db *sql.DB) *Service {
	t.Helper()
	s := newInlineService(t, db, &stubRunner{})
	s.ScratchRoot = filepath.Join(t.TempDir(), "revisions")
	s.FindRun = func(string) (int, bool) { return 0, false }
	return s
}

// insertReviseWizard crafts a mode='revise' planning_sessions row directly.
func insertReviseWizard(t *testing.T, db *sql.DB, uuid, status string) int64 {
	t.Helper()
	res, err := db.Exec(
		`INSERT INTO planning_sessions(project_id, session_uuid, status, idea, mode, revise_task_id, running_plan, created_at, updated_at)
		 VALUES(1, ?, ?, 'phase 2 hit the wrong endpoint', 'revise', ?, '{"title":"T"}', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		uuid, status, reviseTaskID)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

// stageScratch writes a scratch dir (manifest + content files) under the
// service's scratch root and returns its path.
func stageScratch(t *testing.T, s *Service, uuid string, manifest string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(s.ScratchRoot, uuid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if manifest != "" {
		if err := os.WriteFile(filepath.Join(dir, "revision.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// reviseFixture wires the full staging scenario: plan on disk, revise wizard
// row in `status`, ingested session whose newest assistant turn ends with the
// REVISION STAGED sentinel for the given scratch dir.
func reviseFixture(t *testing.T, db *sql.DB, s *Service, status, scratchDir string) (uuid string) {
	t.Helper()
	uuid = "uuid-revise"
	insertReviseWizard(t, db, uuid, status)
	insertSessionRow(t, db, 42, uuid)
	insertAssistantTurn(t, db, 42, 1, "All proposed files are written.\n\nREVISION STAGED: "+scratchDir)
	return uuid
}

func revisionCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM plan_revisions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// validManifest revises phase 2, adds phase 4, and updates the README table.
const validManifest = `{"reason":"fix the api phase","summary":{"title":"T"},"files":[
	{"path":"phase-2-api.md","action":"update"},
	{"path":"phase-4-rollout.md","action":"create"},
	{"path":"README.md","action":"update"}]}`

const revisedREADME = `# Test Plan

| # | Phase | Doc | Depends on |
|---|-------|-----|------------|
| 1 | Store | ` + "`phase-1-store.md`" + ` | — |
| 2 | API | ` + "`phase-2-api.md`" + ` | 1 |
| 3 | UI | ` + "`phase-3-ui.md`" + ` | 2 |
| 4 | Rollout | ` + "`phase-4-rollout.md`" + ` | 3 |
`

func validScratchFiles() map[string]string {
	return map[string]string{
		"phase-2-api.md":     phaseDoc("API v2"),
		"phase-4-rollout.md": phaseDoc("Rollout"),
		"README.md":          revisedREADME,
	}
}

// ── BuildEvidence ──

func TestBuildEvidence(t *testing.T) {
	db := testDB(t)
	revisePlanFixture(t, db)
	if _, err := db.Exec(
		`UPDATE epic_phases SET run_state='failed', run_error='exit 1', run_branch='swarm/phase-2'
		  WHERE workspace_task_id=? AND seq=2`, reviseTaskID); err != nil {
		t.Fatal(err)
	}

	evidence, doneDocs, err := BuildEvidence(db, reviseTaskID)
	if err != nil {
		t.Fatalf("BuildEvidence: %v", err)
	}
	if len(doneDocs) != 1 || doneDocs[0] != "phase-1-store.md" {
		t.Errorf("doneDocs = %v, want [phase-1-store.md]", doneDocs)
	}
	for _, must := range []string{
		"| 1 | `phase-1-store.md` | 2/2 |",
		"| 2 | `phase-2-api.md` | 0/2 | failed | failed | swarm/phase-2 | exit 1 |",
		"| 3 | `phase-3-ui.md` | 0/2 | idle | idle |",
	} {
		if !strings.Contains(evidence, must) {
			t.Errorf("evidence missing %q\n%s", must, evidence)
		}
	}
}

// ── StartRevise ──

func TestStartRevise_HappyPath(t *testing.T) {
	db := testDB(t)
	planDir := revisePlanFixture(t, db)
	r := &stubRunner{}
	s := newInlineService(t, db, r)
	s.ScratchRoot = filepath.Join(t.TempDir(), "revisions")

	uuid, err := s.StartRevise(reviseTaskID, "phase 2 needs a different endpoint", nil)
	if err != nil {
		t.Fatalf("StartRevise: %v", err)
	}
	var mode string
	var reviseTask sql.NullInt64
	var idea string
	if err := db.QueryRow(
		`SELECT mode, revise_task_id, idea FROM planning_sessions WHERE session_uuid=?`, uuid).
		Scan(&mode, &reviseTask, &idea); err != nil {
		t.Fatalf("wizard row not inserted: %v", err)
	}
	if mode != ModeRevise || !reviseTask.Valid || reviseTask.Int64 != reviseTaskID {
		t.Errorf("row mode=%q reviseTask=%+v, want revise/%d", mode, reviseTask, reviseTaskID)
	}
	if idea != "phase 2 needs a different endpoint" {
		t.Errorf("idea = %q, want the operator's reason", idea)
	}
	scratch := filepath.Join(s.ScratchRoot, uuid)
	if fi, err := os.Stat(scratch); err != nil || !fi.IsDir() {
		t.Errorf("scratch dir %q not created: %v", scratch, err)
	}
	spec := r.lastSpec()
	if spec.Cwd != "/repo/p" {
		t.Errorf("spec.Cwd = %q, want the project path", spec.Cwd)
	}
	for _, must := range []string{planDir, scratch, "phase-1-store.md", "REVISION STAGED: " + scratch} {
		if !strings.Contains(spec.Prompt, must) {
			t.Errorf("revise prompt missing %q", must)
		}
	}
}

func TestStartRevise_TaskNotFound(t *testing.T) {
	db := testDB(t)
	s := reviseService(t, db)
	if _, err := s.StartRevise(999, "r", nil); err != ErrTaskNotFound {
		t.Fatalf("err = %v, want ErrTaskNotFound", err)
	}
}

func TestStartRevise_NoPlan(t *testing.T) {
	db := testDB(t)
	if _, err := db.Exec(
		`INSERT INTO tasks(id, project_id, title, prompt, created_at, source)
		 VALUES(11, 1, 'no plan', '', '2026-01-01T00:00:00Z', 'workspace')`); err != nil {
		t.Fatal(err)
	}
	s := reviseService(t, db)
	if _, err := s.StartRevise(11, "r", nil); err != ErrNoPlan {
		t.Fatalf("err = %v, want ErrNoPlan", err)
	}
}

func TestStartRevise_PlanBusy_RunningPhase(t *testing.T) {
	db := testDB(t)
	revisePlanFixture(t, db)
	if _, err := db.Exec(
		`UPDATE epic_phases SET run_state='running' WHERE workspace_task_id=? AND seq=2`, reviseTaskID); err != nil {
		t.Fatal(err)
	}
	s := reviseService(t, db)
	if _, err := s.StartRevise(reviseTaskID, "r", nil); err != ErrPlanBusy {
		t.Fatalf("err = %v, want ErrPlanBusy", err)
	}
}

func TestStartRevise_PlanBusy_LivePlanRun(t *testing.T) {
	db := testDB(t)
	revisePlanFixture(t, db)
	if _, err := db.Exec(
		`INSERT INTO plan_runs(workspace_task_id, run_state) VALUES(?, 'running')`, reviseTaskID); err != nil {
		t.Fatal(err)
	}
	s := reviseService(t, db)
	if _, err := s.StartRevise(reviseTaskID, "r", nil); err != ErrPlanBusy {
		t.Fatalf("err = %v, want ErrPlanBusy", err)
	}
}

func TestStartRevise_RevisionOpen(t *testing.T) {
	db := testDB(t)
	planDir := revisePlanFixture(t, db)
	if _, err := planrev.Insert(db,
		planrev.Revision{WorkspaceTaskID: reviseTaskID, PlanDir: planDir, CreatedAt: "2026-01-01T00:00:00Z"},
		[]planrev.File{{DocPath: "phase-2-api.md", Action: planrev.ActionUpdate, Proposed: "x"}}); err != nil {
		t.Fatal(err)
	}
	s := reviseService(t, db)
	if _, err := s.StartRevise(reviseTaskID, "r", nil); err != ErrRevisionOpen {
		t.Fatalf("err = %v, want ErrRevisionOpen", err)
	}
}

// ── the PLAN SAVED protocol violation ──

func TestReviseSession_PlanSavedNeverMarksDone(t *testing.T) {
	db := testDB(t)
	revisePlanFixture(t, db)
	s := reviseService(t, db)
	uuid := "uuid-revise"
	insertReviseWizard(t, db, uuid, StatusProceeding)
	insertSessionRow(t, db, 42, uuid)
	insertAssistantTurn(t, db, 42, 1,
		"Wrote a brand new plan instead.\n\nPLAN SAVED: /ws/p/workspace/working/2026/08/11/rogue/plan")

	s.OnSessionTurns(uuid)

	status, _, raw, planDir := wizardState(t, db, uuid)
	if status != StatusAwaiting {
		t.Fatalf("status = %q, want awaiting_answer (raw fallback, never done)", status)
	}
	if planDir.Valid {
		t.Errorf("plan_dir = %q, want NULL — a revise session must not adopt a plan dir", planDir.String)
	}
	if !raw.Valid || !strings.Contains(raw.String, "PLAN SAVED:") {
		t.Errorf("raw_reply = %+v, want the violating reply surfaced", raw)
	}
	if revisionCount(t, db) != 0 {
		t.Error("a PLAN SAVED reply must not stage a revision")
	}
}

// ── staging: the happy path ──

func TestStage_HappyPath(t *testing.T) {
	db := testDB(t)
	planDir := revisePlanFixture(t, db)
	s := reviseService(t, db)
	scratch := stageScratch(t, s, "uuid-revise", validManifest, validScratchFiles())
	uuid := reviseFixture(t, db, s, StatusProceeding, scratch)

	liveAPI, err := os.ReadFile(filepath.Join(planDir, "phase-2-api.md"))
	if err != nil {
		t.Fatal(err)
	}

	s.OnSessionTurns(uuid)

	status, _, _, _ := wizardState(t, db, uuid)
	if status != StatusDone {
		t.Fatalf("status = %q, want done", status)
	}
	rev, err := planrev.LatestStaged(db, reviseTaskID)
	if err != nil || rev == nil {
		t.Fatalf("LatestStaged = %+v, %v — want the staged revision", rev, err)
	}
	if rev.PlanDir != planDir || rev.SessionUUID != uuid || rev.Origin != planrev.OriginOperator {
		t.Errorf("revision = %+v, want planDir/session/origin set", rev)
	}
	if rev.Reason != "phase 2 hit the wrong endpoint" {
		t.Errorf("reason = %q, want the session's idea (operator reason)", rev.Reason)
	}
	if rev.Summary != `{"title":"T"}` {
		t.Errorf("summary = %q, want the running_plan JSON", rev.Summary)
	}
	if len(rev.Files) != 3 {
		t.Fatalf("files = %d, want 3", len(rev.Files))
	}
	byPath := map[string]planrev.File{}
	for _, f := range rev.Files {
		byPath[f.DocPath] = f
	}
	api := byPath["phase-2-api.md"]
	if api.Action != planrev.ActionUpdate || api.BaseHash != planrev.Sha256Hex(liveAPI) {
		t.Errorf("phase-2 file = %+v, want update with base_hash of the live bytes", api)
	}
	if api.Proposed != phaseDoc("API v2") {
		t.Errorf("phase-2 proposed content mismatch")
	}
	if f := byPath["phase-4-rollout.md"]; f.Action != planrev.ActionCreate || f.BaseHash != "" {
		t.Errorf("phase-4 file = %+v, want create with no base_hash", f)
	}
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Errorf("scratch dir %q not removed after staging", scratch)
	}
	// The live plan dir is untouched — nothing under it is written in this phase.
	if b, _ := os.ReadFile(filepath.Join(planDir, "phase-2-api.md")); string(b) != string(liveAPI) {
		t.Error("staging modified the live plan dir")
	}
}

func TestStage_DiagnosisOriginAndTrigger(t *testing.T) {
	db := testDB(t)
	revisePlanFixture(t, db)
	s := reviseService(t, db)
	scratch := stageScratch(t, s, "uuid-revise", validManifest, validScratchFiles())
	uuid := reviseFixture(t, db, s, StatusProceeding, scratch)
	s.triggers = map[string]int64{uuid: 55}

	s.OnSessionTurns(uuid)

	rev, err := planrev.LatestStaged(db, reviseTaskID)
	if err != nil || rev == nil {
		t.Fatalf("LatestStaged = %+v, %v", rev, err)
	}
	if rev.Origin != planrev.OriginDiagnosis || rev.TriggerPhaseID == nil || *rev.TriggerPhaseID != 55 {
		t.Errorf("revision = origin %q trigger %v, want phase_diagnosis/55", rev.Origin, rev.TriggerPhaseID)
	}
}

func TestStage_RenameAndDelete(t *testing.T) {
	db := testDB(t)
	planDir := revisePlanFixture(t, db)
	s := reviseService(t, db)
	manifest := `{"reason":"r","files":[
		{"path":"phase-2-endpoints.md","action":"rename","renameFrom":"phase-2-api.md"},
		{"path":"phase-3-ui.md","action":"delete"},
		{"path":"README.md","action":"update"}]}`
	readme := "# Test Plan\n\n| # | Phase | Doc | Depends on |\n|---|---|---|---|\n" +
		"| 1 | Store | `phase-1-store.md` | — |\n| 2 | Endpoints | `phase-2-endpoints.md` | 1 |\n"
	scratch := stageScratch(t, s, "uuid-revise", manifest, map[string]string{
		"phase-2-endpoints.md": phaseDoc("Endpoints"),
		"README.md":            readme,
	})
	uuid := reviseFixture(t, db, s, StatusProceeding, scratch)
	liveSrc, _ := os.ReadFile(filepath.Join(planDir, "phase-2-api.md"))

	s.OnSessionTurns(uuid)

	rev, err := planrev.LatestStaged(db, reviseTaskID)
	if err != nil || rev == nil {
		status, _, raw, _ := wizardState(t, db, uuid)
		t.Fatalf("no staged revision (session %q, raw=%+v)", status, raw)
	}
	byPath := map[string]planrev.File{}
	for _, f := range rev.Files {
		byPath[f.DocPath] = f
	}
	ren := byPath["phase-2-endpoints.md"]
	if ren.Action != planrev.ActionRename || ren.RenameFrom != "phase-2-api.md" || ren.BaseHash != planrev.Sha256Hex(liveSrc) {
		t.Errorf("rename file = %+v, want renameFrom + base_hash of the source", ren)
	}
	if del := byPath["phase-3-ui.md"]; del.Action != planrev.ActionDelete || del.Proposed != "" {
		t.Errorf("delete file = %+v, want no proposed content", del)
	}
}

// ── staging: every rejection leaves the session resumable, no rows ──

// rejectCase stages the scratch content and asserts rejection.
func assertRejected(t *testing.T, db *sql.DB, s *Service, uuid, wantErrFragment string) {
	t.Helper()
	s.OnSessionTurns(uuid)
	if n := revisionCount(t, db); n != 0 {
		t.Fatalf("plan_revisions rows = %d, want 0 on a rejected revision", n)
	}
	status, _, raw, _ := wizardState(t, db, uuid)
	if status != StatusAwaiting {
		t.Fatalf("status = %q, want awaiting_answer (resumable)", status)
	}
	if !raw.Valid || !strings.Contains(raw.String, "REVISION REJECTED:") {
		t.Fatalf("raw_reply = %+v, want the rejection surfaced", raw)
	}
	if wantErrFragment != "" && !strings.Contains(raw.String, wantErrFragment) {
		t.Errorf("raw_reply = %q, want fragment %q", raw.String, wantErrFragment)
	}
}

func rejectFixture(t *testing.T, manifest string, files map[string]string) (*sql.DB, *Service, string) {
	t.Helper()
	db := testDB(t)
	revisePlanFixture(t, db)
	s := reviseService(t, db)
	scratch := stageScratch(t, s, "uuid-revise", manifest, files)
	uuid := reviseFixture(t, db, s, StatusProceeding, scratch)
	return db, s, uuid
}

func TestStage_RejectsUnknownAction(t *testing.T) {
	db, s, uuid := rejectFixture(t,
		`{"reason":"r","files":[{"path":"phase-2-api.md","action":"replace"}]}`,
		map[string]string{"phase-2-api.md": phaseDoc("x")})
	assertRejected(t, db, s, uuid, `unknown action "replace"`)
}

func TestStage_RejectsAbsolutePath(t *testing.T) {
	db, s, uuid := rejectFixture(t,
		`{"reason":"r","files":[{"path":"/etc/passwd","action":"update"}]}`, nil)
	assertRejected(t, db, s, uuid, "path is absolute")
}

func TestStage_RejectsDotDotPath(t *testing.T) {
	db, s, uuid := rejectFixture(t,
		`{"reason":"r","files":[{"path":"../README.md","action":"update"}]}`, nil)
	assertRejected(t, db, s, uuid, "escapes the plan dir")
}

func TestStage_RejectsNewStepDoc(t *testing.T) {
	db, s, uuid := rejectFixture(t,
		`{"reason":"r","files":[{"path":"step-01-setup.md","action":"create"}]}`,
		map[string]string{"step-01-setup.md": "# step"})
	assertRejected(t, db, s, uuid, "legacy read-compat only")
}

func TestStage_RejectsDoneDocTarget(t *testing.T) {
	db, s, uuid := rejectFixture(t,
		`{"reason":"r","files":[{"path":"phase-1-store.md","action":"update"}]}`,
		map[string]string{"phase-1-store.md": phaseDoc("Store v2")})
	assertRejected(t, db, s, uuid, "phase is complete")
}

func TestStage_RejectsRunningPhaseTarget(t *testing.T) {
	db, s, uuid := rejectFixture(t,
		`{"reason":"r","files":[{"path":"phase-2-api.md","action":"update"}]}`,
		map[string]string{"phase-2-api.md": phaseDoc("API v2")})
	if _, err := db.Exec(
		`UPDATE epic_phases SET run_state='running' WHERE workspace_task_id=? AND seq=2`, reviseTaskID); err != nil {
		t.Fatal(err)
	}
	assertRejected(t, db, s, uuid, "phase is running")
}

func TestStage_RejectsMissingCompletionReport(t *testing.T) {
	db, s, uuid := rejectFixture(t,
		`{"reason":"r","files":[{"path":"phase-2-api.md","action":"update"}]}`,
		map[string]string{"phase-2-api.md": "# API v2\n\n- [ ] a\n"})
	assertRejected(t, db, s, uuid, "`## Completion Report`")
}

func TestStage_RejectsDanglingDependsOn(t *testing.T) {
	readme := "# Test Plan\n\n| # | Phase | Doc | Depends on |\n|---|---|---|---|\n" +
		"| 1 | Store | `phase-1-store.md` | — |\n| 2 | API | `phase-2-api.md` | 9 |\n" +
		"| 3 | UI | `phase-3-ui.md` | 2 |\n"
	db, s, uuid := rejectFixture(t,
		`{"reason":"r","files":[{"path":"README.md","action":"update"}]}`,
		map[string]string{"README.md": readme})
	assertRejected(t, db, s, uuid, "depends on phase 9")
}

func TestStage_RejectsDocCellNamingAbsentFile(t *testing.T) {
	// Deleting phase-3-ui.md without touching the README leaves the live table
	// naming a file the revision removes.
	db, s, uuid := rejectFixture(t,
		`{"reason":"r","files":[{"path":"phase-3-ui.md","action":"delete"}]}`, nil)
	assertRejected(t, db, s, uuid, "will not exist after this revision")
}

func TestStage_RejectsMissingManifest(t *testing.T) {
	db, s, uuid := rejectFixture(t, "", nil)
	assertRejected(t, db, s, uuid, "revision.json unreadable")
}

func TestStage_RejectsEmptyManifest(t *testing.T) {
	db, s, uuid := rejectFixture(t, `{"reason":"r","files":[]}`, nil)
	assertRejected(t, db, s, uuid, "lists no files")
}

func TestStage_RejectsCreateOverExisting(t *testing.T) {
	db, s, uuid := rejectFixture(t,
		`{"reason":"r","files":[{"path":"phase-2-api.md","action":"create"}]}`,
		map[string]string{"phase-2-api.md": phaseDoc("x")})
	assertRejected(t, db, s, uuid, "already exists")
}

func TestStage_RejectsUpdateOfMissing(t *testing.T) {
	db, s, uuid := rejectFixture(t,
		`{"reason":"r","files":[{"path":"phase-9-ghost.md","action":"update"}]}`,
		map[string]string{"phase-9-ghost.md": phaseDoc("x")})
	assertRejected(t, db, s, uuid, "no such file")
}

func TestStage_RejectionKeepsScratchDir(t *testing.T) {
	db, s, uuid := rejectFixture(t,
		`{"reason":"r","files":[{"path":"phase-2-api.md","action":"replace"}]}`,
		map[string]string{"phase-2-api.md": phaseDoc("x")})
	assertRejected(t, db, s, uuid, "")
	if _, err := os.Stat(filepath.Join(s.ScratchRoot, uuid)); err != nil {
		t.Errorf("scratch dir must survive a rejection so the agent can amend it: %v", err)
	}
}

func TestStage_RejectionIsIdempotentOnReingest(t *testing.T) {
	db, s, uuid := rejectFixture(t,
		`{"reason":"r","files":[{"path":"phase-2-api.md","action":"replace"}]}`,
		map[string]string{"phase-2-api.md": phaseDoc("x")})
	assertRejected(t, db, s, uuid, "")
	var notified int
	s.Notify = func(int64) { notified++ }
	s.OnSessionTurns(uuid) // same turn, same scratch dir → same verdict
	if notified != 0 {
		t.Errorf("Notify fired %d times on an idempotent re-ingest, want 0 (loop guard)", notified)
	}
}

func TestOnReviseTurn_StagedPathOutsideScratchRoot(t *testing.T) {
	db := testDB(t)
	revisePlanFixture(t, db)
	s := reviseService(t, db)
	uuid := reviseFixture(t, db, s, StatusProceeding, "/tmp/not-the-scratch-root/x")
	s.OnSessionTurns(uuid)
	if revisionCount(t, db) != 0 {
		t.Error("a staged path outside the scratch root must not be read")
	}
	status, _, raw, _ := wizardState(t, db, uuid)
	if status != StatusAwaiting || !raw.Valid {
		t.Errorf("status=%q raw=%+v, want the raw fallback", status, raw)
	}
}

// ── mode='plan' isolation ──

func TestPlanModeIgnoresRevisionSentinel(t *testing.T) {
	// A plan-mode wizard whose reply happens to quote REVISION STAGED must go
	// through the unchanged plan-mode path (raw fallback here: no valid PLAN
	// SAVED, no question block).
	db := testDB(t)
	s := reviseService(t, db)
	uuid := "uuid-plan"
	insertWizardRow(t, db, uuid, StatusProceeding)
	insertSessionRow(t, db, 42, uuid)
	insertAssistantTurn(t, db, 42, 1, "quoting REVISION STAGED: /db/revisions/x for fun")

	s.OnSessionTurns(uuid)

	if revisionCount(t, db) != 0 {
		t.Error("plan mode must never stage a revision")
	}
	status, _, raw, _ := wizardState(t, db, uuid)
	if status != StatusAwaiting || !raw.Valid {
		t.Errorf("status=%q raw=%+v, want plan-mode raw fallback", status, raw)
	}
}

// The revise interview reuses the question machinery untouched.
func TestReviseSession_QuestionTurn(t *testing.T) {
	db := testDB(t)
	revisePlanFixture(t, db)
	s := reviseService(t, db)
	uuid := "uuid-revise"
	insertReviseWizard(t, db, uuid, StatusGenerating)
	insertSessionRow(t, db, 42, uuid)
	insertAssistantTurn(t, db, 42, 1, questionTurnText("q-revise-scope"))

	s.OnSessionTurns(uuid)

	status, cq, _, _ := wizardState(t, db, uuid)
	if status != StatusAwaiting || !cq.Valid || !strings.Contains(cq.String, "q-revise-scope") {
		t.Errorf("status=%q cq=%+v, want the interview loop to work in revise mode", status, cq)
	}
}
