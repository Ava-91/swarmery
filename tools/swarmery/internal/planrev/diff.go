package planrev

import (
	"os"
	"path/filepath"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/textdiff"
)

// FileDiff is one staged file rendered against the LIVE plan dir at call time:
// the unified diff (live → proposed) IS the review contract — raw proposed
// content is never exposed separately — and Stale flags a live file whose hash
// drifted from the base_hash captured at staging time.
type FileDiff struct {
	DocPath    string `json:"docPath"`
	Action     string `json:"action"`
	RenameFrom string `json:"renameFrom,omitempty"`
	Stale      bool   `json:"stale"`
	Diff       string `json:"diff"`
}

// LiveDiffs computes every file's review diff for a revision. This is the one
// shared implementation behind GET /api/revisions/{id} — the api layer and the
// e2e test both call it, so what the test asserts is what the operator sees.
// Missing live files degrade to an empty side ("" → the diff shows a pure add
// or a pure delete), matching the handler's historical tolerance.
func LiveDiffs(rev *Revision) []FileDiff {
	diffs := make([]FileDiff, 0, len(rev.Files))
	for _, f := range rev.Files {
		live := ""
		if f.Action != ActionCreate {
			path := filepath.Join(rev.PlanDir, f.DocPath)
			if f.Action == ActionRename {
				path = filepath.Join(rev.PlanDir, f.RenameFrom)
			}
			if b, err := os.ReadFile(path); err == nil {
				live = string(b)
			}
		}
		proposed := f.Proposed // "" for delete: the diff renders live → gone
		diffs = append(diffs, FileDiff{
			DocPath:    f.DocPath,
			Action:     f.Action,
			RenameFrom: f.RenameFrom,
			Stale:      f.BaseHash != "" && Sha256Hex([]byte(live)) != f.BaseHash,
			Diff:       textdiff.UnifiedDiff("a/"+f.DocPath, "b/"+f.DocPath, live, proposed),
		})
	}
	return diffs
}
