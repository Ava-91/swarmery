package phaserun

import (
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
- If the document's premises don't match the code you find, STOP and end your reply with: PHASE BLOCKED: <one-line reason>. Otherwise end with: PHASE DONE.

PHASE DOCUMENT ({{.DocRelPath}}):
----------------------------------------
{{.DocContent}}
----------------------------------------`))

// BuildPrompt renders the phase-run prompt for one phase doc. Template
// execution on a fixed template with string data cannot fail, so the
// (unreachable) error is ignored (same posture as planning.BuildPrompt).
func BuildPrompt(docPath, docRelPath, docContent string) string {
	var b strings.Builder
	_ = promptTemplate.Execute(&b, struct {
		DocPath    string
		DocRelPath string
		DocContent string
	}{docPath, docRelPath, docContent})
	return b.String()
}
