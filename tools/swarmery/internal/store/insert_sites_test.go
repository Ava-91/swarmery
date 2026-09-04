package store

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// allowedTaskInsertSites is the closed set of non-test Go files under internal/
// that may write a tasks row with a raw INSERT, each with the reason it is
// allowed to. Everything else must go through InsertBoardTask — a hand-written
// row is how a card with no title, an unknown origin, or no provenance reaches
// the board. The list is checked in BOTH directions: a new site fails, and so
// does an entry whose file no longer inserts (a stale exception is an exception
// nobody is looking at).
var allowedTaskInsertSites = map[string]string{
	"store/board_tasks.go": "the constructor itself — the one place a board card is minted",
	"wsingest/wsingest.go": "source='workspace' projection of on-disk plan cards, upserted on " +
		"(workspace_id, external_id) every scan; owned by the disk, not the board",
}

// taskInsertRe matches a raw insert into the tasks table, case-insensitively
// and across the whitespace a multi-line SQL literal may carry. The trailing
// word boundary keeps task_sessions / task_artifacts out.
var taskInsertRe = regexp.MustCompile(`(?i)INSERT\s+INTO\s+tasks\b`)

// scanTaskInsertSites walks root (the internal/ tree) and returns the set of
// non-test .go files, as root-relative slash paths, that contain a raw insert.
func scanTaskInsertSites(root string) (map[string]bool, error) {
	found := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if taskInsertRe.Match(body) {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			found[filepath.ToSlash(rel)] = true
		}
		return nil
	})
	return found, err
}

// auditTaskInsertSites compares what the scan found against the allowlist and
// returns one line per violation, sorted, or nil when the two agree exactly.
func auditTaskInsertSites(found map[string]bool, allow map[string]string) []string {
	var out []string
	for f := range found {
		if _, ok := allow[f]; !ok {
			out = append(out, f+": raw insert into tasks outside the allowlist — use store.InsertBoardTask")
		}
	}
	for f := range allow {
		if !found[f] {
			out = append(out, f+": stale allowlist entry — the file no longer inserts into tasks")
		}
	}
	sort.Strings(out)
	return out
}

// TestTaskInsertSitesAreAllowlisted is the ratchet over the real tree.
func TestTaskInsertSitesAreAllowlisted(t *testing.T) {
	found, err := scanTaskInsertSites("..")
	if err != nil {
		t.Fatalf("scan internal/: %v", err)
	}
	if v := auditTaskInsertSites(found, allowedTaskInsertSites); len(v) != 0 {
		t.Fatalf("tasks insert-site audit failed:\n  %s", strings.Join(v, "\n  "))
	}
}

// TestTaskInsertSitesAuditCatchesBothDrifts proves the audit is not vacuous:
// a synthetic new site and a synthetic stale entry each produce a violation.
func TestTaskInsertSitesAuditCatchesBothDrifts(t *testing.T) {
	found, err := scanTaskInsertSites("..")
	if err != nil {
		t.Fatal(err)
	}

	// A new raw insert somewhere in the api layer.
	withNew := map[string]bool{"api/rogue.go": true}
	for f := range found {
		withNew[f] = true
	}
	v := auditTaskInsertSites(withNew, allowedTaskInsertSites)
	if len(v) != 1 || !strings.HasPrefix(v[0], "api/rogue.go: raw insert") {
		t.Errorf("new site: violations = %q, want exactly the rogue file", v)
	}

	// An allowlist entry pointing at a file that no longer inserts.
	withStale := map[string]string{"ghost/ghost.go": "used to insert"}
	for f, why := range allowedTaskInsertSites {
		withStale[f] = why
	}
	v = auditTaskInsertSites(found, withStale)
	if len(v) != 1 || !strings.HasPrefix(v[0], "ghost/ghost.go: stale allowlist entry") {
		t.Errorf("stale entry: violations = %q, want exactly the ghost entry", v)
	}

	// The scanner itself must see through case and line breaks: the real sites
	// spell the statement across lines and in different cases.
	for _, src := range []string{"INSERT INTO tasks (", "insert into tasks(", "INSERT\n\t\tINTO   tasks ("} {
		if !taskInsertRe.MatchString(src) {
			t.Errorf("regexp missed %q", src)
		}
	}
	for _, src := range []string{"INSERT INTO task_sessions (", "INSERT INTO task_artifacts("} {
		if taskInsertRe.MatchString(src) {
			t.Errorf("regexp wrongly matched %q", src)
		}
	}
}
