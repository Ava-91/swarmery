package api

import (
	"database/sql"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
)

// worktreesServer returns a live server plus its DB, so each test seeds exactly
// the rows it asserts on.
func worktreesServer(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "wt.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	h, err := NewServer(db, false)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, db
}

// An empty machine must serve empty LISTS, never nulls: the panel maps over
// them, and `null.map` is a blank page rather than an empty state.
func TestWorktrees_EmptySerialisesAsLists(t *testing.T) {
	srv, _ := worktreesServer(t)
	var got map[string]any
	getJSON(t, srv.URL+"/api/worktrees", &got)

	for _, k := range []string{"live", "sweeps"} {
		v, ok := got[k]
		if !ok {
			t.Fatalf("response has no %q key: %v", k, got)
		}
		if v == nil {
			t.Errorf("%s = null, want []", k)
			continue
		}
		if _, ok := v.([]any); !ok {
			t.Errorf("%s is %T, want a list", k, v)
		}
	}
	if _, ok := got["enabled"].(bool); !ok {
		t.Errorf("enabled = %v, want a bool", got["enabled"])
	}
}

func TestWorktrees_SweepsAreNewestFirst(t *testing.T) {
	srv, db := worktreesServer(t)
	if _, err := db.Exec(`INSERT INTO projects (id, path, slug, name, first_seen)
		VALUES (1, '/nowhere-a', 'alpha', 'Alpha', '2026-08-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO worktree_sweeps (ts, project_id, path, verdict, reason) VALUES
		('2026-08-01T00:00:00Z', 1, '/old',   'redundant',    'first'),
		('2026-08-03T00:00:00Z', 1, '/newer', 'skip',         'second'),
		('2026-08-05T00:00:00Z', 1, '/newest','keep-unmerged','third')`); err != nil {
		t.Fatal(err)
	}

	var got struct {
		Sweeps []struct {
			Ts      string `json:"ts"`
			Path    string `json:"path"`
			Verdict string `json:"verdict"`
		} `json:"sweeps"`
	}
	getJSON(t, srv.URL+"/api/worktrees", &got)
	if len(got.Sweeps) != 3 {
		t.Fatalf("sweeps = %d, want 3", len(got.Sweeps))
	}
	if got.Sweeps[0].Path != "/newest" || got.Sweeps[2].Path != "/old" {
		t.Errorf("order = %v, want newest first", got.Sweeps)
	}
}

func TestWorktrees_ProjectScopeNarrowsSweeps(t *testing.T) {
	srv, db := worktreesServer(t)
	if _, err := db.Exec(`INSERT INTO projects (id, path, slug, name, first_seen) VALUES
		(1, '/nowhere-a', 'alpha', 'Alpha', '2026-08-01T00:00:00Z'),
		(2, '/nowhere-b', 'beta',  'Beta',  '2026-08-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO worktree_sweeps (ts, project_id, path, verdict, reason) VALUES
		('2026-08-05T00:00:00Z', 1, '/a', 'redundant', 'alpha row'),
		('2026-08-05T00:00:00Z', 2, '/b', 'redundant', 'beta row')`); err != nil {
		t.Fatal(err)
	}

	var got struct {
		Sweeps []struct {
			Path string `json:"path"`
		} `json:"sweeps"`
	}
	getJSON(t, srv.URL+"/api/worktrees?project=alpha", &got)
	if len(got.Sweeps) != 1 || got.Sweeps[0].Path != "/a" {
		t.Errorf("scoped sweeps = %v, want only alpha's row", got.Sweeps)
	}
}

// A project row whose path is gone from disk (moved, unmounted, deleted) must
// not blank the panel for every OTHER project.
func TestWorktrees_MissingProjectPathIsSkippedNotFatal(t *testing.T) {
	srv, db := worktreesServer(t)
	if _, err := db.Exec(`INSERT INTO projects (id, path, slug, name, first_seen)
		VALUES (1, '/definitely/not/here', 'ghost', 'Ghost', '2026-08-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	getJSON(t, srv.URL+"/api/worktrees", &got) // getJSON fails the test on a non-200
	if live, ok := got["live"].([]any); !ok || len(live) != 0 {
		t.Errorf("live = %v, want an empty list for an unreachable project", got["live"])
	}
}
