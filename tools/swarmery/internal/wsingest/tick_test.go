package wsingest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTickAllCheckboxes(t *testing.T) {
	cases := []struct {
		name     string
		in, want string
		wantN    int
	}{
		{"mixed", "- [x] a\n- [ ] b\n- [ ] c\n", "- [x] a\n- [x] b\n- [x] c\n", 2},
		{"all done already", "- [x] a\n- [X] b\n", "- [x] a\n- [X] b\n", 0},
		{"no boxes", "## Acceptance\n\nprose only\n", "## Acceptance\n\nprose only\n", 0},
		{"star + indent preserved", "* [ ] a\n  - [ ] nested\n", "* [x] a\n  - [x] nested\n", 2},
		{"non-boxes untouched", "- [] a\n- [y] b\n- [ ] real\n", "- [] a\n- [y] b\n- [x] real\n", 1},
		{
			// An auto-tick must not rewrite a quoted template: the doc explains what
			// an EMPTY checklist looks like, and a ticked example documents a lie.
			name:  "fenced example survives a tick",
			in:    "- [ ] real\n\n```markdown\n- [ ] template\n```\n",
			want:  "- [x] real\n\n```markdown\n- [ ] template\n```\n",
			wantN: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, n := tickAllCheckboxes(c.in)
			if got != c.want || n != c.wantN {
				t.Errorf("tickAllCheckboxes(%q) = %q, %d; want %q, %d", c.in, got, n, c.want, c.wantN)
			}
			// A tick must never change the total the scanner counts.
			_, totBefore := CountCheckboxes(c.in)
			doneAfter, totAfter := CountCheckboxes(got)
			if totAfter != totBefore || doneAfter != totAfter {
				t.Errorf("after tick: %d/%d done, want %d/%d", doneAfter, totAfter, totBefore, totBefore)
			}
		})
	}
}

func TestTickPhaseChecklist(t *testing.T) {
	db := testDB(t)
	mustExec(t, db, `INSERT INTO projects (id, path, slug, first_seen)
		VALUES (1, '/repo/p', 'p', '2026-01-01T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO tasks (id, project_id, title, prompt, status, created_at, source, external_id)
		VALUES (10, 1, 'Epic', 'goal', 'running', '2026-07-24T00:00:00Z', 'workspace', 'ws-epic'),
		       (64, 1, 'Phase 1', 'doc', 'done', '2026-07-26T00:00:00Z', 'queue', 'T-aaaaaa')`)

	doc := filepath.Join(t.TempDir(), "phase-1-schema.md")
	body := "# Phase 1\n\n## Acceptance criteria\n- [x] a\n- [ ] b\n- [ ] c\n"
	if err := os.WriteFile(doc, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO epic_phases
		(workspace_task_id, seq, name, doc_path, depends_on, checkboxes_total, checkboxes_done,
		 activated_at, activated_board_task_id)
		VALUES (10, 1, 'Phase 1', ?, '[]', 3, 1, '2026-07-26T00:00:00Z', 64)`, doc)

	n, err := TickPhaseChecklist(db, 64)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if n != 2 {
		t.Errorf("ticked = %d, want 2", n)
	}
	got, err := os.ReadFile(doc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "[ ]") {
		t.Errorf("doc still has unchecked boxes:\n%s", got)
	}

	// Second call is a no-op.
	if n, err := TickPhaseChecklist(db, 64); err != nil || n != 0 {
		t.Errorf("second tick = %d, %v; want 0, nil", n, err)
	}
	// A board task not minted from a phase is a no-op, not an error.
	if n, err := TickPhaseChecklist(db, 9999); err != nil || n != 0 {
		t.Errorf("unlinked task tick = %d, %v; want 0, nil", n, err)
	}
}

func TestTickPhaseChecklistUnreadableDoc(t *testing.T) {
	db := testDB(t)
	mustExec(t, db, `INSERT INTO projects (id, path, slug, first_seen)
		VALUES (1, '/repo/p', 'p', '2026-01-01T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO tasks (id, project_id, title, prompt, status, created_at, source, external_id)
		VALUES (10, 1, 'Epic', 'goal', 'running', '2026-07-24T00:00:00Z', 'workspace', 'ws-epic2')`)
	mustExec(t, db, `INSERT INTO epic_phases
		(workspace_task_id, seq, name, doc_path, depends_on, activated_at, activated_board_task_id)
		VALUES (10, 1, 'Phase 1', '/nonexistent/phase-1.md', '[]', '2026-07-26T00:00:00Z', 77)`)
	if _, err := TickPhaseChecklist(db, 77); err == nil {
		t.Error("want error for unreadable doc, got nil")
	}
}
