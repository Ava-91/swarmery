package phaserun

import (
	"fmt"
	"path/filepath"
	"strings"
	"text/template"
)

// promptTemplate is the phase-run execution contract (interactive planning v2
// phase 5, spec Part 2 §2.2). The phase DOC is the contract — the executor
// works in an isolated worktree, ticks the doc's acceptance checkboxes in
// place (progress then flows through the existing wsingest checkbox pipeline),
// commits locally, and never pushes.
//
// text/template so the doc path/content interpolate without any prompt-side
// format bug (idiom of planning/prompt.go).
var promptTemplate = template.Must(template.New("phaserun").Parse(
	`You are executing ONE phase of an approved implementation plan, headlessly, in an isolated git worktree of the project repo (your cwd).

The phase document below is your complete contract. Follow it exactly:
- Complete the numbered tasks / acceptance criteria of THIS phase only — do not start other phases.
- As you complete each acceptance criterion, EDIT the phase document itself and tick its checkbox (- [ ] → - [x]). The document lives at: {{.DocPath}} — edit it in place (it is outside the repo; use the absolute path).
- Run the verification commands the document specifies before declaring done.
- Commit your work in the worktree with conventional commits. Do NOT push, do NOT open PRs, do NOT merge.
- ENDING YOUR TURN ENDS THIS PROCESS, and any subagent still running dies with it — while the exit code stays 0, so the run is recorded as a clean success that landed nothing. Never dispatch helpers and then reply that you are waiting on them: that reply IS the kill. Await anything you dispatch inside the same turn, or do the work yourself.
- If the document's premises don't match the code you find, STOP and end your reply with: PHASE BLOCKED: <one-line reason>. Otherwise end with: PHASE DONE.

{{.RepoNote}}PHASE DOCUMENT ({{.DocRelPath}}):
----------------------------------------
{{.DocContent}}
----------------------------------------`))

// BuildPrompt renders the phase-run prompt for one phase doc. Template
// execution on a fixed template with string data cannot fail, so the
// (unreachable) error is ignored (same posture as planning.BuildPrompt).
func BuildPrompt(docPath, docRelPath, docContent string) string {
	return BuildPromptIn(docPath, docRelPath, docContent, "", "")
}

// BuildPromptIn is BuildPrompt with the run's repository context: repoRoot is the
// resolved checkout the worktree was cut from, projectPath the project root.
//
// When they differ — a multi-repo project, where the run lives in ONE checkout
// inside the umbrella — the prompt says so. Phase docs for such projects write
// their paths from the project root ("sk-next/src/components/x.tsx"), and inside
// the worktree that same file is "src/components/x.tsx"; without the note an
// agent "fixes" the mismatch by creating a nested directory and writes the whole
// phase into a tree nobody reads.
func BuildPromptIn(docPath, docRelPath, docContent, repoRoot, projectPath string) string {
	var b strings.Builder
	_ = promptTemplate.Execute(&b, struct {
		DocPath    string
		DocRelPath string
		DocContent string
		RepoNote   string
	}{docPath, docRelPath, docContent, repoNote(repoRoot, projectPath)})
	return b.String()
}

// repoNote renders the multi-repo orientation block, or "" when the run's
// repository IS the project root (where the note would only add noise).
func repoNote(repoRoot, projectPath string) string {
	if repoRoot == "" || projectPath == "" || filepath.Clean(repoRoot) == filepath.Clean(projectPath) {
		return ""
	}
	name := filepath.Base(repoRoot)
	return fmt.Sprintf(
		"REPOSITORY: your worktree is a checkout of `%s` (%s), ONE repository inside the project root %s.\n"+
			"Paths in this document may be written from the project root (e.g. `%s/src/...`); inside your worktree that same file is `src/...`. "+
			"Do NOT create a `%s/` directory to make such a path resolve.\n\n",
		name, repoRoot, projectPath, name, name)
}
