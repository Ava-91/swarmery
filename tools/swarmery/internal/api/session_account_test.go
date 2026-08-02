package api

// Per-subscription account dimension (migration 0047): the DTO field, the
// ?account= filter with its 'default' ↔ '' synonym, and the by=account
// breakdown pivot.

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
)

// accountServer plants four sessions across three accounts, including both
// spellings of the default one ('' — as written before 0047 and by the hooks
// channel — and an explicitly stamped 'default'), each with one priced turn so
// the breakdown has something to rank.
func accountServer(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "accounts.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	now := time.Now().UTC()
	at := func(hoursAgo int) string {
		return now.Add(-time.Duration(hoursAgo) * time.Hour).Format("2006-01-02T15:04:05.000Z")
	}

	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("exec: %v\n%s", err, q)
		}
	}

	mustExec(`INSERT INTO projects (id, path, slug, name, first_seen) VALUES
		(1, '/work/alpha', '-work-alpha', 'Alpha', ?)`, at(48))
	mustExec(`INSERT INTO sessions (id, project_id, session_uuid, status, started_at, source, account) VALUES
		(1, 1, 'u-stamped-default', 'completed', ?, 'jsonl', 'default'),
		(2, 1, 'u-legacy-blank',    'completed', ?, 'jsonl', ''),
		(3, 1, 'u-nabu-org',          'completed', ?, 'jsonl', 'nabu-org'),
		(4, 1, 'u-science',         'completed', ?, 'jsonl', 'science')`,
		at(4), at(3), at(2), at(1))
	mustExec(`INSERT INTO turns (session_id, seq, role, model, started_at, tokens_in, tokens_out, cost_usd) VALUES
		(1, 0, 'assistant', 'm', ?, 100, 10, 0.10),
		(2, 0, 'assistant', 'm', ?, 200, 20, 0.20),
		(3, 0, 'assistant', 'm', ?, 400, 40, 0.40),
		(4, 0, 'assistant', 'm', ?, 800, 80, 0.80)`, at(4), at(3), at(2), at(1))

	h, err := NewServer(db, false)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, db
}

// sessionsPage is the GET /api/sessions envelope, projected onto the fields
// this file asserts about.
type accountSessionsPage struct {
	Sessions []struct {
		SessionUUID string `json:"sessionUuid"`
		Account     string `json:"account"`
	} `json:"sessions"`
}

// TestSessionDTOCarriesAccount: the list and the detail response both expose
// the stamped subscription, including the '' that means "stock account".
func TestSessionDTOCarriesAccount(t *testing.T) {
	srv, _ := accountServer(t)

	var page accountSessionsPage
	getJSON(t, srv.URL+"/api/sessions", &page)
	got := map[string]string{}
	for _, s := range page.Sessions {
		got[s.SessionUUID] = s.Account
	}
	for uuid, want := range map[string]string{
		"u-stamped-default": "default",
		"u-legacy-blank":    "",
		"u-nabu-org":          "nabu-org",
		"u-science":         "science",
	} {
		if got[uuid] != want {
			t.Errorf("list: account of %s = %q, want %q", uuid, got[uuid], want)
		}
	}

	var detail struct {
		Account string `json:"account"`
	}
	getJSON(t, srv.URL+"/api/sessions/u-nabu-org", &detail)
	if detail.Account != "nabu-org" {
		t.Errorf("detail: account = %q, want nabu-org", detail.Account)
	}
}

// TestSessionsAccountFilter: exact match per account, and the ONE synonym —
// 'default' also selects the '' rows, because both spellings mean the stock
// subscription (pre-0047 rows and hook-minted sessions carry '').
func TestSessionsAccountFilter(t *testing.T) {
	srv, _ := accountServer(t)

	for _, tc := range []struct {
		account string
		want    []string
	}{
		{"default", []string{"u-stamped-default", "u-legacy-blank"}},
		{"nabu-org", []string{"u-nabu-org"}},
		{"science", []string{"u-science"}},
		{"nope", nil},
	} {
		t.Run(tc.account, func(t *testing.T) {
			var page accountSessionsPage
			getJSON(t, srv.URL+"/api/sessions?account="+tc.account, &page)
			if len(page.Sessions) != len(tc.want) {
				t.Fatalf("?account=%s returned %d sessions, want %d",
					tc.account, len(page.Sessions), len(tc.want))
			}
			seen := map[string]bool{}
			for _, s := range page.Sessions {
				seen[s.SessionUUID] = true
			}
			for _, uuid := range tc.want {
				if !seen[uuid] {
					t.Errorf("?account=%s did not return %s", tc.account, uuid)
				}
			}
		})
	}

	// No account param → every row, unfiltered.
	var all accountSessionsPage
	getJSON(t, srv.URL+"/api/sessions", &all)
	if len(all.Sessions) != 4 {
		t.Errorf("unfiltered list = %d sessions, want 4", len(all.Sessions))
	}
}

// TestBreakdownByAccount: the per-subscription rollup ranks accounts by cost,
// folds '' into the 'default' row, and keys/labels every row with the account.
func TestBreakdownByAccount(t *testing.T) {
	srv, _ := accountServer(t)

	var rows []struct {
		Key       string   `json:"key"`
		Name      string   `json:"name"`
		CostUSD   *float64 `json:"cost_usd"`
		TokensIn  *int64   `json:"tokens_in"`
		TokensOut *int64   `json:"tokens_out"`
		Sessions  int64    `json:"sessions"`
	}
	getJSON(t, srv.URL+"/api/stats/breakdown?by=account", &rows)
	if len(rows) != 3 {
		t.Fatalf("breakdown rows = %d, want 3 (default, nabu-org, science)", len(rows))
	}
	// Ranked by cost DESC: science 0.80, nabu-org 0.40, default 0.10+0.20.
	if rows[0].Key != "science" || rows[1].Key != "nabu-org" || rows[2].Key != "default" {
		t.Errorf("ranking = %s/%s/%s, want science/nabu-org/default",
			rows[0].Key, rows[1].Key, rows[2].Key)
	}
	for _, r := range rows {
		if r.Name != r.Key {
			t.Errorf("row %s: name = %q, want the key itself", r.Key, r.Name)
		}
	}
	// The 'default' row is the SUM of the stamped and the blank session.
	def := rows[2]
	if def.Sessions != 2 {
		t.Errorf("default sessions = %d, want 2 ('' folded in)", def.Sessions)
	}
	if def.CostUSD == nil || *def.CostUSD < 0.2999 || *def.CostUSD > 0.3001 {
		t.Errorf("default cost = %v, want 0.30", def.CostUSD)
	}
	if def.TokensIn == nil || *def.TokensIn != 300 {
		t.Errorf("default tokens_in = %v, want 300", def.TokensIn)
	}
	if def.TokensOut == nil || *def.TokensOut != 30 {
		t.Errorf("default tokens_out = %v, want 30", def.TokensOut)
	}
}

// TestBreakdownRejectsUnknownDimension: the pivot switch stays closed, and its
// error names account alongside the pre-existing dimensions.
func TestBreakdownRejectsUnknownDimension(t *testing.T) {
	srv, _ := accountServer(t)
	resp, err := http.Get(srv.URL + "/api/stats/breakdown?by=subscription")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}
