package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
)

// ctxSession is the thin projection this test needs from a session row.
type ctxSession struct {
	ID            int64  `json:"id"`
	ContextTokens *int64 `json:"contextTokens"`
}

type ctxEnvelope struct {
	Sessions []ctxSession `json:"sessions"`
}

// TestSessionContextTokens locks the context-occupancy signal: a session's
// contextTokens is the LAST assistant turn's input footprint
// (tokens_in + cache_read + cache_write), not a sum over turns. That footprint
// ≈ how full the model's context window is — the fat-session root cause. A
// session with no priced assistant turn reports null.
func TestSessionContextTokens(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "ctx.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("exec: %v\n%s", err, q)
		}
	}

	mustExec(`INSERT INTO projects (id, path, slug, name, first_seen) VALUES
		(1, '/work/p', '-work-p', 'P', '2026-07-01T00:00:00Z')`)
	// Session 1: three assistant turns; the last carries a big context footprint.
	mustExec(`INSERT INTO sessions (id, project_id, session_uuid, status, started_at) VALUES
		(1, 1, 'u1', 'active', '2026-07-27T10:00:00.000Z')`)
	mustExec(`INSERT INTO turns (session_id, seq, role, started_at, tokens_in, tokens_out, tokens_cache_read, tokens_cache_write, cost_usd) VALUES
		(1, 1, 'assistant', '2026-07-27T10:00:00Z', 5, 10, 1000, 100, 0.1),
		(1, 2, 'user',      '2026-07-27T10:01:00Z', 0, 0, 0, 0, 0),
		(1, 3, 'assistant', '2026-07-27T10:02:00Z', 2, 20, 400000, 500, 0.5)`)
	// Session 2: no assistant turn with usage → null context.
	mustExec(`INSERT INTO sessions (id, project_id, session_uuid, status, started_at) VALUES
		(2, 1, 'u2', 'active', '2026-07-27T09:00:00.000Z')`)

	h, err := NewServer(db, false)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL + "/api/sessions")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()
	var env ctxEnvelope
	if err := json.NewDecoder(res.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}

	got := map[int64]*int64{}
	for _, s := range env.Sessions {
		got[s.ID] = s.ContextTokens
	}
	if c := got[1]; c == nil || *c != 400502 {
		t.Errorf("session 1 contextTokens = %v, want 400502 (last assistant turn: 2+400000+500)", c)
	}
	if c := got[2]; c != nil {
		t.Errorf("session 2 contextTokens = %v, want nil (no priced turn)", c)
	}
}
