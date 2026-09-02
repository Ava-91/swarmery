package api

import (
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
)

// TestSessionsCursorWalkPastEmptyStartedAt pins the pagination half of the
// phantom-session bug: sessions.started_at is NOT NULL but may be ” (a row
// minted before any record carried a timestamp), ” sorts LAST under
// `ORDER BY started_at DESC`, so a page boundary can land exactly on it. The
// cursor encoder then emitted "|<id>" and the decoder rejected it as
// malformed — the Sessions page answered "load more" with a hard 400 and the
// rest of the list became unreachable.
//
// The walk below crosses that boundary twice (limit=2 over 3 timestamped and
// 2 empty-started rows) and must return every row exactly once.
func TestSessionsCursorWalkPastEmptyStartedAt(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "empty-start.db"))
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
	for i := 1; i <= 3; i++ {
		mustExec(fmt.Sprintf(`INSERT INTO sessions (id, project_id, session_uuid, status, started_at)
			VALUES (%d, 1, 'u%d', 'completed', '2026-07-%02dT10:00:00.000Z')`, i, i, i))
	}
	// Two timestamp-less rows — the tail of the DESC ordering.
	mustExec(`INSERT INTO sessions (id, project_id, session_uuid, status, started_at) VALUES
		(4, 1, 'u4', 'completed', ''),
		(5, 1, 'u5', 'completed', '')`)

	h, err := NewServer(db, false)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	var got []int64
	url := srv.URL + "/api/sessions?limit=2"
	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatal("cursor walk did not terminate")
		}
		var page pageEnvelope
		getJSON(t, url, &page)
		for _, s := range page.Sessions {
			got = append(got, s.ID)
		}
		if page.NextCursor == nil {
			break
		}
		url = srv.URL + "/api/sessions?limit=2&cursor=" + *page.NextCursor
	}

	want := []int64{3, 2, 1, 5, 4} // timestamped first (DESC), then '' by id DESC
	if len(got) != len(want) {
		t.Fatalf("walked ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("walked ids = %v, want %v", got, want)
		}
	}
}

// TestDecodeSessionCursorEmptyStartedAt is the unit-level guard for the same
// contract: every value the ORDER BY can produce must round-trip, including
// the empty started_at. A token with no '|' separator stays a client error.
func TestDecodeSessionCursorEmptyStartedAt(t *testing.T) {
	startedAt, id, err := decodeSessionCursor(encodeSessionCursor("", 947))
	if err != nil {
		t.Fatalf("empty started_at cursor: err = %v, want nil", err)
	}
	if startedAt != "" || id != 947 {
		t.Fatalf("round-trip = (%q, %d), want (\"\", 947)", startedAt, id)
	}
	if _, _, err := decodeSessionCursor("bm8tc2VwYXJhdG9y"); err == nil {
		t.Error("cursor without a '|' separator: err = nil, want an error")
	}
}
