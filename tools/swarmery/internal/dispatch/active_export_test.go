package dispatch

// IsActive is the only in-memory dispatcher state the api layer reads (the board
// review exits refuse to re-queue or archive a card the dispatcher still owns).
// The exported wrapper is what makes that guard possible from outside this
// package, so it gets a test of its own: an accessor that silently stopped
// tracking the set would turn every one of those guards into a no-op, and no
// api-package test can reach markActive to notice.

import (
	"testing"
)

func TestIsActiveMirrorsTheInMemorySet(t *testing.T) {
	db := testDB(t)
	s := newTestService(t, db, &stubRunner{}, &stubWt{})

	if s.IsActive(7) {
		t.Error("IsActive(7) = true before anything was marked")
	}
	s.markActive(7)
	if !s.IsActive(7) {
		t.Error("IsActive(7) = false while the run is marked active")
	}
	// A different task must not be swept up by the first one.
	if s.IsActive(8) {
		t.Error("IsActive(8) = true, want false")
	}
	s.clearActive(7)
	if s.IsActive(7) {
		t.Error("IsActive(7) = true after the run was cleared")
	}
}
