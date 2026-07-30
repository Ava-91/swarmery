package wsingest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCountCheckboxes(t *testing.T) {
	cases := []struct {
		name              string
		in                string
		wantDone, wantTot int
	}{
		{"mixed", "- [x] a\n- [ ] b\n- [x] c\n", 2, 3},
		{"none", "## Acceptance\n\nsome prose, no boxes\n", 0, 0},
		{"upper X", "- [X] done\n- [ ] not\n", 1, 2},
		{"star bullet", "* [x] a\n* [ ] b\n", 1, 2},
		{"indented", "  - [x] nested\n    - [ ] deeper\n", 1, 2},
		{"not a checkbox", "- [] a\n- [y] b\n- regular item\n", 0, 0},
		{"all done", "- [x] a\n- [x] b\n", 2, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			done, tot := CountCheckboxes(c.in)
			if done != c.wantDone || tot != c.wantTot {
				t.Errorf("CountCheckboxes = %d/%d, want %d/%d", done, tot, c.wantDone, c.wantTot)
			}
		})
	}
}

func TestParseDocStatus(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"in progress", "# Phase 2 — API\nStatus: In progress\n## Goal\n", "in_progress"},
		{"wip alias", "# P\nStatus: WIP\n", "in_progress"},
		{"kebab", "# P\nStatus: in-progress\n", "in_progress"},
		{"pending", "# P\nStatus: Pending\n", "pending"},
		{"done", "# P\nStatus: Completed\n", "done"},
		{"absent", "# P\n\n## Goal\nno marker\n", ""},
		{"unrecognized", "# P\nStatus: on fire\n", ""},
		{"below a section is ignored", "# P\n\n## Template\nStatus: Pending\n", ""},
		{"beyond header window ignored", "# P\n" + strings.Repeat("filler\n", docStatusHeaderLines) + "Status: In progress\n", ""},
		{"not a status line", "# P\nRepo: /x · Status quo\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseDocStatus(c.in); got != c.want {
				t.Errorf("parseDocStatus = %q, want %q", got, c.want)
			}
		})
	}
}

func TestParseCompletionReport(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"basic", "# P\n## Goal\nx\n## Completion Report\nShipped X.\n- commit abc\n", "Shipped X.\n- commit abc"},
		{"stops at next section", "# P\n## Completion Report\ndone stuff\n## Notes\nnope\n", "done stuff"},
		{"absent", "# P\n## Goal\nx\n", ""},
		{"empty stub", "# P\n## Completion Report\n\n## Notes\nx\n", ""},
		{"case-insensitive heading", "# P\n## completion report\nfilled\n", "filled"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseCompletionReport(c.in); got != c.want {
				t.Errorf("parseCompletionReport = %q, want %q", got, c.want)
			}
		})
	}
}

func TestParseLeadingInts(t *testing.T) {
	cases := []struct {
		in   string
		want []int
	}{
		{"—", nil},
		{"-", nil},
		{"none", nil},
		{"", nil},
		{"1, 2", []int{1, 2}},
		{"1 (API), 3 (live states)", []int{1, 3}},
		{"1", []int{1}},
		{"10—15", []int{10, 15}},
	}
	for _, c := range cases {
		if got := parseLeadingInts(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseLeadingInts(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestDocFromCell(t *testing.T) {
	cases := []struct{ in, want string }{
		{"`phase-1-task-queue.md`", "phase-1-task-queue.md"},
		{"`step-03-wire.md`", "step-03-wire.md"},
		{"phase-2-parser.md", "phase-2-parser.md"},
		{"see phase-4-board-ui.md here", "phase-4-board-ui.md"},
		{"no doc here", ""},
		{"", ""},
		{"`some/path/phase-9.md`", "phase-9.md"}, // basename only
	}
	for _, c := range cases {
		if got := docFromCell(c.in); got != c.want {
			t.Errorf("docFromCell(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPhaseTableCols(t *testing.T) {
	t.Run("full header", func(t *testing.T) {
		cells := []string{"#", "Phase", "Doc", "Repo area", "Depends on", "Parallel?", "Est."}
		cols, ok := phaseTableCols(cells)
		if !ok || cols.seq != 0 || cols.name != 1 || cols.doc != 2 || cols.dep != 4 {
			t.Errorf("cols = %+v ok %v", cols, ok)
		}
		// "Repo area" names a subsystem, not a checkout — it must NOT be read as the
		// run root, or a run would be sent into a directory called "daemon".
		if cols.repo != -1 {
			t.Errorf("cols.repo = %d, want -1 for a 'Repo area' column", cols.repo)
		}
	})
	t.Run("no doc column → not ok", func(t *testing.T) {
		cells := []string{"#", "Phase", "Repo area"}
		if _, ok := phaseTableCols(cells); ok {
			t.Error("expected ok=false without a Doc column")
		}
	})
	t.Run("synonyms seq/name/file", func(t *testing.T) {
		cells := []string{"Seq", "Name", "File", "Depends"}
		cols, ok := phaseTableCols(cells)
		if !ok || cols.seq != 0 || cols.name != 1 || cols.doc != 2 || cols.dep != 3 {
			t.Errorf("cols = %+v ok %v", cols, ok)
		}
	})
	t.Run("repo column and its synonyms", func(t *testing.T) {
		for i, cells := range [][]string{
			{"#", "Phase", "Doc", "Repo", "Depends on"},
			{"#", "Phase", "Doc", "Repos", "Depends on"},
			{"#", "Phase", "Doc", "Repository", "Depends on"},
		} {
			cols, ok := phaseTableCols(cells)
			if !ok || cols.repo != 3 {
				t.Errorf("case %d: cols = %+v ok %v, want repo=3", i, cols, ok)
			}
		}
	})
}

func TestParseDocRepo(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{
			name: "header table row (name + absolute path)",
			in:   "# Phase 3\n\n| Field | Value |\n|---|---|\n| **Repo** | `sk-next` (`/Volumes/Work/Skygor/sk-next`) |\n| **Branch** | `perf/x` |\n",
			want: "`sk-next` (`/Volumes/Work/Skygor/sk-next`)",
		},
		{
			name: "prose header line",
			in:   "# Phase 1\n\n**Repo:** `/Volumes/Work/swarmery`\n**Branch:** `feat/x`\n",
			want: "`/Volumes/Work/swarmery`",
		},
		{
			name: "plural spelling",
			in:   "# Phase 1\n\n| **Repos** | `a` |\n",
			want: "`a`",
		},
		{name: "absent", in: "# Phase 1\n\nno repo here\n", want: ""},
		{
			// A quoted agent prompt further down describes someone else's repo; the
			// header block bound is what keeps it from being read as this phase's.
			name: "after a section heading is ignored",
			in:   "# Phase 1\n\n## Agent prompt\n\n> **Repo:** `/other/repo`\n",
			want: "",
		},
		{
			name: "beyond the header window is ignored",
			in:   "# Phase 1\n" + strings.Repeat("\n", 20) + "**Repo:** `/late/repo`\n",
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseDocRepo(c.in); got != c.want {
				t.Errorf("parseDocRepo = %q, want %q", got, c.want)
			}
		})
	}
}

func TestParsePlanTableRepoColumn(t *testing.T) {
	readme := "# Epic\n\n| # | Phase | Doc | Repo | Depends on |\n|---|---|---|---|---|\n" +
		"| 1 | Schema | `phase-1.md` | sk-next | — |\n" +
		"| 2 | Helm | `phase-2.md` | sk-next (+ helm) | 1 |\n"
	phases := parsePlanTable(readme)
	if len(phases) != 2 {
		t.Fatalf("phases = %d, want 2", len(phases))
	}
	if phases[0].repo != "sk-next" || phases[1].repo != "sk-next (+ helm)" {
		t.Errorf("repos = %q, %q", phases[0].repo, phases[1].repo)
	}
}

// A plan with no Repo column must index exactly as it did before the column
// existed — that is what every already-indexed plan looks like.
func TestParsePlanTableNoRepoColumn(t *testing.T) {
	readme := "# Epic\n\n| # | Phase | Doc | Depends on |\n|---|---|---|---|\n" +
		"| 1 | Schema | `phase-1.md` | — |\n"
	phases := parsePlanTable(readme)
	if len(phases) != 1 || phases[0].repo != "" {
		t.Fatalf("phases = %+v, want one phase with an empty repo", phases)
	}
}

// The doc's own header is the more specific statement and outranks the table.
func TestParsePlanDocRepoOverridesTable(t *testing.T) {
	dir := writePlan(t, map[string]string{
		"README.md": "# Epic\n\n| # | Phase | Doc | Repo |\n|---|---|---|---|\n" +
			"| 1 | Schema | `phase-1.md` | table-repo |\n| 2 | UI | `phase-2.md` | table-repo |\n",
		"phase-1.md": "# Phase 1\n\n| **Repo** | `doc-repo` |\n",
		"phase-2.md": "# Phase 2\n\nno header repo\n",
	})
	warn, _ := collectWarn(t)
	phases := parsePlan(dir, warn)
	if len(phases) != 2 {
		t.Fatalf("phases = %d, want 2", len(phases))
	}
	if phases[0].repo != "`doc-repo`" {
		t.Errorf("phase[0].repo = %q, want the doc header", phases[0].repo)
	}
	if phases[1].repo != "table-repo" {
		t.Errorf("phase[1].repo = %q, want the table cell to survive", phases[1].repo)
	}
}

func TestParsePlanTable(t *testing.T) {
	readme := `# Epic

## Phase sequencing

| # | Phase | Doc | Repo area | Depends on | Parallel? | Est. |
|---|---|---|---|---|---|---|
| 1 | Schema | ` + "`phase-1-schema.md`" + ` | daemon | — | with 2 | 1 d |
| 2 | Parser | ` + "`phase-2-parser.md`" + ` | daemon | 1 | — | 1 d |
| 3 | UI | ` + "`phase-3-ui.md`" + ` | web | 1, 2 | — | 2 d |

**Critical path:** 1 → 2 → 3.
`
	phases := parsePlanTable(readme)
	if len(phases) != 3 {
		t.Fatalf("phases = %d, want 3", len(phases))
	}
	if phases[0].seq != 1 || phases[0].name != "Schema" || phases[0].docPath != "phase-1-schema.md" {
		t.Errorf("phase[0] = %+v", phases[0])
	}
	if phases[0].dependsOn != nil {
		t.Errorf("phase[0].dependsOn = %v, want nil (em-dash)", phases[0].dependsOn)
	}
	if !reflect.DeepEqual(phases[2].dependsOn, []int{1, 2}) {
		t.Errorf("phase[2].dependsOn = %v, want [1 2]", phases[2].dependsOn)
	}
}

func TestParsePlanTableNoTable(t *testing.T) {
	readme := "# Epic\n\nJust prose, no phase-sequencing table at all.\n"
	if got := parsePlanTable(readme); got != nil {
		t.Errorf("parsePlanTable(no table) = %v, want nil", got)
	}
}

// writePlan is a tiny fixture helper: writes files (name→content) into a fresh
// plan dir and returns its path.
func writePlan(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "plan")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestParsePlanWithTable(t *testing.T) {
	dir := writePlan(t, map[string]string{
		"README.md": "# Epic\n\n| # | Phase | Doc | Depends on |\n|---|---|---|---|\n" +
			"| 1 | Schema | `phase-1.md` | — |\n| 2 | UI | `phase-2.md` | 1 |\n",
		"phase-1.md": "# Phase 1 — Schema\n\n## Acceptance criteria\n- [x] a\n- [ ] b\n",
		"phase-2.md": "# Phase 2 — UI\n\nno checkboxes here\n",
	})
	warn, _ := collectWarn(t)
	phases := parsePlan(dir, warn)
	if len(phases) != 2 {
		t.Fatalf("phases = %d, want 2", len(phases))
	}
	// H1 title overrides the terse table label.
	if phases[0].name != "Phase 1 — Schema" {
		t.Errorf("phase[0].name = %q, want the doc H1", phases[0].name)
	}
	if phases[0].checkboxesDone != 1 || phases[0].checkboxesTotal != 2 {
		t.Errorf("phase[0] checkboxes = %d/%d, want 1/2", phases[0].checkboxesDone, phases[0].checkboxesTotal)
	}
	if phases[1].checkboxesTotal != 0 {
		t.Errorf("phase[1] checkboxes total = %d, want 0", phases[1].checkboxesTotal)
	}
	if !filepath.IsAbs(phases[0].docPath) {
		t.Errorf("phase[0].docPath = %q, want absolute", phases[0].docPath)
	}
}

func TestParsePlanFallbackNoTable(t *testing.T) {
	// No table in README → one phase per phase-*/step-* doc, seq by filename sort.
	dir := writePlan(t, map[string]string{
		"README.md":    "# Epic\n\nprose only, no table\n",
		"phase-2-b.md": "# Second\n- [x] done\n",
		"phase-1-a.md": "# First\n- [ ] todo\n- [x] done\n",
		"step-03-c.md": "# Third\nno boxes\n",
		"notes.md":     "# Not a phase doc — ignored\n- [x] x\n",
	})
	warn, _ := collectWarn(t)
	phases := parsePlan(dir, warn)
	if len(phases) != 3 {
		t.Fatalf("phases = %d, want 3 (notes.md excluded)", len(phases))
	}
	// Sorted: phase-1-a, phase-2-b, step-03-c.
	if phases[0].name != "First" || phases[1].name != "Second" || phases[2].name != "Third" {
		t.Errorf("fallback order = %q/%q/%q", phases[0].name, phases[1].name, phases[2].name)
	}
	if phases[0].seq != 1 || phases[1].seq != 2 || phases[2].seq != 3 {
		t.Errorf("fallback seqs = %d/%d/%d", phases[0].seq, phases[1].seq, phases[2].seq)
	}
	if phases[0].checkboxesDone != 1 || phases[0].checkboxesTotal != 2 {
		t.Errorf("phase[0] checkboxes = %d/%d, want 1/2", phases[0].checkboxesDone, phases[0].checkboxesTotal)
	}
}

// collectWarn returns a warn func plus a pointer to the formatted messages it
// captured — the test-side stand-in for the scan's logging warn.
func collectWarn(t *testing.T) (func(string, ...any), *[]string) {
	t.Helper()
	var msgs []string
	return func(format string, args ...any) {
		msgs = append(msgs, fmt.Sprintf(format, args...))
	}, &msgs
}

func TestParsePlanMissingDocFile(t *testing.T) {
	// A table row naming a doc that doesn't exist keeps its metadata and its
	// (non-existent) path, with no counts.
	dir := writePlan(t, map[string]string{
		"README.md": "# Epic\n\n| # | Phase | Doc | Depends on |\n|---|---|---|---|\n" +
			"| 1 | Ghost | `phase-missing.md` | — |\n",
	})
	warn, msgs := collectWarn(t)
	phases := parsePlan(dir, warn)
	if len(phases) != 1 {
		t.Fatalf("phases = %d, want 1", len(phases))
	}
	if phases[0].name != "Ghost" {
		t.Errorf("phase[0].name = %q, want Ghost (table label kept)", phases[0].name)
	}
	if want := filepath.Join(dir, "phase-missing.md"); phases[0].docPath != want {
		t.Errorf("phase[0].docPath = %q, want %q — the table's filename joined to the plan dir",
			phases[0].docPath, want)
	}
	if phases[0].checkboxesTotal != 0 {
		t.Errorf("phase[0] total = %d, want 0", phases[0].checkboxesTotal)
	}
	if len(*msgs) != 1 || !strings.Contains((*msgs)[0], "phase-missing.md") {
		t.Errorf("warnings = %v, want one naming the missing doc", *msgs)
	}
}

// TestParsePlanMissingDocsStayDistinct is the regression for the collision that
// blanking docPath to "" created: every unresolved row shared the natural key
// (task, ""), so the UNIQUE(workspace_task_id, doc_path) upsert kept exactly ONE
// of them (last writer wins, mislabelled) — and before the upsert landed, the
// constraint violation rolled the whole plan back to zero indexed phases.
func TestParsePlanMissingDocsStayDistinct(t *testing.T) {
	dir := writePlan(t, map[string]string{
		"README.md": "# Epic\n\n| # | Phase | Doc | Depends on |\n|---|---|---|---|\n" +
			"| 1 | Phase 1 | `phase-1-gone.md` | — |\n" +
			"| 2 | Phase 2 | `phase-2-gone.md` | 1 |\n",
	})
	warn, msgs := collectWarn(t)
	phases := parsePlan(dir, warn)
	if len(phases) != 2 {
		t.Fatalf("phases = %d, want 2", len(phases))
	}
	if phases[0].docPath == phases[1].docPath {
		t.Fatalf("both unresolved docs share doc_path %q — they collide on the natural key",
			phases[0].docPath)
	}
	if phases[0].docPath != filepath.Join(dir, "phase-1-gone.md") ||
		phases[1].docPath != filepath.Join(dir, "phase-2-gone.md") {
		t.Errorf("docPaths = %q / %q, want the table filenames under %s",
			phases[0].docPath, phases[1].docPath, dir)
	}
	if phases[0].seq != 1 || phases[0].name != "Phase 1" ||
		phases[1].seq != 2 || phases[1].name != "Phase 2" {
		t.Errorf("table metadata lost: %+v / %+v", phases[0], phases[1])
	}
	if len(*msgs) != 2 {
		t.Errorf("warnings = %v, want one per missing doc", *msgs)
	}
}

func TestParsePlanEmptyDir(t *testing.T) {
	dir := writePlan(t, map[string]string{}) // no files at all
	warn, _ := collectWarn(t)
	if got := parsePlan(dir, warn); len(got) != 0 {
		t.Errorf("parsePlan(empty) = %v, want none", got)
	}
}

// TestScanEpicsIndexesEveryUnresolvedPhase is the end-to-end shape of the same
// defect: a plan whose docs have not been written yet must still index EVERY table
// row, not collapse to a single mislabelled survivor.
func TestScanEpicsIndexesEveryUnresolvedPhase(t *testing.T) {
	db := testDB(t)
	root := t.TempDir()
	planDir := filepath.Join(root, "demo", "workspace", "working", "2026", "07", "26", "demo", "plan")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	card := "# Task: Demo epic\n\n" +
		"- **Статус**: active\n- **Старт**: 2026-07-26 · **Завершено**: —\n- **Ціль**: demo goal\n"
	if err := os.WriteFile(filepath.Join(filepath.Dir(planDir), "README.md"), []byte(card), 0o644); err != nil {
		t.Fatal(err)
	}
	readme := "# Demo plan\n\n| # | Phase | Doc | Depends on |\n|---|---|---|---|\n" +
		"| 1 | Schema | `phase-1-schema.md` | — |\n" +
		"| 2 | Parser | `phase-2-parser.md` | 1 |\n"
	if err := os.WriteFile(filepath.Join(planDir, "README.md"), []byte(readme), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New(db, Config{WorkspaceRoot: root})
	if _, err := s.Scan(); err != nil {
		t.Fatalf("scan: %v", err)
	}

	rows, err := db.Query(`SELECT seq, name, doc_path FROM epic_phases ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type ph struct {
		seq       int
		name, doc string
	}
	var got []ph
	for rows.Next() {
		var p ph
		if err := rows.Scan(&p.seq, &p.name, &p.doc); err != nil {
			t.Fatal(err)
		}
		got = append(got, p)
	}
	if len(got) != 2 {
		t.Fatalf("epic_phases = %+v (%d rows), want 2 — unresolved docs collided on (task, \"\")",
			got, len(got))
	}
	if got[0].seq != 1 || got[0].name != "Schema" || filepath.Base(got[0].doc) != "phase-1-schema.md" {
		t.Errorf("phase 1 = %+v", got[0])
	}
	if got[1].seq != 2 || got[1].name != "Parser" || filepath.Base(got[1].doc) != "phase-2-parser.md" {
		t.Errorf("phase 2 = %+v", got[1])
	}
}

// The declared repo has to survive the whole scan into the column the run
// surfaces read — and a rescan after the declaration is deleted must clear it.
// The docs are the truth; an index that keeps a stale repo would send a run into
// a directory the plan no longer names.
func TestScanEpicsStoresAndClearsDeclaredRepo(t *testing.T) {
	db := testDB(t)
	root := t.TempDir()
	taskDir := filepath.Join(root, "demo", "workspace", "working", "2026", "07", "30", "demo")
	planDir := filepath.Join(taskDir, "plan")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	card := "# Task: Demo epic\n\n" +
		"- **Статус**: active\n- **Старт**: 2026-07-30 · **Завершено**: —\n- **Ціль**: demo goal\n"
	if err := os.WriteFile(filepath.Join(taskDir, "README.md"), []byte(card), 0o644); err != nil {
		t.Fatal(err)
	}
	readme := "# Demo plan\n\n| # | Phase | Doc | Repo | Depends on |\n|---|---|---|---|---|\n" +
		"| 1 | Schema | `phase-1.md` | table-repo | — |\n"
	if err := os.WriteFile(filepath.Join(planDir, "README.md"), []byte(readme), 0o644); err != nil {
		t.Fatal(err)
	}
	writeDoc := func(body string) {
		if err := os.WriteFile(filepath.Join(planDir, "phase-1.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeDoc("# Phase 1\n\n| **Repo** | `sk-next` |\n\n- [ ] a\n")

	s := New(db, Config{WorkspaceRoot: root})
	if _, err := s.Scan(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	var repo sql.NullString
	if err := db.QueryRow(`SELECT repo FROM epic_phases WHERE seq = 1`).Scan(&repo); err != nil {
		t.Fatal(err)
	}
	if repo.String != "`sk-next`" {
		t.Fatalf("repo = %q, want the doc header cell", repo.String)
	}

	// Drop the declaration from both the doc and the table, rescan.
	writeDoc("# Phase 1\n\n- [ ] a\n")
	if err := os.WriteFile(filepath.Join(planDir, "README.md"),
		[]byte("# Demo plan\n\n| # | Phase | Doc | Depends on |\n|---|---|---|---|\n| 1 | Schema | `phase-1.md` | — |\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Scan(); err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if err := db.QueryRow(`SELECT repo FROM epic_phases WHERE seq = 1`).Scan(&repo); err != nil {
		t.Fatal(err)
	}
	if repo.Valid {
		t.Fatalf("repo = %q after the declaration was removed, want NULL", repo.String)
	}
}

// TestScanEpicsGammaFixture drives the full hash-gated scan over the committed
// gamma-task plan fixture (README table + 3 phase docs, one with 4 checkboxes
// half done, one fully done, one with zero checkboxes).
func TestScanEpicsGammaFixture(t *testing.T) {
	db := testDB(t)
	seed(t, db)
	pinMtime(t)
	stats := scan(t, db)

	if stats.EpicPhases != 3 {
		t.Errorf("epic_phases = %d, want 3 (gamma plan)", stats.EpicPhases)
	}

	var taskID int64
	if err := db.QueryRow(`SELECT id FROM tasks WHERE external_id='2026-07-08-gamma-task'`).Scan(&taskID); err != nil {
		t.Fatalf("gamma-task row: %v", err)
	}

	rows, err := db.Query(`SELECT seq, name, depends_on, checkboxes_done, checkboxes_total
		FROM epic_phases WHERE workspace_task_id = ? ORDER BY seq`, taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type ph struct {
		seq         int
		name, deps  string
		done, total int
	}
	var got []ph
	for rows.Next() {
		var p ph
		if err := rows.Scan(&p.seq, &p.name, &p.deps, &p.done, &p.total); err != nil {
			t.Fatal(err)
		}
		got = append(got, p)
	}
	if len(got) != 3 {
		t.Fatalf("epic_phases rows = %d, want 3", len(got))
	}
	// Phase 1: H1 "Phase 1 — Schema + write API", 2/4 checkboxes, no deps.
	if got[0].seq != 1 || got[0].done != 2 || got[0].total != 4 || got[0].deps != "[]" {
		t.Errorf("phase 1 = %+v, want seq1 2/4 deps[]", got[0])
	}
	// Phase 2: fully done 3/3, deps [1].
	if got[1].seq != 2 || got[1].done != 3 || got[1].total != 3 || got[1].deps != "[1]" {
		t.Errorf("phase 2 = %+v, want seq2 3/3 deps[1]", got[1])
	}
	// Phase 3: zero checkboxes, deps [1,2].
	if got[2].seq != 3 || got[2].done != 0 || got[2].total != 0 || got[2].deps != "[1,2]" {
		t.Errorf("phase 3 = %+v, want seq3 0/0 deps[1,2]", got[2])
	}
}

// TestScanEpicsPreservesActivation checks that a rescan after an activation
// (activated_at + board task stamped) does NOT clear the activation state. The
// guarantee used to come from snapshotting those two columns around a
// delete+reinsert; it now falls out of the upsert leaving them off its
// DO UPDATE SET list.
func TestScanEpicsPreservesActivation(t *testing.T) {
	db := testDB(t)
	seed(t, db)
	pinMtime(t)
	scan(t, db)

	var taskID int64
	if err := db.QueryRow(`SELECT id FROM tasks WHERE external_id='2026-07-08-gamma-task'`).Scan(&taskID); err != nil {
		t.Fatalf("gamma-task row: %v", err)
	}
	// Simulate an activation of phase seq=1.
	if _, err := db.Exec(`UPDATE epic_phases SET activated_at='2026-07-24T00:00:00Z',
		activated_board_task_id=999 WHERE workspace_task_id=? AND seq=1`, taskID); err != nil {
		t.Fatal(err)
	}
	// Force the gate to re-parse by clearing the stored plan hash, then rescan.
	if _, err := db.Exec(`DELETE FROM task_artifacts WHERE task_id=? AND kind='plan'`, taskID); err != nil {
		t.Fatal(err)
	}
	scan(t, db)

	var at string
	var boardID int64
	if err := db.QueryRow(`SELECT activated_at, activated_board_task_id
		FROM epic_phases WHERE workspace_task_id=? AND seq=1`, taskID).Scan(&at, &boardID); err != nil {
		t.Fatalf("phase 1 after rescan: %v", err)
	}
	if at != "2026-07-24T00:00:00Z" || boardID != 999 {
		t.Errorf("activation lost after rescan: at=%q board=%d", at, boardID)
	}
}

// TestScanEpicsPreservesRunStateAcrossCheckboxFlip is THE regression for the
// inverted run measurement: applyEpics used to DELETE + re-INSERT every phase row
// on any plan-content change, carrying over only the activation columns. The
// phase executor's job is to edit its phase doc and tick its checkbox
// (phaserun/prompt.go), so the very act of succeeding wiped the run_* family the
// run was being measured by — and minted a NEW row id, orphaning the
// swarm/phase-<id> branch the run had just committed to. Runs that produced
// nothing kept their state; runs that landed work looked idle.
func TestScanEpicsPreservesRunStateAcrossCheckboxFlip(t *testing.T) {
	db := testDB(t)
	root, phaseDoc := demoWorkspace(t)
	s := New(db, Config{WorkspaceRoot: root})
	if _, err := s.Scan(); err != nil {
		t.Fatalf("scan 1: %v", err)
	}

	var id int64
	if err := db.QueryRow(`SELECT id FROM epic_phases`).Scan(&id); err != nil {
		t.Fatalf("phase row: %v", err)
	}
	mustExec(t, db, `UPDATE epic_phases
		SET run_state='done', run_session_uuid='uuid-run-1',
		    run_started_at='2026-07-28T10:00:00Z', run_ended_at='2026-07-28T10:20:00Z',
		    run_error='boom', run_checkboxes_before=0
		WHERE id=?`, id)

	// The executor ticks one acceptance criterion — the one edit every run that
	// achieves anything makes.
	raw, err := os.ReadFile(phaseDoc)
	if err != nil {
		t.Fatal(err)
	}
	flipped := strings.Replace(string(raw), "- [ ] a", "- [x] a", 1)
	if err := os.WriteFile(phaseDoc, []byte(flipped), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Scan(); err != nil {
		t.Fatalf("scan 2: %v", err)
	}

	var (
		gotID       int64
		state       string
		uuid        sql.NullString
		startedAt   sql.NullString
		endedAt     sql.NullString
		runErr      sql.NullString
		before      sql.NullInt64
		done, total int
	)
	if err := db.QueryRow(`SELECT id, run_state, run_session_uuid, run_started_at,
		run_ended_at, run_error, run_checkboxes_before, checkboxes_done, checkboxes_total
		FROM epic_phases`).Scan(&gotID, &state, &uuid, &startedAt, &endedAt, &runErr,
		&before, &done, &total); err != nil {
		t.Fatalf("phase row after rescan: %v", err)
	}
	if gotID != id {
		t.Errorf("phase id = %d, want %d — the row was deleted and re-inserted, "+
			"orphaning swarm/phase-%d", gotID, id, id)
	}
	if state != "done" {
		t.Errorf("run_state = %q, want \"done\" (wiped by the rescan)", state)
	}
	if uuid.String != "uuid-run-1" {
		t.Errorf("run_session_uuid = %v, want uuid-run-1", uuid)
	}
	if startedAt.String != "2026-07-28T10:00:00Z" {
		t.Errorf("run_started_at = %v, want 2026-07-28T10:00:00Z", startedAt)
	}
	if endedAt.String != "2026-07-28T10:20:00Z" {
		t.Errorf("run_ended_at = %v, want 2026-07-28T10:20:00Z", endedAt)
	}
	if runErr.String != "boom" {
		t.Errorf("run_error = %v, want boom", runErr)
	}
	if !before.Valid || before.Int64 != 0 {
		t.Errorf("run_checkboxes_before = %v, want 0 (measured baseline lost)", before)
	}
	// Structure IS the doc's to own: the tick must still land.
	if done != 1 || total != 2 {
		t.Errorf("checkboxes = %d/%d, want 1/2", done, total)
	}
}

// demoWorkspace builds a minimal temp workspace with one epic task
// (working/2026/07/26/demo: card README + plan/README.md phase table +
// plan/phase-1-demo.md with checkboxes) and returns the root and the phase
// doc path.
func demoWorkspace(t *testing.T) (root, phaseDoc string) {
	t.Helper()
	root = t.TempDir()
	taskDir := filepath.Join(root, "demo", "workspace", "working", "2026", "07", "26", "demo")
	planDir := filepath.Join(taskDir, "plan")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(taskDir, "README.md"): "# Task: Demo epic\n\n" +
			"- **Статус**: active\n- **Старт**: 2026-07-26 · **Завершено**: —\n- **Ціль**: demo goal\n",
		filepath.Join(planDir, "README.md"): "# Demo plan\n\n" +
			"| # | Phase | Doc | Depends on |\n|---|---|---|---|\n" +
			"| 1 | Demo | `phase-1-demo.md` | — |\n",
		filepath.Join(planDir, "phase-1-demo.md"): "# Phase 1 — Demo\n\n" +
			"## Acceptance criteria\n- [ ] a\n- [ ] b\n",
	}
	for path, body := range files {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, filepath.Join(planDir, "phase-1-demo.md")
}

// multiPhaseWorkspace builds a temp workspace with one epic task whose plan
// README lists the given phase docs in order. Returns the workspace root and the
// plan dir, so a test can rewrite the plan with writePlanDocs and rescan.
func multiPhaseWorkspace(t *testing.T, docs ...string) (root, planDir string) {
	t.Helper()
	root = t.TempDir()
	taskDir := filepath.Join(root, "demo", "workspace", "working", "2026", "07", "26", "demo")
	planDir = filepath.Join(taskDir, "plan")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	card := "# Task: Demo epic\n\n" +
		"- **Статус**: active\n- **Старт**: 2026-07-26 · **Завершено**: —\n- **Ціль**: demo goal\n"
	if err := os.WriteFile(filepath.Join(taskDir, "README.md"), []byte(card), 0o644); err != nil {
		t.Fatal(err)
	}
	writePlanDocs(t, planDir, docs...)
	return root, planDir
}

// writePlanDocs (re)writes the plan README's sequencing table plus one doc per
// name, and REMOVES any phase doc not in the list — the "a phase left the plan"
// edit. With no names at all the plan keeps a header-only table and no docs.
func writePlanDocs(t *testing.T, planDir string, docs ...string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("# Demo plan\n\n| # | Phase | Doc | Depends on |\n|---|---|---|---|\n")
	keep := map[string]bool{"README.md": true}
	for i, d := range docs {
		fmt.Fprintf(&b, "| %d | Phase %d | `%s` | — |\n", i+1, i+1, d)
		keep[d] = true
		body := fmt.Sprintf("# Phase %d — Demo\n\n## Acceptance criteria\n- [ ] a\n- [ ] b\n", i+1)
		if err := os.WriteFile(filepath.Join(planDir, d), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(planDir, "README.md"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(planDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() && !keep[e.Name()] {
			if err := os.Remove(filepath.Join(planDir, e.Name())); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// phaseIDsByDoc maps doc basename → epic_phases.id for the sole indexed task.
func phaseIDsByDoc(t *testing.T, db *sql.DB) map[string]int64 {
	t.Helper()
	rows, err := db.Query(`SELECT id, doc_path FROM epic_phases`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var id int64
		var doc string
		if err := rows.Scan(&id, &doc); err != nil {
			t.Fatal(err)
		}
		out[filepath.Base(doc)] = id
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestScanEpicsDeletesPhaseDocRemovedFromPlan: delete-by-exclusion still prunes a
// phase whose doc left the plan, and the survivors keep their row ids (and hence
// their run branches).
func TestScanEpicsDeletesPhaseDocRemovedFromPlan(t *testing.T) {
	db := testDB(t)
	root, planDir := multiPhaseWorkspace(t, "phase-1-a.md", "phase-2-b.md")
	s := New(db, Config{WorkspaceRoot: root})
	if _, err := s.Scan(); err != nil {
		t.Fatalf("scan 1: %v", err)
	}
	before := phaseIDsByDoc(t, db)
	if len(before) != 2 {
		t.Fatalf("phases after first scan = %v, want 2", before)
	}

	writePlanDocs(t, planDir, "phase-1-a.md") // phase 2 leaves the plan
	if _, err := s.Scan(); err != nil {
		t.Fatalf("scan 2: %v", err)
	}

	after := phaseIDsByDoc(t, db)
	if len(after) != 1 {
		t.Fatalf("phases after removal = %v, want only phase-1-a.md", after)
	}
	if after["phase-1-a.md"] != before["phase-1-a.md"] {
		t.Errorf("survivor id = %d, want %d (identity lost)",
			after["phase-1-a.md"], before["phase-1-a.md"])
	}
}

// TestScanEpicsEmptyPlanDeletesAllPhases: with no phases parsed, the exclusion
// guard collapses to an unconditional delete.
func TestScanEpicsEmptyPlanDeletesAllPhases(t *testing.T) {
	db := testDB(t)
	root, planDir := multiPhaseWorkspace(t, "phase-1-a.md", "phase-2-b.md")
	s := New(db, Config{WorkspaceRoot: root})
	if _, err := s.Scan(); err != nil {
		t.Fatalf("scan 1: %v", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM epic_phases`); n != 2 {
		t.Fatalf("phases after first scan = %d, want 2", n)
	}

	writePlanDocs(t, planDir) // every phase doc leaves the plan
	if _, err := s.Scan(); err != nil {
		t.Fatalf("scan 2: %v", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM epic_phases`); n != 0 {
		t.Errorf("phases after emptying the plan = %d, want 0", n)
	}
}

// TestScanEpicsMissingReadmeKeepsPhases: a plan dir that momentarily holds no
// files at all — a `git checkout` of another branch, an `agent-work.sh archive`
// mid-move — parses as zero phases, and the unconditional prune then destroyed
// every phase's run_* state irreversibly. That was the last remaining path that
// lost run state after the delete+re-insert was replaced by an upsert.
func TestScanEpicsMissingReadmeKeepsPhases(t *testing.T) {
	db := testDB(t)
	root, planDir := multiPhaseWorkspace(t, "phase-1-a.md", "phase-2-b.md")
	s := New(db, Config{WorkspaceRoot: root})
	if _, err := s.Scan(); err != nil {
		t.Fatalf("scan 1: %v", err)
	}
	before := phaseIDsByDoc(t, db)
	if len(before) != 2 {
		t.Fatalf("phases after first scan = %v, want 2", before)
	}
	mustExec(t, db, `UPDATE epic_phases SET run_state='done', run_session_uuid='uuid-run-1',
		run_checkboxes_before=0, run_checkboxes_after=2`)

	// The whole plan dir goes empty — README and every phase doc.
	entries, err := os.ReadDir(planDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := os.Remove(filepath.Join(planDir, e.Name())); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Scan(); err != nil {
		t.Fatalf("scan 2: %v", err)
	}

	after := phaseIDsByDoc(t, db)
	if len(after) != 2 {
		t.Fatalf("phases after the plan dir emptied = %v, want both kept", after)
	}
	if after["phase-1-a.md"] != before["phase-1-a.md"] || after["phase-2-b.md"] != before["phase-2-b.md"] {
		t.Errorf("phase identities changed: %v → %v", before, after)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM epic_phases
		WHERE run_state='done' AND run_checkboxes_before=0 AND run_checkboxes_after=2`); n != 2 {
		t.Errorf("phases with intact run_* = %d, want 2", n)
	}
}

// TestScanNotifiesPlanUpdated: Config.NotifyPlan fires exactly once per task
// whose plan hash changed — on first index, NOT on an unchanged rescan, and
// again after a checkbox flip.
func TestScanNotifiesPlanUpdated(t *testing.T) {
	db := testDB(t)
	root, phaseDoc := demoWorkspace(t)

	var fired []int64
	s := New(db, Config{
		WorkspaceRoot: root,
		NotifyPlan:    func(taskID int64) { fired = append(fired, taskID) },
	})

	if _, err := s.Scan(); err != nil {
		t.Fatalf("scan 1: %v", err)
	}
	if len(fired) != 1 {
		t.Fatalf("after first scan fired = %v, want exactly one notification", fired)
	}
	var taskID int64
	if err := db.QueryRow(`SELECT id FROM tasks WHERE external_id='2026-07-26-demo'`).Scan(&taskID); err != nil {
		t.Fatalf("demo task row: %v", err)
	}
	if fired[0] != taskID {
		t.Errorf("notified task = %d, want %d", fired[0], taskID)
	}

	// Unchanged rescan → hash gate holds, no notification.
	if _, err := s.Scan(); err != nil {
		t.Fatalf("scan 2: %v", err)
	}
	if len(fired) != 1 {
		t.Errorf("after unchanged rescan fired = %v, want still one", fired)
	}

	// Checkbox flip → hash changes → one more notification.
	raw, err := os.ReadFile(phaseDoc)
	if err != nil {
		t.Fatal(err)
	}
	flipped := []byte(strings.Replace(string(raw), "- [ ] a", "- [x] a", 1))
	if err := os.WriteFile(phaseDoc, flipped, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Scan(); err != nil {
		t.Fatalf("scan 3: %v", err)
	}
	if len(fired) != 2 || fired[1] != taskID {
		t.Errorf("after checkbox flip fired = %v, want [%d %d]", fired, taskID, taskID)
	}
}

// TestRunWatcherTriggersRescan: with an effectively-disabled ticker (1 h), a
// plan-doc edit must still cause a rescan (and thus NotifyPlan) via the
// fsnotify watcher within the debounce window — the phase-1 latency contract.
func TestRunWatcherTriggersRescan(t *testing.T) {
	db := testDB(t)
	root, phaseDoc := demoWorkspace(t)

	fired := make(chan int64, 16)
	s := New(db, Config{
		WorkspaceRoot:  root,
		RescanInterval: time.Hour, // only fsnotify can trigger the second scan
		NotifyPlan:     func(taskID int64) { fired <- taskID },
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Run(ctx)
	}()
	t.Cleanup(func() { // stop Run BEFORE testDB's cleanup closes the DB
		cancel()
		<-done
	})

	// Initial scan indexes the plan and fires once. NotifyPlan fires
	// mid-scan (from scanEpics) — Run only registers the watches AFTER that
	// scan returns, so the edit below is re-applied on an interval until its
	// rescan lands (same poll-handshake pattern as api's newFrameReader).
	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("initial scan did not notify")
	}

	raw, err := os.ReadFile(phaseDoc)
	if err != nil {
		t.Fatal(err)
	}
	flip := func(on bool) {
		body := string(raw)
		if on {
			body = strings.Replace(body, "- [ ] b", "- [x] b", 1)
		}
		if err := os.WriteFile(phaseDoc, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.After(10 * time.Second)
	on := true
	for {
		flip(on)
		on = !on // alternate so every write really changes the plan hash
		select {
		case <-fired:
			return // fsnotify → debounce → rescan → NotifyPlan: contract holds
		case <-time.After(300 * time.Millisecond):
		case <-deadline:
			t.Fatal("fsnotify-triggered rescan did not fire (watcher dead?)")
		}
	}
}
