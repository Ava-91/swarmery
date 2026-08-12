// Package planrev owns plan revisions: a staged, reviewable proposal to change
// an already-saved plan. A revision outlives the planning session that produced
// it (an operator may apply it hours later), so it is its own package rather
// than state hanging off internal/planning.
package planrev

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// Revision statuses — the closed set persisted in plan_revisions.status.
const (
	StatusStaged     = "staged"
	StatusApplied    = "applied"
	StatusRejected   = "rejected"
	StatusSuperseded = "superseded"
	StatusFailed     = "failed"
)

// Origins — provenance of WHAT started the revision. The original ask behind
// this feature was "no way to see if it was manual or automated", so origin is
// a column, never inferred prose.
const (
	OriginOperator  = "operator_revise"
	OriginDiagnosis = "phase_diagnosis"
)

// File actions.
const (
	ActionCreate = "create"
	ActionUpdate = "update"
	ActionDelete = "delete"
	ActionRename = "rename"
)

// ErrEmptyRevision rejects staging a revision with no file changes — an empty
// proposal has nothing to review, apply, or roll back.
var ErrEmptyRevision = errors.New("planrev: revision has no files")

// ErrNotStaged marks an operation that requires a 'staged' revision hitting one
// that has already been decided (applied/rejected/superseded/failed).
var ErrNotStaged = errors.New("planrev: revision is not staged")

// File is one proposed change to a plan document. DocPath and RenameFrom are
// always plan-dir-relative; the apply step joins them onto Revision.PlanDir.
type File struct {
	ID          int64  `json:"id"`
	DocPath     string `json:"docPath"`
	Action      string `json:"action"`
	RenameFrom  string `json:"renameFrom,omitempty"`
	BaseHash    string `json:"baseHash,omitempty"`
	Proposed    string `json:"-"` // never in a list DTO; detail endpoint renders a diff
	AppliedHash string `json:"appliedHash,omitempty"`
}

// Revision is a staged proposal to change one saved plan.
type Revision struct {
	ID              int64  `json:"id"`
	WorkspaceTaskID int64  `json:"workspaceTaskId"`
	PlanDir         string `json:"planDir"`
	SessionUUID     string `json:"sessionUuid,omitempty"`
	Status          string `json:"status"`
	Origin          string `json:"origin"`
	TriggerPhaseID  *int64 `json:"triggerPhaseId,omitempty"`
	Reason          string `json:"reason"`
	Summary         string `json:"-"` // raw JSON; the api layer re-marshals
	Error           string `json:"error,omitempty"`
	CreatedAt       string `json:"createdAt"`
	DecidedAt       string `json:"decidedAt,omitempty"`
	DecidedBy       string `json:"decidedBy,omitempty"`
	Files           []File `json:"files,omitempty"`
}

// Sha256Hex matches sysscan's content_hash encoding (see sysedit.sha256Hex) so
// base_hash / applied_hash compare against the rest of the system.
func Sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
