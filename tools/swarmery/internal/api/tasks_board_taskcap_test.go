package api

import (
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/taskcap"
)

// The capture write path moved to internal/taskcap so internal/ingest can share
// it (api imports ingest, so ingest can never import api). taskcap cannot import
// this package to assert it agrees with the board's own vocabulary — that would
// be the very cycle the move exists to avoid — so the agreement is pinned from
// this side instead.

// TestCapturedPriorityMatchesBoardDefault: a captured card is a suggestion and
// must sit at exactly the same priority a hand-created card defaults to. If the
// board's scale is ever re-tuned, taskcap.NormalPriority has to move with it or
// every session-captured card silently jumps (or loses) queue position.
func TestCapturedPriorityMatchesBoardDefault(t *testing.T) {
	if got, want := taskcap.NormalPriority, priorityLabels["normal"]; got != want {
		t.Errorf("taskcap.NormalPriority = %d, but the board's normal priority is %d", got, want)
	}
}

// TestValidOriginDelegatesToTaskcap: the closed provenance set has exactly one
// definition. A second copy in this package is precisely how the HTTP validation
// and the writer drift apart — one accepting an origin the other rejects.
func TestValidOriginDelegatesToTaskcap(t *testing.T) {
	for _, o := range []string{"manual", "session", "llm", "telepathy", "", "SESSION"} {
		if got, want := validOrigin(o), taskcap.ValidOrigin(o); got != want {
			t.Errorf("validOrigin(%q) = %v, taskcap.ValidOrigin = %v", o, got, want)
		}
	}
	if !validOrigin("manual") || !validOrigin("session") || !validOrigin("llm") {
		t.Error("the three known origins must all validate")
	}
	if validOrigin("telepathy") || validOrigin("") {
		t.Error("unknown origins must not validate")
	}
}

// TestNewBoardExternalIDShape: captured cards are minted by taskcap and manual
// ones by this package, and both must produce the same "T-xxxxxx" shape the
// dispatcher and commit trailers reference.
func TestNewBoardExternalIDShape(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		id, err := newBoardExternalID()
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		if len(id) != 8 || id[:2] != "T-" {
			t.Fatalf("external id = %q, want T- + 6 chars", id)
		}
		for _, c := range id[2:] {
			if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'z') {
				t.Fatalf("external id %q has a non-base36 character %q", id, c)
			}
		}
		seen[id] = true
	}
	if len(seen) < 60 {
		t.Errorf("only %d distinct ids out of 64 — the mint is not random enough", len(seen))
	}
}
