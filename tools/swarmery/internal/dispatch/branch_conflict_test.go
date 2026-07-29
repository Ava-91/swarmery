package dispatch

import (
	"fmt"
	"strings"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/worktree"
)

// Splitting ErrBranchExists out of ErrBranchBusy changed what Acquire returns, not what
// dispatch does with it: admission still fails the card and surfaces the reason on the
// task row. These pin that, because the dispatcher only formats the error — a silent
// behaviour change here would strand a card with no explanation.
func TestAdmissionSurfacesBranchConflict(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{
			name: "branch merely exists",
			err:  fmt.Errorf("%w: swarm/T-x (no worktree holds it — merge or delete it)", worktree.ErrBranchExists),
			want: "already exists",
		},
		{
			name: "branch checked out elsewhere",
			err:  fmt.Errorf("%w: /other/place is on swarm/T-x", worktree.ErrBranchBusy),
			want: "busy in another worktree",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := testDB(t)
			s := newTestService(t, db, &stubRunner{}, &stubWt{acquireErr: tc.err})
			id := insertTask(t, db, "T-x", taskOpts{})

			s.Schedule()

			e := taskField(t, db, id, "dispatch_error")
			if !e.Valid || e.String == "" {
				t.Fatal("dispatch_error is empty — the card fails with no explanation")
			}
			if !strings.Contains(e.String, "worktree acquire") {
				t.Errorf("dispatch_error = %q, want it to name the acquisition step", e.String)
			}
			if !strings.Contains(e.String, tc.want) {
				t.Errorf("dispatch_error = %q, want it to carry %q", e.String, tc.want)
			}
		})
	}
}

// The whole point of the split: the message for a branch nothing has checked out must
// not send the operator hunting for a worktree.
func TestAdmissionDoesNotClaimAWorktreeForAMereNameCollision(t *testing.T) {
	db := testDB(t)
	s := newTestService(t, db, &stubRunner{},
		&stubWt{acquireErr: fmt.Errorf("%w: swarm/T-x (no worktree holds it — merge or delete it)",
			worktree.ErrBranchExists)})
	id := insertTask(t, db, "T-x", taskOpts{})

	s.Schedule()

	e := taskField(t, db, id, "dispatch_error")
	if strings.Contains(e.String, "another worktree") {
		t.Errorf("dispatch_error = %q — there is no other worktree to look for", e.String)
	}
}
