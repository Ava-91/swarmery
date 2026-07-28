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

// A minimal, well-formed transcript: one assistant record with a Bash tool_use
// + usage, and its tool_result carrier. Bash content "z"*40 → raw 42 → est 10.
const ctxHogsTranscript = `{"type":"assistant","uuid":"a1","timestamp":"2026-07-27T10:00:00.000Z","sessionId":"u-ch-1","message":{"id":"m1","role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}],"usage":{"cache_creation_input_tokens":500}}}
{"type":"user","uuid":"u1","timestamp":"2026-07-27T10:00:01.000Z","sessionId":"u-ch-1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"}]}}
`

func ctxHogsTestServer(t *testing.T) (*httptest.Server, *sql.DB, string) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "ctxhogs.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(
		`INSERT INTO projects (path, slug, name, first_seen, last_activity)
		 VALUES ('/tmp/ch', '-tmp-ch', 'ch', '2026-07-27T00:00:00Z', '2026-07-27T00:00:00Z')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO sessions (id, project_id, session_uuid, status, started_at, source)
		 VALUES (1, 1, 'u-ch-1', 'active', '2026-07-27T00:00:00Z', 'jsonl')`); err != nil {
		t.Fatalf("insert session: %v", err)
	}

	// Point the endpoint at a temp projects root and isolate the global from
	// other tests by restoring it on cleanup.
	root := t.TempDir()
	prev := transcriptsRoot
	AttachProjectsRoot(root)
	t.Cleanup(func() { AttachProjectsRoot(prev) })

	h, err := NewServer(db, false)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, db, root
}

func writeCtxHogsTranscript(t *testing.T, root, uuid string) {
	t.Helper()
	dir := filepath.Join(root, "-tmp-ch")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, uuid+".jsonl"), []byte(ctxHogsTranscript), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

func TestGetSessionContextHogs200(t *testing.T) {
	srv, _, root := ctxHogsTestServer(t)
	writeCtxHogsTranscript(t, root, "u-ch-1")

	// By numeric id and by uuid both resolve.
	for _, idArg := range []string{"1", "u-ch-1"} {
		resp, err := http.Get(srv.URL + "/api/sessions/" + idArg + "/context-hogs")
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			t.Fatalf("id=%s status = %d, want 200", idArg, resp.StatusCode)
		}
		var got struct {
			Tools []struct {
				Name      string `json:"name"`
				Calls     int    `json:"calls"`
				EstTokens int64  `json:"estTokens"`
			} `json:"tools"`
			Turns []struct {
				Seq        int   `json:"seq"`
				CacheWrite int64 `json:"cacheWrite"`
			} `json:"turns"`
			TotalEst    int64 `json:"totalEst"`
			Uninspected int   `json:"uninspected"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			resp.Body.Close()
			t.Fatalf("decode: %v", err)
		}
		resp.Body.Close()

		if len(got.Tools) != 1 || got.Tools[0].Name != "Bash" ||
			got.Tools[0].Calls != 1 || got.Tools[0].EstTokens != 10 {
			t.Errorf("id=%s tools = %+v, want one Bash{Calls:1,EstTokens:10}", idArg, got.Tools)
		}
		if len(got.Turns) != 1 || got.Turns[0].CacheWrite != 500 {
			t.Errorf("id=%s turns = %+v, want one {Seq:0,CacheWrite:500}", idArg, got.Turns)
		}
		if got.TotalEst != 10 {
			t.Errorf("id=%s totalEst = %d, want 10", idArg, got.TotalEst)
		}
	}
}

func TestGetSessionContextHogs404NoTranscript(t *testing.T) {
	srv, _, _ := ctxHogsTestServer(t)
	// Session row exists, but no transcript file was written under the root.
	resp, err := http.Get(srv.URL + "/api/sessions/1/context-hogs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when no transcript on disk", resp.StatusCode)
	}
}

func TestGetSessionContextHogs404UnknownSession(t *testing.T) {
	srv, _, _ := ctxHogsTestServer(t)
	resp, err := http.Get(srv.URL + "/api/sessions/999/context-hogs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for an unknown session", resp.StatusCode)
	}
}
