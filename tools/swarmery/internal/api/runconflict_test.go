package api

import (
	"errors"
	"fmt"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/worktree"
)

// TestWorktreeConflictMapsEverySentinel closes the class the hard way. An unmapped
// sentinel is not a cosmetic gap: it falls through to the generic arm and reaches
// the UI as an opaque 500 carrying git's raw sentence. That is precisely how a
// plan-run retry spent four attempts acting on a diagnosis naming the wrong blocker
// (2026-07-30) — ErrBranchExists had never been mapped. Adding a sentinel to
// internal/worktree without a code here now fails HERE, not in production.
func TestWorktreeConflictMapsEverySentinel(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{worktree.ErrBranchCheckedOut, codeBranchCheckedOut},
		{worktree.ErrBranchIsHead, codeBranchIsHead},
		{worktree.ErrRefusedBranch, codeBranchRefused},
		{worktree.ErrBranchBusy, codeBranchBusy},
		{worktree.ErrDetachedHead, codeDetachedHead},
		{worktree.ErrBranchExists, codeBranchExists},
		{worktree.ErrPathOccupied, codePathOccupied},
	} {
		// Wrapped, the way every real caller returns it — planrun.Start wraps acquire
		// failures ("worktree acquire: %w"), so an errors.Is-based mapping is the only
		// one that keeps working.
		code, msg, ok := worktreeConflict(fmt.Errorf("worktree acquire: %w", tc.err))
		switch {
		case !ok:
			t.Errorf("%v is unmapped — it would surface as an opaque 500", tc.err)
		case code != tc.want:
			t.Errorf("%v → code %q, want %q", tc.err, code, tc.want)
		case msg == "":
			t.Errorf("%v maps to an empty message — the code is for the client, the message is for the human", tc.err)
		}
	}
}

// TestWorktreeConflictCodesAreDistinct: two blockers with different remedies must
// not share a discriminator. ErrBranchExists (merge or delete the branch) and
// ErrPathOccupied (free the path) collapsing into one code is the original defect
// one layer up.
func TestWorktreeConflictCodesAreDistinct(t *testing.T) {
	seen := map[string]error{}
	for _, err := range []error{
		worktree.ErrBranchCheckedOut,
		worktree.ErrBranchIsHead,
		worktree.ErrRefusedBranch,
		worktree.ErrBranchBusy,
		worktree.ErrDetachedHead,
		worktree.ErrBranchExists,
		worktree.ErrPathOccupied,
	} {
		code, _, ok := worktreeConflict(err)
		if !ok {
			continue // reported by TestWorktreeConflictMapsEverySentinel
		}
		if prev, dup := seen[code]; dup {
			t.Errorf("code %q maps both %v and %v — distinct remedies need distinct codes", code, prev, err)
		}
		seen[code] = err
	}
}

func TestWorktreeConflictIgnoresForeignErrors(t *testing.T) {
	if _, _, ok := worktreeConflict(errors.New("database is locked")); ok {
		t.Error("an unrelated failure must not be dressed up as a worktree conflict (409 says 'retry differently')")
	}
	if _, _, ok := worktreeConflict(nil); ok {
		t.Error("nil is not a conflict")
	}
}
