package promptguard_test

// This guard exists because the bug it catches reads as helpful advice.
//
// The dispatched-agent contract used to say "Work ONLY here" and then, two
// lines later, "THIS CARD HAS A PLAN DOCUMENT at <absolute path> — edit it in
// place (it is outside the repo; use the absolute path)". Both sentences look
// reasonable in isolation. Together they instruct an agent to do the one thing
// its sandbox refuses, and one retro window measured the cost: 56 isolation
// errors, 4 plan-read refusals, 3 summaries written to scratch files the
// dashboard never reads, and an 81% error rate for the executor agent.
//
// The reported instance was in internal/dispatch. The SAME sentence was also in
// internal/phaserun, and the same absolute path reached playbook bodies through
// a template variable — so fixing the report would have left the class open in
// two other places. This test closes the class instead: any prompt-building
// string literal anywhere under internal/** that tells an agent to work at an
// absolute path outside its tree fails the build.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// promptMarkers identify a string literal that is talking TO an agent rather
// than to a machine. A literal counts as prompt text only if it carries one of
// these, so ordinary code strings (SQL, log lines, file paths) never enter the
// scan and the rule stays cheap to reason about.
var promptMarkers = []string{
	"you are ", "your ", "do not ", "don't ", "must ", "end your reply",
	"edit it", "the document", "this card", "acceptance criteria", "checkbox",
}

// violationPhrases are the instruction shapes that put an agent outside its
// root. Matched on SHAPE, not on one exact sentence: a reworded version of the
// same instruction is the same bug, and pinning the exact wording would let a
// paraphrase through while looking rigorous.
var violationPhrases = []string{
	"outside the repo",
	"outside the worktree",
	"use the absolute path",
	"by absolute path",
	"its absolute path",
}

// exemptions are prompt sites that legitimately name an out-of-tree absolute
// path, keyed "<relpath>:<func>", with the reason recorded next to each.
//
// The map is checked in BOTH directions. An entry that no longer matches
// anything is deleted by a failing test rather than left to rot: a stale
// exemption is worse than a missing one, because it silently absolves whatever
// future site happens to reuse the name.
var exemptions = map[string]string{}

func TestNoPromptSendsAnAgentOutsideItsWorktree(t *testing.T) {
	root := moduleRoot(t)
	internalDir := filepath.Join(root, "internal")

	var violations []string
	matchedExemptions := map[string]bool{}

	err := filepath.WalkDir(internalDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "testdata", "node_modules", ".git":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)

		for _, decl := range f.Decls {
			for _, lit := range promptLiterals(decl) {
				lower := strings.ToLower(lit)
				if !isPromptText(lower) {
					continue
				}
				phrase, bad := violates(lower)
				if !bad {
					continue
				}
				key := rel + ":" + declName(decl)
				if _, ok := exemptions[key]; ok {
					matchedExemptions[key] = true
					continue
				}
				violations = append(violations, key+" — says "+strconv.Quote(phrase))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(violations) > 0 {
		sort.Strings(violations)
		t.Errorf(`a prompt instructs an agent to work outside its worktree:

  %s

An agent dispatched into a worktree is REFUSED when it reaches outside it, so an
instruction to use an out-of-tree absolute path cannot be obeyed — it costs the
turn and, when the path is the plan document, loses the report the dashboard
renders.

Lend the file INTO the worktree (worktree.LendPlanDoc) and name it by its
worktree-relative path; return it afterwards (worktree.ReturnPlanDocLogged). If
this site genuinely needs an out-of-tree absolute path, add it to exemptions in
this file WITH the reason.`, strings.Join(violations, "\n  "))
	}

	for key := range exemptions {
		if !matchedExemptions[key] {
			t.Errorf("exemption %q no longer matches any prompt site — delete it or re-key it; "+
				"a stale exemption silently absolves whatever reuses the name", key)
		}
	}
}

// promptLiterals collects every string literal inside a declaration, including
// the concatenated pieces that a Go raw string + "`" + interpolation produces.
func promptLiterals(decl ast.Decl) []string {
	var out []string
	ast.Inspect(decl, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if s, err := strconv.Unquote(lit.Value); err == nil {
				out = append(out, s)
			}
		}
		return true
	})
	return out
}

func isPromptText(lower string) bool {
	for _, m := range promptMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

func violates(lower string) (string, bool) {
	for _, p := range violationPhrases {
		if strings.Contains(lower, p) {
			return p, true
		}
	}
	return "", false
}

// declName is the enclosing function or var name, so a violation points at
// something a reader can open.
func declName(decl ast.Decl) string {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		return d.Name.Name
	case *ast.GenDecl:
		for _, spec := range d.Specs {
			if vs, ok := spec.(*ast.ValueSpec); ok && len(vs.Names) > 0 {
				return vs.Names[0].Name
			}
		}
	}
	return "(file scope)"
}

// TestScannerActuallyMatches keeps the guard from passing vacuously. If the
// literal-collection or prompt-detection heuristic ever stops working, every
// scan returns "clean" over a corpus it is no longer reading — so prove on a
// synthetic input that a violation IS detected and an innocent prompt is not.
func TestScannerActuallyMatches(t *testing.T) {
	violating := "You are running in a worktree. The document is outside the repo; use the absolute path."
	if !isPromptText(strings.ToLower(violating)) {
		t.Fatal("the prompt-text heuristic no longer recognises an agent instruction")
	}
	if _, bad := violates(strings.ToLower(violating)); !bad {
		t.Fatal("the violation heuristic no longer recognises the reported instruction")
	}

	fixed := "You are running in a worktree. The document has been lent into this worktree at .swarmery/plan/x.md — edit it there."
	if _, bad := violates(strings.ToLower(fixed)); bad {
		t.Fatal("the fixed wording is flagged as a violation")
	}

	sqlish := "SELECT path FROM projects WHERE id = ?"
	if isPromptText(strings.ToLower(sqlish)) {
		t.Fatal("ordinary code strings are being scanned as prompt text")
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("go.mod not found above %s", dir)
	return ""
}
