package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
)

func handoffTestServer(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(
		`INSERT INTO projects (path, slug, name, first_seen, last_activity)
		 VALUES ('/tmp/hp', '-tmp-hp', 'hp', '2026-07-13T00:00:00Z', '2026-07-13T00:00:00Z')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO sessions (id, project_id, session_uuid, status, started_at, source)
		 VALUES (1, 1, 'u-ho-1', 'active', '2026-07-13T00:00:00Z', 'jsonl')`); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	h, err := NewServer(db, false)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, db
}

func TestGetSessionHandoff200(t *testing.T) {
	srv, db := handoffTestServer(t)

	// Write a handoff file and record the row.
	dir := t.TempDir()
	path := filepath.Join(dir, "u-ho-1.md")
	body := "# Handoff: finish it\n## Next step\n- run make test\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO handoffs (session_id, path, context_tokens, created_at)
		VALUES (1, ?, 160000, '2026-07-21T12:00:00Z')`, path); err != nil {
		t.Fatalf("insert handoff: %v", err)
	}

	// By numeric id and by uuid both resolve.
	for _, idArg := range []string{"1", "u-ho-1"} {
		resp, err := http.Get(srv.URL + "/api/sessions/" + idArg + "/handoff")
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			t.Fatalf("id=%s status = %d, want 200", idArg, resp.StatusCode)
		}
		var got struct {
			Markdown  string `json:"markdown"`
			Path      string `json:"path"`
			CreatedAt string `json:"createdAt"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			resp.Body.Close()
			t.Fatalf("decode: %v", err)
		}
		resp.Body.Close()
		if got.Markdown != body {
			t.Errorf("id=%s markdown = %q, want %q", idArg, got.Markdown, body)
		}
		if got.Path != path {
			t.Errorf("id=%s path = %q, want %q", idArg, got.Path, path)
		}
		if got.CreatedAt != "2026-07-21T12:00:00Z" {
			t.Errorf("id=%s createdAt = %q", idArg, got.CreatedAt)
		}
	}
}

func TestGetSessionHandoff404NoRow(t *testing.T) {
	srv, _ := handoffTestServer(t)
	resp, err := http.Get(srv.URL + "/api/sessions/1/handoff")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a session with no handoff", resp.StatusCode)
	}
}

func TestGetSessionHandoff404FileMissing(t *testing.T) {
	srv, db := handoffTestServer(t)
	// Row points at a path that does not exist on disk.
	if _, err := db.Exec(`INSERT INTO handoffs (session_id, path, context_tokens, created_at)
		VALUES (1, '/nonexistent/gone.md', 160000, '2026-07-21T12:00:00Z')`); err != nil {
		t.Fatalf("insert handoff: %v", err)
	}
	resp, err := http.Get(srv.URL + "/api/sessions/1/handoff")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when the file is gone", resp.StatusCode)
	}
}
