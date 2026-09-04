package taskcap_test

import (
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/taskcap"
)

// TestStaleAfterMirrorsSweepPredicate: a card the sweeper would retire reports
// created_at + ttl; every card outside the sweep's WHERE clause reports nil.
// The four rows below are the four conjuncts of StaleInboxWhere, one flipped
// at a time off the same eligible base.
func TestStaleAfterMirrorsSweepPredicate(t *testing.T) {
	const ttl = 336 * time.Hour
	created := "2026-08-10T12:00:00.000Z"
	wt := "/wt/p/T-1"
	base := taskcap.BoardTaskRow{Source: "queue", BoardColumn: "triage", Origin: "session", CreatedAt: created}

	want := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	if got := taskcap.StaleAfter(base, ttl); got == nil || !got.Equal(want) {
		t.Fatalf("eligible card: StaleAfter = %v, want %v", got, want)
	}
	llm := base
	llm.Origin = "llm"
	if got := taskcap.StaleAfter(llm, ttl); got == nil || !got.Equal(want) {
		t.Errorf("llm card: StaleAfter = %v, want %v", got, want)
	}

	cases := map[string]func(r *taskcap.BoardTaskRow){
		"manual origin":    func(r *taskcap.BoardTaskRow) { r.Origin = "manual" },
		"accepted to todo": func(r *taskcap.BoardTaskRow) { r.BoardColumn = "todo" },
		"dispatcher-owned": func(r *taskcap.BoardTaskRow) { r.WorktreePath = &wt },
		"workspace row":    func(r *taskcap.BoardTaskRow) { r.Source = "workspace" },
		"verify-fix":       func(r *taskcap.BoardTaskRow) { r.Origin = "verify-fix" },
	}
	for name, mutate := range cases {
		r := base
		mutate(&r)
		if got := taskcap.StaleAfter(r, ttl); got != nil {
			t.Errorf("%s: StaleAfter = %v, want nil", name, got)
		}
	}

	// The sweep's off switch: no TTL, no expiry.
	if got := taskcap.StaleAfter(base, 0); got != nil {
		t.Errorf("ttl=0: StaleAfter = %v, want nil", got)
	}
}

// TestStaleAfterUsesIdleClock: a captured card the user dragged out and back
// restarts its idle clock at column_moved_at — the same COALESCE the sweep's
// SQL applies — and an unparseable stamp yields nil rather than a guess.
func TestStaleAfterUsesIdleClock(t *testing.T) {
	moved := "2026-08-20T00:00:00.000Z"
	r := taskcap.BoardTaskRow{
		Source: "queue", BoardColumn: "triage", Origin: "session",
		CreatedAt: "2026-08-10T12:00:00.000Z", ColumnMovedAt: &moved,
	}
	want := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	if got := taskcap.StaleAfter(r, 24*time.Hour); got == nil || !got.Equal(want) {
		t.Errorf("moved card: StaleAfter = %v, want %v", got, want)
	}
	rfc := r
	rfc.ColumnMovedAt = nil
	rfc.CreatedAt = "2026-08-10T12:00:00Z"
	if got := taskcap.StaleAfter(rfc, 24*time.Hour); got == nil || !got.Equal(time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("rfc3339 created_at: StaleAfter = %v", got)
	}
	bad := r
	bad.ColumnMovedAt = nil
	bad.CreatedAt = "yesterday"
	if got := taskcap.StaleAfter(bad, 24*time.Hour); got != nil {
		t.Errorf("unparseable stamp: StaleAfter = %v, want nil", got)
	}
}
