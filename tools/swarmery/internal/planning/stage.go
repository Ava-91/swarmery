package planning

// Staging ingest for revise-mode wizard sessions. The revise agent writes its
// proposal (full files + revision.json manifest) into a per-session scratch
// dir and ends with "REVISION STAGED: <dir>"; Stage validates the proposal
// against the LIVE plan and turns it into plan_revisions +
// plan_revision_files rows. Nothing under the plan dir is written here — the
// rows ARE the proposal, and Apply is a later, separate operator decision.
//
// A validation failure is an interview turn, not a terminal state: the session
// falls back to awaiting_answer with the precise error appended to raw_reply,
// so the operator can resume the session and tell the agent what to fix. The
// scratch dir is kept on failure (the agent amends it) and removed on success.

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/planrev"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/wsingest"
)

// revisionManifest is the revision.json contract the revise prompt mandates.
type revisionManifest struct {
	Reason  string          `json:"reason"`
	Summary json.RawMessage `json:"summary"`
	Files   []manifestFile  `json:"files"`
}

type manifestFile struct {
	Path       string `json:"path"`
	Action     string `json:"action"`
	RenameFrom string `json:"renameFrom,omitempty"`
}

// docNameRe is the closed set of doc names a revision may touch: the README
// and phase docs, both plan-dir-root only (no subdirs). step-*.md is
// deliberately absent — legacy read-compat only, never for new plans.
var docNameRe = regexp.MustCompile(`^(?:README\.md|phase-[^/]+\.md)$`)

// completionReportRe matches wsingest's completionHeadingRe — the exact
// heading the dashboard's Summary tab parses out of a phase doc.
var completionReportRe = regexp.MustCompile(`(?mi)^##\s+Completion Report\s*$`)

// onReviseTurn advances a revise-mode wizard for the newest assistant turn:
// the revise sentinels first, then the shared interview machinery (question
// parse / raw fallback), which is reused untouched.
func (s *Service) onReviseTurn(row *wizardRow, text string) {
	// A revise session minting a plan is a protocol violation: never mark done,
	// surface the reply via the raw fallback so the operator can correct it.
	if strings.Contains(text, planSavedMarker) {
		log.Printf("warn: planning: revise uuid=%s emitted %q — protocol violation, not marking done", row.uuid, planSavedMarker)
		s.applyRawTurn(row, text)
		return
	}
	if strings.Contains(text, revisionStagedMarker) {
		dir := filepath.Clean(extractStagedDir(text))
		root := filepath.Clean(s.scratchRoot())
		if strings.HasPrefix(dir+string(os.PathSeparator), root+string(os.PathSeparator)) {
			s.Stage(row, dir)
			return
		}
		// A staged path outside the scratch root is never read — it could name
		// anything on disk. Raw fallback, same as an off-convention PLAN SAVED.
		log.Printf("warn: planning: revise uuid=%s REVISION STAGED path %q is outside the scratch root %q — not staging", row.uuid, dir, root)
		s.applyRawTurn(row, text)
		return
	}
	pt := ParseTurn(text)
	if pt.Question != nil {
		s.applyQuestionTurn(row, pt)
		return
	}
	s.applyRawTurn(row, text)
}

// Stage validates the scratch dir's proposal and inserts the plan_revisions +
// plan_revision_files rows. Success removes the scratch dir and CAS-moves the
// session to done; any validation failure leaves the session resumable in
// awaiting_answer with the error appended to raw_reply and inserts nothing.
func (s *Service) Stage(row *wizardRow, scratchDir string) {
	if err := s.stage(row, scratchDir); err != nil {
		s.stageFail(row, err)
	}
}

func (s *Service) stage(row *wizardRow, scratchDir string) error {
	if !row.reviseTaskID.Valid {
		return errors.New("revise session carries no revise_task_id")
	}
	taskID := row.reviseTaskID.Int64

	// Plan dir resolved at staging time (the task may have moved zones since
	// the session started) — same SELECT as StartRevise / api.resolveEpicDirs.
	var planDir string
	if err := s.DB.QueryRow(
		`SELECT path FROM task_artifacts WHERE task_id = ? AND kind = 'plan'`,
		taskID).Scan(&planDir); err != nil {
		return fmt.Errorf("resolve plan dir of task %d: %w", taskID, err)
	}

	// 1. The manifest is the proposal's spine — absent/unreadable ⇒ reject.
	rawManifest, err := os.ReadFile(filepath.Join(scratchDir, "revision.json"))
	if err != nil {
		return fmt.Errorf("revision.json unreadable: %w", err)
	}
	var m revisionManifest
	if err := json.Unmarshal(rawManifest, &m); err != nil {
		return fmt.Errorf("revision.json invalid: %w", err)
	}
	if len(m.Files) == 0 {
		return errors.New("revision.json lists no files — an empty revision has nothing to review")
	}

	// Immutable + untouchable docs from the LIVE rows (a phase may have started
	// running, or finished, during the interview).
	_, doneDocs, err := BuildEvidence(s.DB, taskID)
	if err != nil {
		return err
	}
	doneSet := make(map[string]bool, len(doneDocs))
	for _, d := range doneDocs {
		doneSet[d] = true
	}
	runningSet, err := s.runningDocs(taskID)
	if err != nil {
		return err
	}

	// 2-5. Per-entry validation + content/hash collection.
	files := make([]planrev.File, 0, len(m.Files))
	for _, f := range m.Files {
		pf, err := s.stageFile(planDir, scratchDir, f, doneSet, runningSet)
		if err != nil {
			return err
		}
		files = append(files, pf)
	}

	// 6. The post-revision plan must still be internally consistent: every Doc
	// cell of the (proposed or live) README table resolves to a file that will
	// exist, and every dependency names a phase in the table.
	if err := validatePlanTable(planDir, m.Files, files); err != nil {
		return err
	}

	// 7. Stage the rows. Origin: a session started from a phase diagnosis
	// carries its trigger phase; a plain operator revise does not.
	origin := planrev.OriginOperator
	var trigger *int64
	s.mu.Lock()
	if id, ok := s.triggers[row.uuid]; ok {
		origin = planrev.OriginDiagnosis
		trigger = &id
		delete(s.triggers, row.uuid)
	}
	s.mu.Unlock()

	reason := s.sessionIdea(row.id)
	if reason == "" {
		reason = m.Reason
	}
	rev := planrev.Revision{
		WorkspaceTaskID: taskID,
		PlanDir:         planDir,
		SessionUUID:     row.uuid,
		Origin:          origin,
		TriggerPhaseID:  trigger,
		Reason:          reason,
		Summary:         row.runningPlan.String,
		CreatedAt:       s.ts(),
	}
	revID, err := planrev.Insert(s.DB, rev, files)
	if err != nil {
		return err
	}

	if err := os.RemoveAll(scratchDir); err != nil {
		log.Printf("warn: planning: revise uuid=%s staged but scratch dir %q not removed: %v", row.uuid, scratchDir, err)
	}
	// CAS: only the open statuses may complete — a concurrent Cancel wins (the
	// staged revision stays reviewable either way).
	res, uerr := s.DB.Exec(
		`UPDATE planning_sessions SET status=?, updated_at=? WHERE id=? AND status IN (?, ?, ?)`,
		StatusDone, s.ts(), row.id, StatusGenerating, StatusProceeding, StatusAwaiting)
	if uerr != nil {
		log.Printf("error: planning: revise uuid=%s mark done: %v", row.uuid, uerr)
		return nil // the revision IS staged — never turn that into a validation failure
	}
	if n, _ := res.RowsAffected(); n == 0 {
		log.Printf("warn: planning: revise uuid=%s staged revision %d but the session was already terminal", row.uuid, revID)
		return nil
	}
	log.Printf("planning: revise uuid=%s staged revision %d (%d files) for task %d", row.uuid, revID, len(files), taskID)
	s.notify(row.projectID)
	return nil
}

// stageFile validates ONE manifest entry against the live plan dir and builds
// its planrev.File (proposed content + base_hash of the live bytes).
func (s *Service) stageFile(planDir, scratchDir string, f manifestFile, doneSet, runningSet map[string]bool) (planrev.File, error) {
	var zero planrev.File
	switch f.Action {
	case planrev.ActionCreate, planrev.ActionUpdate, planrev.ActionRename, planrev.ActionDelete:
	default:
		return zero, fmt.Errorf("file %q: unknown action %q", f.Path, f.Action)
	}
	if err := validateDocName(f.Path); err != nil {
		return zero, err
	}
	if f.Action == planrev.ActionRename {
		if err := validateDocName(f.RenameFrom); err != nil {
			return zero, err
		}
	}
	if f.Path == "README.md" && (f.Action == planrev.ActionDelete || f.Action == planrev.ActionRename) {
		return zero, fmt.Errorf("README.md may only be created or updated, not %sd — the plan needs its README", f.Action)
	}

	// Done docs are immutable; a running phase's doc is being edited by its
	// executor right now. Both the target and a rename source count as touching.
	for _, p := range []string{f.Path, f.RenameFrom} {
		if p == "" {
			continue
		}
		if doneSet[p] {
			return zero, fmt.Errorf("file %q: phase is complete — done phase docs must not be changed, renamed, or deleted", p)
		}
		if runningSet[p] {
			return zero, fmt.Errorf("file %q: its phase is running — revise after the run ends", p)
		}
	}

	// Existence rules against the LIVE plan dir: create must be new;
	// update/delete need their target, rename needs its source.
	liveTarget := filepath.Join(planDir, f.Path)
	switch f.Action {
	case planrev.ActionCreate:
		if _, err := os.Stat(liveTarget); err == nil {
			return zero, fmt.Errorf("create %q: the file already exists in the plan — use action update", f.Path)
		}
	case planrev.ActionUpdate, planrev.ActionDelete:
		if _, err := os.Stat(liveTarget); err != nil {
			return zero, fmt.Errorf("%s %q: no such file in the plan", f.Action, f.Path)
		}
	case planrev.ActionRename:
		if _, err := os.Stat(filepath.Join(planDir, f.RenameFrom)); err != nil {
			return zero, fmt.Errorf("rename %q → %q: no such source file in the plan", f.RenameFrom, f.Path)
		}
	}

	pf := planrev.File{DocPath: f.Path, Action: f.Action, RenameFrom: f.RenameFrom}

	// base_hash pins the live bytes the proposal was staged against (the file
	// this revision replaces/removes): the target for update/delete, the
	// rename source for rename, nothing for create.
	baseFile := ""
	switch f.Action {
	case planrev.ActionUpdate, planrev.ActionDelete:
		baseFile = liveTarget
	case planrev.ActionRename:
		baseFile = filepath.Join(planDir, f.RenameFrom)
	}
	if baseFile != "" {
		b, err := os.ReadFile(baseFile)
		if err != nil {
			return zero, fmt.Errorf("%s %q: read live file: %w", f.Action, f.Path, err)
		}
		pf.BaseHash = planrev.Sha256Hex(b)
	}

	// Proposed content: written whole into the scratch dir under the TARGET
	// name (a delete has no content file).
	if f.Action != planrev.ActionDelete {
		b, err := os.ReadFile(filepath.Join(scratchDir, f.Path))
		if err != nil {
			return zero, fmt.Errorf("%s %q: proposed content missing from the scratch dir: %w", f.Action, f.Path, err)
		}
		pf.Proposed = string(b)
		// 5. The dashboard renders exactly the `## Completion Report` section as
		// a phase's summary — a proposed phase doc without the heading would
		// show "no summary of the work written" over work that shipped.
		if strings.HasPrefix(f.Path, "phase-") && !completionReportRe.MatchString(pf.Proposed) {
			return zero, fmt.Errorf("%s %q: the proposed doc has no `## Completion Report` section — every phase doc must keep it as its last section", f.Action, f.Path)
		}
	}
	return pf, nil
}

// validateDocName enforces the closed doc-name set on a plan-dir-relative
// path, with a dedicated message for the legacy step-*.md shape.
func validateDocName(p string) error {
	if p == "" {
		return errors.New("manifest entry with an empty path")
	}
	if filepath.IsAbs(p) {
		return fmt.Errorf("file %q: path is absolute; use the doc's plan-dir-relative name", p)
	}
	for _, seg := range strings.Split(filepath.ToSlash(p), "/") {
		if seg == ".." {
			return fmt.Errorf("file %q: path escapes the plan dir", p)
		}
	}
	if strings.HasPrefix(p, "step-") {
		return fmt.Errorf("file %q: step-*.md docs are legacy read-compat only — a phase's steps are its acceptance-criteria checkboxes", p)
	}
	if !docNameRe.MatchString(p) {
		return fmt.Errorf("file %q: only README.md and phase-*.md at the plan root may be revised", p)
	}
	return nil
}

// validatePlanTable checks the post-revision plan's structural consistency:
// build the doc set that will exist after applying the manifest, parse the
// phase-sequencing table of the README that will be live then (the proposed
// one when the revision carries it, the on-disk one otherwise) with THE
// scanner's parser, and require every Doc cell to resolve and every
// dependency to name a table row. A README with no recognizable table is
// tolerated, exactly as the scanner tolerates it (one-phase-per-doc fallback).
func validatePlanTable(planDir string, manifest []manifestFile, files []planrev.File) error {
	post := map[string]bool{}
	for _, name := range listPlanDocNames(planDir) {
		post[name] = true
	}
	post["README.md"] = fileExists(filepath.Join(planDir, "README.md"))
	var readme string
	readmeProposed := false
	for i, f := range manifest {
		switch f.Action {
		case planrev.ActionDelete:
			delete(post, f.Path)
		case planrev.ActionRename:
			delete(post, f.RenameFrom)
			post[f.Path] = true
		default:
			post[f.Path] = true
		}
		if f.Path == "README.md" && f.Action != planrev.ActionDelete {
			readme = files[i].Proposed
			readmeProposed = true
		}
	}
	if !readmeProposed {
		b, err := os.ReadFile(filepath.Join(planDir, "README.md"))
		if err != nil {
			return nil // no README to validate against — the scanner's fallback shape
		}
		readme = string(b)
	}

	phases, err := wsingest.ParsePlanTable(readme)
	if errors.Is(err, wsingest.ErrNoPlanTable) {
		return nil
	}
	if err != nil {
		return err
	}
	seqs := make(map[int]bool, len(phases))
	for _, p := range phases {
		seqs[p.Seq] = true
	}
	for _, p := range phases {
		if !post[p.Doc] {
			return fmt.Errorf("README phase table names `%s`, which will not exist after this revision — every Doc cell must name a file the revision leaves in place or creates", p.Doc)
		}
		for _, dep := range p.DependsOn {
			if !seqs[dep] {
				return fmt.Errorf("README phase table: phase %d depends on phase %d, which is not a row of the table", p.Seq, dep)
			}
		}
	}
	return nil
}

// stageFail surfaces a validation failure as a resumable interview turn:
// awaiting_answer with the error appended to raw_reply. Deterministic
// validation of an unchanged scratch dir yields the same appended text, so a
// re-ingest of the same turn is the idempotent no-op the notify loop-guard
// requires.
func (s *Service) stageFail(row *wizardRow, verr error) {
	text := strings.TrimSpace(s.lastAssistantText(row.uuid))
	newRaw := text + "\n\nREVISION REJECTED: " + verr.Error() +
		"\n(The scratch dir was kept — resume this session, tell the agent what to fix, and have it re-stage the revision.)"
	if row.status == StatusAwaiting && row.rawReply.Valid && row.rawReply.String == newRaw {
		return // idempotent re-ingest — no notify (loop guard, see OnSessionTurns)
	}
	log.Printf("warn: planning: revise uuid=%s revision rejected: %v", row.uuid, verr)
	res, err := s.DB.Exec(
		`UPDATE planning_sessions SET current_question=NULL, raw_reply=?, status=?, updated_at=?
		  WHERE id=? AND status IN (?, ?, ?)`,
		newRaw, StatusAwaiting, s.ts(), row.id,
		StatusGenerating, StatusAwaiting, StatusProceeding)
	if err != nil {
		log.Printf("error: planning: revise uuid=%s apply rejection: %v", row.uuid, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return // lost to a concurrent terminal write — never overwrite it
	}
	s.notify(row.projectID)
}

// runningDocs returns the basenames of docs whose phase row is mid-run.
func (s *Service) runningDocs(taskID int64) (map[string]bool, error) {
	rows, err := s.DB.Query(
		`SELECT doc_path FROM epic_phases WHERE workspace_task_id = ? AND run_state = 'running'`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out[filepath.Base(p)] = true
	}
	return out, rows.Err()
}

// sessionIdea reads the session's stored idea (the operator's revise reason).
func (s *Service) sessionIdea(sessionID int64) string {
	var idea string
	if err := s.DB.QueryRow(`SELECT idea FROM planning_sessions WHERE id = ?`, sessionID).Scan(&idea); err != nil {
		return ""
	}
	return idea
}

// listPlanDocNames returns the plan dir's markdown doc basenames (sorted,
// README excluded) — the live half of the post-revision doc set, and the
// revise prompt's seed list.
func listPlanDocNames(planDir string) []string {
	entries, err := os.ReadDir(planDir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || e.Name() == "README.md" || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
