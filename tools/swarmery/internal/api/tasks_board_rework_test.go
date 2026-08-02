package api

// Phase 4 boundary test. Auto-move (ingest/capture.go) only ever pushes a card
// FORWARD, in_progress → in_review; getting it back out is entirely the user's,
// and the escape hatch is the plain board PATCH. A card dragged from In Review
// back to In Progress is rework-in-place; dragged back to Todo it re-enters the
// dispatcher's queue and can be run by an agent, which is the intended
// rework-by-agent path.
//
// Neither move has code of its own — they work because legalTransition is
// permissive by default — which is exactly why they need a regression assert.
// The two rules that DO exist (any→archived always legal, done→in_progress
// rejected) sit close enough that a future tightening could take the rework path
// with them and nothing else would notice.

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestBoardReworkOutOfInReview(t *testing.T) {
	srv, _ := testServerWithDB(t) // fixture ingests one project (id 1)

	for _, tc := range []struct {
		name string
		to   string
		why  string
	}{
		{"in_review → in_progress", "in_progress", "rework in place after a review"},
		{"in_review → todo", "todo", "hand the card back to the dispatcher queue"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := postBoard(t, srv.URL,
				`{"projectId":1,"title":"reworkable","prompt":"p","boardColumn":"in_review"}`)
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("create status = %d, want 201", resp.StatusCode)
			}
			var created boardTaskDTO
			json.NewDecoder(resp.Body).Decode(&created)
			resp.Body.Close()
			if created.BoardColumn != "in_review" {
				t.Fatalf("created in %q, want in_review", created.BoardColumn)
			}

			resp = patchBoard(t, srv.URL, created.ID, `{"boardColumn":"`+tc.to+`"}`)
			body := resp.StatusCode
			var moved boardTaskDTO
			json.NewDecoder(resp.Body).Decode(&moved)
			resp.Body.Close()
			if body != http.StatusOK {
				t.Fatalf("PATCH in_review → %s = %d, want 200 (%s)", tc.to, body, tc.why)
			}
			if moved.BoardColumn != tc.to {
				t.Errorf("board column = %q, want %q", moved.BoardColumn, tc.to)
			}
			if moved.ColumnMovedAt == nil {
				t.Errorf("columnMovedAt not stamped on the move out of in_review")
			}
		})
	}
}

// TestLegalTransitionPermitsReworkOutOfInReview pins the same contract at the
// pure-function level, alongside the two rules that are actually enforced — so a
// change to legalTransition fails here first, with the intent spelled out, rather
// than in an HTTP test that just reports a status code.
func TestLegalTransitionPermitsReworkOutOfInReview(t *testing.T) {
	for _, tc := range []struct {
		from, to string
		wantErr  bool
		why      string
	}{
		{"in_review", "in_progress", false, "rework in place is the manual escape hatch from auto-move"},
		{"in_review", "todo", false, "rework by agent: back to the dispatcher queue"},
		{"in_review", "done", false, "accepting the work is the normal exit"},
		{"in_review", "archived", false, "any → archived is always legal"},
		{"in_progress", "in_review", false, "the transition auto-move itself performs"},
		{"done", "in_progress", true, "recovery rehome is dispatcher-owned, not user-facing"},
	} {
		err := legalTransition(tc.from, tc.to)
		if (err != nil) != tc.wantErr {
			t.Errorf("legalTransition(%q, %q) error = %v, wantErr %v — %s",
				tc.from, tc.to, err, tc.wantErr, tc.why)
		}
	}
}
