package api

// Run-conflict vocabulary shared by the phase-run and plan-run endpoints.
//
// POST …/run and DELETE …/branch answer 409 for a dozen different reasons, and
// they used to arrive as three different body shapes ({error}, {error,unmetDeps},
// {error,branch,commitsAhead}) that the client had to tell apart by sniffing
// which fields were present. That works until a new case adds a field, at which
// point every existing client silently mis-classifies it. A stable `code`
// discriminator makes the case explicit; the pre-existing fields all stay, so
// nothing that reads them breaks.
//
// The mapping from worktree sentinels to codes lives here too (worktreeConflict)
// rather than being spelled out in each switch: the phase and plan surfaces must
// answer the same condition with the same code, and four hand-maintained copies
// is exactly how they would drift apart.

import (
	"errors"
	"net/http"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/worktree"
)

// 409 discriminators. Stable wire values — the web client switches on these, so
// they are part of the API contract and must not be reworded casually.
const (
	// Run admission (both surfaces).
	codeAlreadyRunning = "already-running"
	codeDepsUnmet      = "deps-unmet"
	codeDocUnreadable  = "doc-unreadable"
	codeNoProjectPath  = "no-project-path"

	// Branch lifecycle — the phase surface's own gate, then the worktree sentinels
	// (see worktreeConflict). codeNoRunBranch is not a worktree condition: the phase
	// row simply has no branch STAMPED on it (migration 0043), so there is nothing to
	// delete and re-deriving a name is exactly the bug the stamp exists to prevent.
	codeNoRunBranch = "no-run-branch"

	// Branch lifecycle — the worktree sentinels (see worktreeConflict).
	codeBranchDirty      = "branch-dirty"
	codeBranchCheckedOut = "branch-checked-out"
	codeBranchIsHead     = "branch-is-head"
	codeBranchRefused    = "branch-refused"
	codeBranchBusy       = "branch-busy"
	codeDetachedHead     = "detached-head"
	codeBranchExists     = "branch-exists"
	codePathOccupied     = "path-occupied"

	// codeBranchLivePhase: the orphan-cleanup route was handed a branch that IS a
	// live phase row's run branch. That route exists to delete work stranded under
	// an id generation that no longer has a row; pointing it at a live phase would
	// destroy a run's branch behind that run's own back, which the phase-scoped
	// route refuses by construction (it derives the name, it cannot be told one).
	codeBranchLivePhase = "branch-live-phase"

	// Plan-run-only admission gates.
	codePhaseRunning = "phase-running"
	codePlanInactive = "plan-not-active"
	codeNoPhases     = "no-phases"
	codePlanComplete = "plan-complete"
)

// writeConflict replies 409 {"error": msg, "code": code}.
func writeConflict(w http.ResponseWriter, code, msg string) {
	writeConflictFields(w, code, msg, nil)
}

// writeConflictFields is writeConflict plus the case's structured escape-hatch
// data (unmetDeps; branch/commitsAhead/base). `error` and `code` are written
// last so a stray key in extra can never shadow the discriminator.
func writeConflictFields(w http.ResponseWriter, code, msg string, extra map[string]any) {
	body := make(map[string]any, len(extra)+2)
	for k, v := range extra {
		body[k] = v
	}
	body["error"] = msg
	body["code"] = code
	writeJSONStatus(w, http.StatusConflict, body)
}

// worktreeConflict maps a worktree sentinel to its 409 code and an actionable
// message, or ok=false when err is not one of them.
//
// Every sentinel the branch lifecycle can raise is mapped HERE, including the
// ones no arm used to name: ErrBranchIsHead, ErrRefusedBranch and ErrBranchBusy
// all reached the generic `case err != nil` and surfaced as opaque 500s. Two of
// them are genuinely reachable — a user whose repo has swarm/phase-N checked out
// as HEAD hits ErrBranchIsHead on delete, and a leftover worktree under a
// different project slug (slug churn is real here) makes reclaim answer (0, nil)
// so Acquire then refuses with ErrBranchBusy.
//
// Callers must place this ABOVE their generic `case err != nil` arm: below it,
// it is unreachable code that no body assertion would ever catch — only a status
// assertion would.
func worktreeConflict(err error) (code, msg string, ok bool) {
	switch {
	case errors.Is(err, worktree.ErrBranchCheckedOut):
		return codeBranchCheckedOut, "the run branch is checked out in another worktree", true
	case errors.Is(err, worktree.ErrBranchIsHead):
		return codeBranchIsHead,
			"the run branch is the repo's currently checked-out branch — check out another branch first", true
	case errors.Is(err, worktree.ErrRefusedBranch):
		return codeBranchRefused,
			"refusing to operate on a branch outside the swarm/ namespace", true
	case errors.Is(err, worktree.ErrBranchBusy):
		return codeBranchBusy,
			"the run branch is busy in another worktree — remove it or finish the run that holds it", true
	case errors.Is(err, worktree.ErrDetachedHead):
		return codeDetachedHead,
			"the repo is on a detached HEAD, so the run branch cannot be measured against a base — check out a branch first", true
	// ErrBranchExists and ErrPathOccupied were BOTH unmapped, so an acquire that hit
	// either surfaced as an opaque 500 carrying git's raw sentence — which is how a
	// plan spent four retries acting on a diagnosis that named the wrong blocker
	// (2026-07-30). They are separated here for the same reason the sentinels are:
	// one is resolved by merging or deleting a branch, the other by freeing a path.
	case errors.Is(err, worktree.ErrBranchExists):
		return codeBranchExists,
			"the run branch already exists and holds commits — merge them or delete the branch, then retry", true
	case errors.Is(err, worktree.ErrPathOccupied):
		return codePathOccupied,
			"the run's worktree path is taken by a directory git does not track — free that path, then retry", true
	}
	return "", "", false
}
