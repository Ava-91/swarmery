package phasediag

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
)

// ── git stub ──

// stubGit is a scripted worktree.Git: it records every invocation and returns a
// canned (output, error) per matched arg-prefix, most-recently-registered first
// (the worktree_test.go idiom). UNSCRIPTED verbs fail, so every git signal a
// test relies on has to be stated explicitly — a missing script degrades the
// diagnosis instead of silently inventing a branch.
type stubGit struct {
	calls   []string
	scripts []gitScript
}

type gitScript struct {
	prefix string
	output string
	err    error
}

func (g *stubGit) Run(dir string, args ...string) (string, error) {
	joined := strings.Join(args, " ")
	g.calls = append(g.calls, joined)
	for i := len(g.scripts) - 1; i >= 0; i-- {
		if strings.HasPrefix(joined, g.scripts[i].prefix) {
			return g.scripts[i].output, g.scripts[i].err
		}
	}
	return "", fmt.Errorf("stubGit: unscripted command %q", joined)
}

func (g *stubGit) on(prefix, output string, err error) *stubGit {
	g.scripts = append(g.scripts, gitScript{prefix: prefix, output: output, err: err})
	return g
}

// onBase pre-scripts the base-branch probe every branch-derived blocker needs.
func newGit(base string) *stubGit {
	g := &stubGit{}
	return g.on("symbolic-ref --short HEAD", base+"\n", nil)
}

// branchExists scripts refs/heads/<branch> as present and `ahead` commits past
// base, with the given subjects (newest first).
func (g *stubGit) branchExists(base, branch string, ahead int, subjects ...string) *stubGit {
	g.on("show-ref --verify --quiet refs/heads/"+branch, "", nil)
	g.on(fmt.Sprintf("rev-list --count %s..refs/heads/%s", base, branch), fmt.Sprintf("%d\n", ahead), nil)
	if ahead > 0 {
		g.on(fmt.Sprintf("log --format=%%s --max-count=20 %s..refs/heads/%s", base, branch),
			strings.Join(subjects, "\n")+"\n", nil)
	}
	return g
}

// branchMissing scripts refs/heads/<branch> as absent.
func (g *stubGit) branchMissing(branch string) *stubGit {
	return g.on("show-ref --verify --quiet refs/heads/"+branch, "", errors.New("exit 1"))
}

// ── db fixture ──

type fx struct {
	db      *sql.DB
	taskID  int64
	planDir string
}

// newFixture builds an in-memory-ish store (temp file, real migrations) with
// one project rooted at /repo/p and one workspace epic task to hang phases off.
func newFixture(t *testing.T) *fx {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "phasediag.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	mustExec(t, db, `INSERT INTO projects(id, path, slug, first_seen)
		VALUES(1,'/repo/p','p','2026-01-01T00:00:00Z')`)
	res, err := db.Exec(`INSERT INTO tasks (project_id, title, prompt, status, created_at,
		started_at, source, external_id) VALUES (1,'My Epic','goal','running',
		'2026-07-27T00:00:00Z','2026-07-27T00:00:00Z','workspace','2026-07-27-my-epic')`)
	if err != nil {
		t.Fatalf("insert task: %v", err)
	}
	taskID, _ := res.LastInsertId()
	return &fx{db: db, taskID: taskID, planDir: "/ws/plan"}
}

// clearProjectPath blanks projects.path, the "no filesystem path" case.
func (f *fx) clearProjectPath(t *testing.T) {
	t.Helper()
	mustExec(t, f.db, `UPDATE projects SET path='' WHERE id=1`)
}

func (f *fx) addPhase(t *testing.T, seq int, name, dependsOn string, total, done int) int64 {
	t.Helper()
	doc := filepath.Join(f.planDir, fmt.Sprintf("phase-%d-slug.md", seq))
	res, err := f.db.Exec(`INSERT INTO epic_phases
		(workspace_task_id, seq, name, doc_path, depends_on, checkboxes_total, checkboxes_done)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, f.taskID, seq, name, doc, dependsOn, total, done)
	if err != nil {
		t.Fatalf("insert phase %d: %v", seq, err)
	}
	id, _ := res.LastInsertId()
	return id
}

// setRun stamps the run columns. before < 0 leaves run_checkboxes_before NULL
// (the pre-0041 historical shape).
func (f *fx) setRun(t *testing.T, phaseID int64, state, uuid string, before int) {
	t.Helper()
	var b any
	if before >= 0 {
		b = before
	}
	var u any
	if uuid != "" {
		u = uuid
	}
	mustExec(t, f.db, `UPDATE epic_phases
		SET run_state=?, run_session_uuid=?, run_started_at='2026-07-28T10:00:00Z',
		    run_ended_at='2026-07-28T10:20:00Z', run_checkboxes_before=?
		WHERE id=?`, state, u, b, phaseID)
}

// addAssistantTurns seeds a session and its assistant turns (ascending seq —
// the newest is the last one given).
func (f *fx) addAssistantTurns(t *testing.T, uuid string, texts ...string) {
	t.Helper()
	res, err := f.db.Exec(`INSERT INTO sessions (project_id, session_uuid, status, started_at, source)
		VALUES (1, ?, 'completed', '2026-07-28T10:00:00Z', 'jsonl')`, uuid)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
	sid, _ := res.LastInsertId()
	for i, text := range texts {
		mustExec(t, f.db, `INSERT INTO turns (session_id, seq, role, started_at, text)
			VALUES (?, ?, 'assistant', ?, ?)`,
			sid, i+1, fmt.Sprintf("2026-07-28T10:%02d:00Z", i), text)
	}
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec %s: %v", q, err)
	}
}

// kinds projects a blocker slice to its kinds, in order.
func kinds(bs []Blocker) []string {
	out := make([]string, 0, len(bs))
	for _, b := range bs {
		out = append(out, b.Kind)
	}
	return out
}

func blockerOf(t *testing.T, d Diagnosis, kind string) Blocker {
	t.Helper()
	var found []Blocker
	for _, b := range d.Blockers {
		if b.Kind == kind {
			found = append(found, b)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one %s blocker, got %d (kinds=%v)", kind, len(found), kinds(d.Blockers))
	}
	return found[0]
}

// ── tests ──

// The live incident: run_state='done', 0 of 7 criteria, pre-0041 NULL baseline.
// The chip said "Run done"; the truth is the run achieved nothing.
func TestDiagnoseNoopOnCleanExitWithoutTicks(t *testing.T) {
	f := newFixture(t)
	id := f.addPhase(t, 4, "Phase 4 — Rail", "[]", 7, 0)
	f.setRun(t, id, "done", "", -1) // NULL run_checkboxes_before

	git := newGit("dev").branchMissing(fmt.Sprintf("swarm/phase-%d", id))
	d, err := Diagnose(f.db, git, id)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if d.RunOutcome != OutcomeNoop {
		t.Fatalf("RunOutcome = %q, want %q", d.RunOutcome, OutcomeNoop)
	}
	if d.CriteriaBefore != nil || d.CriteriaAfter != 0 || d.CriteriaTotal != 7 {
		t.Fatalf("criteria = %d/%d (before %v), want 0/7 (before nil — unmeasured)",
			d.CriteriaAfter, d.CriteriaTotal, d.CriteriaBefore)
	}
	if d.PhaseID != id || d.Seq != 4 || d.Name != "Phase 4 — Rail" {
		t.Fatalf("identity = %+v", d)
	}
	if d.RunStartedAt == nil || d.RunEndedAt == nil {
		t.Fatalf("want run timestamps, got started=%v ended=%v", d.RunStartedAt, d.RunEndedAt)
	}
	if len(d.Blockers) != 0 {
		t.Fatalf("want no blockers, got %v", kinds(d.Blockers))
	}
	if d.AgentMessage != nil {
		t.Fatalf("want nil AgentMessage without a session uuid, got %+v", d.AgentMessage)
	}
}

func TestDiagnosePartialAndFailedOutcomes(t *testing.T) {
	f := newFixture(t)
	id := f.addPhase(t, 1, "Phase 1", "[]", 7, 5)
	f.setRun(t, id, "done", "", 3)
	git := newGit("dev").branchMissing(fmt.Sprintf("swarm/phase-%d", id))

	d, err := Diagnose(f.db, git, id)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if d.RunOutcome != OutcomePartial {
		t.Fatalf("RunOutcome = %q, want %q", d.RunOutcome, OutcomePartial)
	}

	mustExec(t, f.db, `UPDATE epic_phases SET run_state='failed', run_error='timeout' WHERE id=?`, id)
	d, err = Diagnose(f.db, git, id)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if d.RunOutcome != OutcomeFailed {
		t.Fatalf("RunOutcome = %q, want %q", d.RunOutcome, OutcomeFailed)
	}
	if d.RunError == nil || *d.RunError != "timeout" {
		t.Fatalf("RunError = %v, want \"timeout\"", d.RunError)
	}
}

func TestDiagnoseDepIncomplete(t *testing.T) {
	f := newFixture(t)
	dep := f.addPhase(t, 2, "Phase 2 — Store", "[]", 8, 3)
	id := f.addPhase(t, 5, "Phase 5 — API", "[2]", 7, 0)
	f.setRun(t, id, "done", "", 0)

	git := newGit("dev").
		branchMissing(fmt.Sprintf("swarm/phase-%d", id)).
		branchMissing(fmt.Sprintf("swarm/phase-%d", dep))

	d, err := Diagnose(f.db, git, id)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	b := blockerOf(t, d, KindDepIncomplete)
	if !strings.Contains(b.Summary, "3/8") || !strings.Contains(b.Summary, "Phase 2") {
		t.Fatalf("summary %q must name Phase 2 and 3/8", b.Summary)
	}
	if !strings.Contains(b.Detail, "phase-2-slug.md") {
		t.Fatalf("detail %q must name the dependency doc", b.Detail)
	}
}

// A dependency with zero criteria cannot prove completion — it is incomplete. The
// derivation is right, but "is only 0/0 complete" would imply a count that came up
// short; nothing was ever countable. Same Kind, different sentence.
func TestDiagnoseDepWithoutCriteriaIsIncomplete(t *testing.T) {
	f := newFixture(t)
	dep := f.addPhase(t, 2, "Phase 2 — Store", "[]", 0, 0)
	id := f.addPhase(t, 3, "Phase 3", "[2]", 4, 4)

	git := newGit("dev").
		branchMissing(fmt.Sprintf("swarm/phase-%d", id)).
		branchMissing(fmt.Sprintf("swarm/phase-%d", dep))

	d, err := Diagnose(f.db, git, id)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if got := kinds(d.Blockers); len(got) != 1 || got[0] != KindDepIncomplete {
		t.Fatalf("kinds = %v, want [%s]", got, KindDepIncomplete)
	}
	b := blockerOf(t, d, KindDepIncomplete)
	if strings.Contains(b.Summary, "0/0") {
		t.Errorf("summary %q reports a 0/0 count; the dependency has no criteria to count", b.Summary)
	}
	if !strings.Contains(b.Summary, "Phase 2") || !strings.Contains(b.Summary, "acceptance-criteria") {
		t.Errorf("summary %q must name Phase 2 and say it has no acceptance-criteria checkboxes", b.Summary)
	}
	if !strings.Contains(b.Detail, "phase-2-slug.md") {
		t.Errorf("detail %q must name the dependency doc", b.Detail)
	}
}

// TestDiagnoseStampedAfterWinsOverLiveCount: run_checkboxes_after (0042) closes the
// measurement interval at exit. Later writers of checkboxes_done — the wsingest
// rescan, TickPhaseChecklist — must not be able to inflate what the run achieved.
func TestDiagnoseStampedAfterWinsOverLiveCount(t *testing.T) {
	f := newFixture(t)
	id := f.addPhase(t, 1, "Phase 1", "[]", 7, 7) // live count has since reached 7/7
	f.setRun(t, id, "done", "", 1)
	mustExec(t, f.db, `UPDATE epic_phases SET run_checkboxes_after=2 WHERE id=?`, id)
	git := newGit("dev").branchMissing(fmt.Sprintf("swarm/phase-%d", id))

	d, err := Diagnose(f.db, git, id)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if d.CriteriaAfter != 2 {
		t.Errorf("CriteriaAfter = %d, want the stamped 2 — not the live 7", d.CriteriaAfter)
	}
	if d.RunOutcome != OutcomePartial {
		t.Errorf("RunOutcome = %q, want %q (1 → 2 of 7)", d.RunOutcome, OutcomePartial)
	}
}

// TestDiagnoseNullBaselineIsNeverPartial: a NULL run_checkboxes_before means the
// run was never measured. Reading it as 0 would derive the phase's whole ticked
// count as this run's delta and claim a 'partial' success nobody measured.
func TestDiagnoseNullBaselineIsNeverPartial(t *testing.T) {
	f := newFixture(t)
	id := f.addPhase(t, 1, "Phase 1", "[]", 7, 3) // 3 of 7 ticked, provenance unknown
	f.setRun(t, id, "done", "", -1)               // NULL baseline
	git := newGit("dev").branchMissing(fmt.Sprintf("swarm/phase-%d", id))

	d, err := Diagnose(f.db, git, id)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if d.RunOutcome != OutcomeNoop {
		t.Errorf("RunOutcome = %q, want %q — an unmeasured run cannot claim progress",
			d.RunOutcome, OutcomeNoop)
	}
	if d.CriteriaAfter != 3 || d.CriteriaTotal != 7 {
		t.Errorf("criteria = %d/%d, want 3/7", d.CriteriaAfter, d.CriteriaTotal)
	}

	// The same NULL baseline on a fully ticked phase still reads 'completed' —
	// understating progress, never overstating it.
	mustExec(t, f.db, `UPDATE epic_phases SET checkboxes_done=7 WHERE id=?`, id)
	d, err = Diagnose(f.db, git, id)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if d.RunOutcome != OutcomeCompleted {
		t.Errorf("RunOutcome = %q, want %q on a fully ticked phase", d.RunOutcome, OutcomeCompleted)
	}
}

// TestDiagnoseCriteriaBeforeDistinguishesUnmeasured: the wire format must be able
// to say "not measured". A non-pointer CriteriaBefore serialised a NULL baseline as
// `"criteriaBefore": 0`, so a UI chip rendered "0 → 3" next to a 'noop' outcome —
// a fabricated measurement the Go-side policy exists precisely to refuse.
func TestDiagnoseCriteriaBeforeDistinguishesUnmeasured(t *testing.T) {
	f := newFixture(t)
	git := newGit("dev")

	unmeasured := f.addPhase(t, 1, "Phase 1", "[]", 7, 3)
	f.setRun(t, unmeasured, "done", "", -1) // NULL run_checkboxes_before
	git.branchMissing(fmt.Sprintf("swarm/phase-%d", unmeasured))

	d, err := Diagnose(f.db, git, unmeasured)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if d.CriteriaBefore != nil {
		t.Errorf("CriteriaBefore = %d, want nil — a NULL baseline is unmeasured, not zero", *d.CriteriaBefore)
	}
	if body, err := json.Marshal(d); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(string(body), `"criteriaBefore":null`) {
		t.Errorf("JSON = %s, want criteriaBefore null", body)
	}

	// A genuinely measured zero baseline still serialises as 0 — the two cases
	// must stay distinguishable on the wire.
	measured := f.addPhase(t, 2, "Phase 2", "[]", 7, 3)
	f.setRun(t, measured, "done", "", 0)
	git.branchMissing(fmt.Sprintf("swarm/phase-%d", measured))

	d, err = Diagnose(f.db, git, measured)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if d.CriteriaBefore == nil || *d.CriteriaBefore != 0 {
		t.Fatalf("CriteriaBefore = %v, want a measured 0", d.CriteriaBefore)
	}
	if d.RunOutcome != OutcomePartial {
		t.Errorf("RunOutcome = %q, want %q (measured 0 → 3)", d.RunOutcome, OutcomePartial)
	}
}

// The incident's real cause: the dependency IS ticked, but its code sits on an
// unmerged branch, so the executor correctly refused to build on top of it.
func TestDiagnoseDepUnmerged(t *testing.T) {
	f := newFixture(t)
	dep := f.addPhase(t, 1, "Phase 1 — Schema", "[]", 8, 8)
	id := f.addPhase(t, 4, "Phase 4 — Rail", "[1]", 7, 0)
	f.setRun(t, id, "done", "", 0)

	depBranch := fmt.Sprintf("swarm/phase-%d", dep)
	git := newGit("dev").
		branchMissing(fmt.Sprintf("swarm/phase-%d", id)).
		branchExists("dev", depBranch, 2, "feat: store", "test: store")

	d, err := Diagnose(f.db, git, id)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	b := blockerOf(t, d, KindDepUnmerged)
	for _, want := range []string{"Phase 1", depBranch, "2 commits", "dev"} {
		if !strings.Contains(b.Summary, want) {
			t.Fatalf("summary %q must contain %q", b.Summary, want)
		}
	}
	// The base is stated as what it actually is — the repo's CURRENT checkout —
	// because that is what baseBranch measures against (matching worktree.Acquire).
	// On a feature branch a dependency merged into dev but not into that branch
	// fires this blocker; without the qualifier the user cannot tell that skew from
	// a genuinely unmerged dependency.
	if !strings.Contains(b.Detail, depBranch) || !strings.Contains(b.Detail, "2 commits") {
		t.Fatalf("detail %q must name the branch and count", b.Detail)
	}
	if !strings.Contains(b.Detail, "measured against the currently checked-out branch dev") {
		t.Fatalf("detail %q must name what it compared against", b.Detail)
	}
}

// Ticked AND merged: nothing to say about the dependency at all.
func TestDiagnoseDepMergedIsSilent(t *testing.T) {
	f := newFixture(t)
	dep := f.addPhase(t, 1, "Phase 1 — Schema", "[]", 8, 8)
	id := f.addPhase(t, 4, "Phase 4 — Rail", "[1]", 7, 2)

	git := newGit("dev").
		branchMissing(fmt.Sprintf("swarm/phase-%d", id)).
		branchExists("dev", fmt.Sprintf("swarm/phase-%d", dep), 0)

	d, err := Diagnose(f.db, git, id)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if len(d.Blockers) != 0 {
		t.Fatalf("want no blockers, got %v", kinds(d.Blockers))
	}
}

// A dependency whose branch was never created (already merged and deleted, or
// the work landed straight on base) is equally silent.
func TestDiagnoseDepTickedWithoutBranchIsSilent(t *testing.T) {
	f := newFixture(t)
	dep := f.addPhase(t, 1, "Phase 1", "[]", 3, 3)
	id := f.addPhase(t, 2, "Phase 2", "[1]", 5, 1)

	git := newGit("dev").
		branchMissing(fmt.Sprintf("swarm/phase-%d", id)).
		branchMissing(fmt.Sprintf("swarm/phase-%d", dep))

	d, err := Diagnose(f.db, git, id)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if len(d.Blockers) != 0 {
		t.Fatalf("want no blockers, got %v", kinds(d.Blockers))
	}
}

func TestDiagnoseOwnBranchStatesAreMutuallyExclusive(t *testing.T) {
	f := newFixture(t)
	id := f.addPhase(t, 4, "Phase 4", "[]", 7, 1)
	branch := fmt.Sprintf("swarm/phase-%d", id)

	// Leftover branch, nothing on it.
	leftover := newGit("dev").branchExists("dev", branch, 0)
	d, err := Diagnose(f.db, leftover, id)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if got := kinds(d.Blockers); len(got) != 1 || got[0] != KindBranchBlocksRetry {
		t.Fatalf("kinds = %v, want [%s]", got, KindBranchBlocksRetry)
	}
	b := blockerOf(t, d, KindBranchBlocksRetry)
	if !strings.Contains(b.Summary, branch) {
		t.Fatalf("summary %q must name the branch", b.Summary)
	}
	if !strings.Contains(b.Detail, "0 commits") ||
		!strings.Contains(b.Detail, "measured against the currently checked-out branch dev") {
		t.Fatalf("detail %q must state 0 commits and the base it compared against", b.Detail)
	}

	// Same branch, three commits on it.
	subjects := []string{"fix: rail width", "feat: rail panel", "test: rail"}
	dirty := newGit("dev").branchExists("dev", branch, 3, subjects...)
	d, err = Diagnose(f.db, dirty, id)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if got := kinds(d.Blockers); len(got) != 1 || got[0] != KindBranchDirty {
		t.Fatalf("kinds = %v, want [%s]", got, KindBranchDirty)
	}
	b = blockerOf(t, d, KindBranchDirty)
	if !strings.Contains(b.Summary, "3 commits") || !strings.Contains(b.Summary, branch) {
		t.Fatalf("summary %q must name the branch and commit count", b.Summary)
	}
	for _, s := range subjects {
		if !strings.Contains(b.Detail, s) {
			t.Fatalf("detail %q must contain subject %q", b.Detail, s)
		}
	}
	lines := strings.Split(strings.TrimSpace(b.Detail), "\n")
	if lines[0] != subjects[0] {
		t.Fatalf("detail must list newest commit first, got %q", lines[0])
	}
	// The comparison base is appended AFTER the subjects, so the newest-commit-first
	// reading order survives while the count still says what it is relative to.
	if last := lines[len(lines)-1]; last != "measured against the currently checked-out branch dev" {
		t.Fatalf("detail must end by naming the base it compared against, got %q", last)
	}
}

func TestDiagnoseNoCriteria(t *testing.T) {
	f := newFixture(t)
	id := f.addPhase(t, 6, "Phase 6", "[]", 0, 0)
	git := newGit("dev").branchMissing(fmt.Sprintf("swarm/phase-%d", id))

	d, err := Diagnose(f.db, git, id)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	b := blockerOf(t, d, KindNoCriteria)
	if !strings.Contains(b.Detail, "phase-6-slug.md") {
		t.Fatalf("detail %q must name the phase doc path", b.Detail)
	}
}

// Detached HEAD (or any symbolic-ref failure): branch-derived blockers are
// omitted rather than guessed, and the diagnosis still answers everything else.
func TestDiagnoseDetachedHeadSkipsBranchBlockers(t *testing.T) {
	f := newFixture(t)
	dep := f.addPhase(t, 1, "Phase 1", "[]", 8, 8)
	id := f.addPhase(t, 2, "Phase 2", "[1]", 0, 0)

	git := &stubGit{}
	git.on("symbolic-ref --short HEAD", "", errors.New("fatal: ref HEAD is not a symbolic ref"))
	git.branchExists("dev", fmt.Sprintf("swarm/phase-%d", id), 3, "feat: x")
	git.branchExists("dev", fmt.Sprintf("swarm/phase-%d", dep), 2, "feat: y")

	d, err := Diagnose(f.db, git, id)
	if err != nil {
		t.Fatalf("Diagnose must not fail on detached HEAD: %v", err)
	}
	if got := kinds(d.Blockers); len(got) != 1 || got[0] != KindNoCriteria {
		t.Fatalf("kinds = %v, want only [%s]", got, KindNoCriteria)
	}
}

func TestDiagnoseWithoutGitOrProjectPath(t *testing.T) {
	f := newFixture(t)
	id := f.addPhase(t, 1, "Phase 1", "[]", 0, 0)

	d, err := Diagnose(f.db, nil, id)
	if err != nil {
		t.Fatalf("Diagnose with nil git: %v", err)
	}
	if got := kinds(d.Blockers); len(got) != 1 || got[0] != KindNoCriteria {
		t.Fatalf("nil git: kinds = %v, want only [%s]", got, KindNoCriteria)
	}

	f.clearProjectPath(t)
	git := newGit("dev").branchExists("dev", fmt.Sprintf("swarm/phase-%d", id), 4, "feat: x")
	d, err = Diagnose(f.db, git, id)
	if err != nil {
		t.Fatalf("Diagnose without project path: %v", err)
	}
	if got := kinds(d.Blockers); len(got) != 1 || got[0] != KindNoCriteria {
		t.Fatalf("no path: kinds = %v, want only [%s]", got, KindNoCriteria)
	}
	if len(git.calls) != 0 {
		t.Fatalf("no project path must mean no git calls, got %v", git.calls)
	}
}

// A git that is broken in every direction must degrade, never fail the request.
func TestDiagnoseGitErrorsDegrade(t *testing.T) {
	f := newFixture(t)
	id := f.addPhase(t, 1, "Phase 1", "[]", 4, 0)

	git := newGit("dev") // show-ref/rev-list unscripted ⇒ error
	d, err := Diagnose(f.db, git, id)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if len(d.Blockers) != 0 {
		t.Fatalf("want no blockers when git cannot answer, got %v", kinds(d.Blockers))
	}

	// rev-list garbage is equally non-fatal.
	git2 := newGit("dev").
		on("show-ref --verify --quiet refs/heads/", "", nil).
		on("rev-list --count", "not-a-number\n", nil)
	d, err = Diagnose(f.db, git2, id)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if len(d.Blockers) != 0 {
		t.Fatalf("want no blockers on unparseable rev-list, got %v", kinds(d.Blockers))
	}
}

func TestDiagnoseAgentMessage(t *testing.T) {
	f := newFixture(t)
	id := f.addPhase(t, 1, "Phase 1", "[]", 4, 0)
	f.setRun(t, id, "done", "sess-1", 0)
	f.addAssistantTurns(t, "sess-1", "first thought", "I stopped: dependency code is unmerged.")

	git := newGit("dev").branchMissing(fmt.Sprintf("swarm/phase-%d", id))
	d, err := Diagnose(f.db, git, id)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if d.AgentMessage == nil {
		t.Fatal("want an AgentMessage")
	}
	if d.AgentMessage.SessionUUID != "sess-1" {
		t.Fatalf("SessionUUID = %q", d.AgentMessage.SessionUUID)
	}
	if d.AgentMessage.Text != "I stopped: dependency code is unmerged." {
		t.Fatalf("want the NEWEST assistant turn, got %q", d.AgentMessage.Text)
	}
	if d.AgentMessage.Truncated {
		t.Fatal("short text must not be marked truncated")
	}
}

// The excerpt is capped in RUNES: a byte-wise cut would split a multi-byte rune
// and render as a replacement char in the modal.
func TestDiagnoseAgentMessageTruncatesOnRuneBoundary(t *testing.T) {
	f := newFixture(t)
	id := f.addPhase(t, 1, "Phase 1", "[]", 4, 0)
	f.setRun(t, id, "done", "sess-2", 0)
	long := strings.Repeat("є", maxAgentText+50) // 2-byte runes
	f.addAssistantTurns(t, "sess-2", long)

	git := newGit("dev").branchMissing(fmt.Sprintf("swarm/phase-%d", id))
	d, err := Diagnose(f.db, git, id)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if d.AgentMessage == nil {
		t.Fatal("want an AgentMessage")
	}
	if !d.AgentMessage.Truncated {
		t.Fatal("want Truncated=true")
	}
	if n := len([]rune(d.AgentMessage.Text)); n != maxAgentText {
		t.Fatalf("text = %d runes, want %d", n, maxAgentText)
	}
	if !strings.ContainsRune(d.AgentMessage.Text, 'є') || strings.ContainsRune(d.AgentMessage.Text, '�') {
		t.Fatalf("text was cut mid-rune: %q", d.AgentMessage.Text[:20])
	}
}

func TestDiagnoseAgentMessageMissing(t *testing.T) {
	f := newFixture(t)
	id := f.addPhase(t, 1, "Phase 1", "[]", 4, 0)
	f.setRun(t, id, "done", "sess-nope", 0) // uuid set, nothing ingested
	git := newGit("dev").branchMissing(fmt.Sprintf("swarm/phase-%d", id))

	d, err := Diagnose(f.db, git, id)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if d.AgentMessage != nil {
		t.Fatalf("want nil AgentMessage with no ingested turn, got %+v", d.AgentMessage)
	}
}

func TestDiagnosePhaseNotFound(t *testing.T) {
	f := newFixture(t)
	if _, err := Diagnose(f.db, newGit("dev"), 9999); !errors.Is(err, ErrPhaseNotFound) {
		t.Fatalf("err = %v, want ErrPhaseNotFound", err)
	}
}

// The UI renders blockers top-down as "most actionable first"; the order is a
// contract, not an accident of map iteration.
func TestDiagnoseBlockerOrder(t *testing.T) {
	f := newFixture(t)
	depA := f.addPhase(t, 1, "Phase 1 — Schema", "[]", 8, 8) // ticked but unmerged
	depB := f.addPhase(t, 2, "Phase 2 — Store", "[]", 8, 3)  // incomplete
	id := f.addPhase(t, 3, "Phase 3 — API", "[2,1]", 0, 0)   // no criteria of its own

	git := newGit("dev").
		branchExists("dev", fmt.Sprintf("swarm/phase-%d", depA), 2, "feat: schema").
		branchMissing(fmt.Sprintf("swarm/phase-%d", depB)).
		branchExists("dev", fmt.Sprintf("swarm/phase-%d", id), 3, "wip: api", "wip: api 2", "wip: api 3")

	d, err := Diagnose(f.db, git, id)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	want := []string{KindDepIncomplete, KindDepUnmerged, KindBranchDirty, KindNoCriteria}
	if got := kinds(d.Blockers); !equal(got, want) {
		t.Fatalf("blocker order = %v, want %v", got, want)
	}
}

// Dependencies are visited in ascending seq order regardless of the order the
// depends_on list happens to carry.
func TestDiagnoseDepsSortedBySeq(t *testing.T) {
	f := newFixture(t)
	d1 := f.addPhase(t, 1, "Phase 1", "[]", 4, 1)
	d2 := f.addPhase(t, 2, "Phase 2", "[]", 4, 2)
	id := f.addPhase(t, 3, "Phase 3", "[2,1]", 4, 0)

	git := newGit("dev").
		branchMissing(fmt.Sprintf("swarm/phase-%d", id)).
		branchMissing(fmt.Sprintf("swarm/phase-%d", d1)).
		branchMissing(fmt.Sprintf("swarm/phase-%d", d2))

	d, err := Diagnose(f.db, git, id)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if len(d.Blockers) != 2 {
		t.Fatalf("want 2 dep blockers, got %v", kinds(d.Blockers))
	}
	if !strings.Contains(d.Blockers[0].Summary, "Phase 1") ||
		!strings.Contains(d.Blockers[1].Summary, "Phase 2") {
		t.Fatalf("deps not in ascending seq order: %q then %q",
			d.Blockers[0].Summary, d.Blockers[1].Summary)
	}
}

// Garbage in depends_on must not gate anything (epics.go decodeIntList posture),
// and a dependency seq with no sibling row is simply unknowable, not a blocker.
func TestDiagnoseUnknownAndGarbageDeps(t *testing.T) {
	f := newFixture(t)
	id := f.addPhase(t, 3, "Phase 3", "[9]", 4, 1)
	git := newGit("dev").branchMissing(fmt.Sprintf("swarm/phase-%d", id))
	d, err := Diagnose(f.db, git, id)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if len(d.Blockers) != 0 {
		t.Fatalf("unknown dep seq: want no blockers, got %v", kinds(d.Blockers))
	}

	mustExec(t, f.db, `UPDATE epic_phases SET depends_on='not json' WHERE id=?`, id)
	d, err = Diagnose(f.db, git, id)
	if err != nil {
		t.Fatalf("Diagnose with garbage depends_on: %v", err)
	}
	if len(d.Blockers) != 0 {
		t.Fatalf("garbage deps: want no blockers, got %v", kinds(d.Blockers))
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
