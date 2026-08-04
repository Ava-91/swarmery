package wtjanitor

import (
	"database/sql"
	"log"
	"strings"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/worktree"
)

// DefaultMinIdle is the age floor a worktree must clear before it is eligible.
const DefaultMinIdle = 30 * time.Minute

// Inspector is the read side of one repository: enumerate its worktrees and
// observe the facts Classify needs.
type Inspector interface {
	Git
	Inspect(repoRoot string, live Liveness) ([]Worktree, error)
}

// Remover is the destructive boundary — separated so tests can assert that a
// vetoed or failed-salvage worktree is NEVER passed to it.
//
// RemoveWorktree takes the branch too because worktree.Manager.Remove deletes
// the branch in the SAME call (keepBranch=false); asking for the removal and
// the branch deletion separately would delete it twice and log a spurious
// failure the second time.
type Remover interface {
	RemoveWorktree(repoRoot, path, branch string, deleteBranch bool) error
	DeleteBranch(repoRoot, branch string) error
	Prune(repoRoot string) error
}

// RealRemover adapts worktree.Manager, whose API is shaped for the acquire
// path: Remove takes an Acquired value (Path/Branch/StartPoint) rather than a
// bare path, and DeleteBranch returns (existed bool, err error). Adapting here
// keeps ONE implementation of worktree removal in the tree — including the
// leftover-directory repair Remove performs when git no longer tracks the path.
type RealRemover struct{ M *worktree.Manager }

// NewRealRemover wraps the manager dispatch/verify/phaserun already share.
func NewRealRemover(m *worktree.Manager) RealRemover { return RealRemover{M: m} }

func (r RealRemover) RemoveWorktree(repoRoot, path, branch string, deleteBranch bool) error {
	return r.M.Remove(repoRoot, worktree.Acquired{Path: path, Branch: branch}, !deleteBranch)
}

func (r RealRemover) DeleteBranch(repoRoot, branch string) error {
	_, err := r.M.DeleteBranch(repoRoot, branch) // existed is uninteresting: deletion is idempotent
	return err
}

func (r RealRemover) Prune(repoRoot string) error { return r.M.Prune(repoRoot) }

// Service runs sweeps. It owns no goroutine: the daemon's ticker calls Sweep.
type Service struct {
	DB   *sql.DB
	Git  Inspector
	Live Liveness
	// MinIdle is the age floor Classify enforces. A zero value means
	// DefaultMinIdle, never "no floor" — a zero floor would silently remove a
	// real safety guarantee.
	MinIdle time.Duration
	// OnlyRepo limits a sweep to one repository path ("" = every project).
	OnlyRepo string
	Remover  Remover
	// now is a test seam.
	now func() time.Time
}

// Result counts one sweep for the caller's log line.
type Result struct {
	Inspected, Removed, Salvaged, Kept, Skipped, Errors int
}

type repo struct {
	id   int64
	path string
}

// Sweep processes every non-archived project once. dryRun decides everything and
// writes the journal but performs no destructive action.
func (s *Service) Sweep(dryRun bool) (Result, error) {
	var res Result
	repos, err := s.repos()
	if err != nil {
		return res, err
	}
	for _, r := range repos {
		wts, ierr := s.Git.Inspect(r.path, s.Live)
		if ierr != nil {
			log.Printf("wtjanitor: inspect %s: %v", r.path, ierr)
			res.Errors++
			continue
		}
		for _, wt := range wts {
			res.Inspected++
			s.sweepOne(r, wt, dryRun, &res)
		}
		if !dryRun {
			s.sweepBranches(r, wts, &res)
			if perr := s.Remover.Prune(r.path); perr != nil {
				log.Printf("wtjanitor: prune %s: %v", r.path, perr)
			}
		}
	}
	return res, nil
}

// sweepOne decides and acts on ONE worktree. The order here IS the safety
// contract: classify, then re-check liveness, then salvage, and only then
// remove — with every failure path returning before the removal.
func (s *Service) sweepOne(r repo, wt Worktree, dryRun bool, res *Result) {
	now := s.clock()
	dec, err := Classify(r.path, wt, s.Git, now, s.minIdle())
	if err != nil {
		s.journal(r, wt, Decision{VerdictSkip, "classify failed"}, "", false, err)
		res.Errors++
		return
	}
	switch dec.Verdict {
	case VerdictSkip:
		res.Skipped++
		s.journal(r, wt, dec, "", false, nil)
		return
	case VerdictKeepUnmerged:
		res.Kept++
		s.journal(r, wt, dec, "", false, nil)
		return
	}
	if dryRun {
		s.journal(r, wt, dec, "", false, nil)
		return
	}

	// Classification and action are not atomic: a run may have started in the
	// meantime, and the whole point of the liveness veto is that it holds at the
	// moment of destruction, not at the moment of judgement.
	if fresh, lerr := s.Live.Busy(wt.Path); lerr == nil && fresh {
		res.Skipped++
		s.journal(r, wt, Decision{VerdictSkip, "became live before removal"}, "", false, nil)
		return
	}

	salvageBranch := ""
	if dec.Verdict == VerdictSalvage {
		b, serr := Salvage(r.path, wt.Path, wt.Branch, now)
		if serr != nil {
			// Salvage failed ⇒ removal is off the table. This is the single most
			// important branch in the package: it is what makes "the janitor
			// cannot be the reason work was lost" true rather than aspirational.
			res.Errors++
			s.journal(r, wt, Decision{VerdictKeepUnmerged, "salvage failed, worktree kept"}, "", false, serr)
			return
		}
		salvageBranch = b
		res.Salvaged++
	}

	// The branch goes with the worktree in ONE call, and only when it carries
	// nothing of its own. HasOwnCommits is already false here (that case
	// returned above); the check is belt-and-braces against a future reordering.
	deleteBranch := wt.Branch != "" && !wt.HasOwnCommits
	if rerr := s.Remover.RemoveWorktree(r.path, wt.Path, wt.Branch, deleteBranch); rerr != nil {
		res.Errors++
		s.journal(r, wt, dec, salvageBranch, false, rerr)
		return
	}
	res.Removed++
	s.journal(r, wt, dec, salvageBranch, true, nil)
}

// sweepBranches deletes residual local branches that no worktree checks out and
// that carry nothing of their own.
//
// salvage/* is deliberately absent from the prefix set: a rescue branch holds
// commits reachable from nothing else by construction, so it could never match
// the "no own commits" test anyway — but leaving it out makes it impossible for
// a future edit to that test to turn the janitor against its own rescues.
func (s *Service) sweepBranches(r repo, wts []Worktree, res *Result) {
	checkedOut := map[string]bool{}
	for _, wt := range wts {
		if wt.Branch != "" {
			checkedOut[wt.Branch] = true
		}
	}
	out, err := run(r.path, "branch", "--format=%(refname:short)")
	if err != nil {
		log.Printf("wtjanitor: list branches %s: %v", r.path, err)
		return
	}
	g, ok := s.Git.(interface {
		hasOwnCommits(repoRoot, branch string) (bool, error)
	})
	if !ok {
		return // stubbed Git in tests: branch sweeping is covered by its own test
	}
	for _, b := range strings.Split(out, "\n") {
		b = strings.TrimSpace(b)
		if b == "" || checkedOut[b] {
			continue
		}
		if !strings.HasPrefix(b, "swarm/") && !strings.HasPrefix(b, "worktree-agent-") {
			continue
		}
		own, oerr := g.hasOwnCommits(r.path, b)
		if oerr != nil || own {
			continue // unmerged work, or we could not prove otherwise
		}
		if derr := s.Remover.DeleteBranch(r.path, b); derr != nil {
			log.Printf("wtjanitor: delete branch %s: %v", b, derr)
			continue
		}
		res.Removed++
	}
}

func (s *Service) repos() ([]repo, error) {
	q := `SELECT id, path FROM projects WHERE archived = 0`
	args := []any{}
	if s.OnlyRepo != "" {
		q += ` AND path = ?`
		args = append(args, s.OnlyRepo)
	}
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []repo
	for rows.Next() {
		var r repo
		if err := rows.Scan(&r.id, &r.path); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// journal records one decision. A journal failure is logged, never returned: an
// unwritable audit row must not stop the sweep that already happened.
func (s *Service) journal(r repo, wt Worktree, dec Decision, salvageBranch string, removed bool, actErr error) {
	var branch, salvage, errStr any
	if wt.Branch != "" {
		branch = wt.Branch
	}
	if salvageBranch != "" {
		salvage = salvageBranch
	}
	if actErr != nil {
		errStr = actErr.Error()
	}
	if _, err := s.DB.Exec(
		`INSERT INTO worktree_sweeps (ts, project_id, path, branch, verdict, reason, salvage_branch, files, removed, error)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		s.clock().UTC().Format(time.RFC3339), r.id, wt.Path, branch, string(dec.Verdict), dec.Reason,
		salvage, len(wt.Dirty), boolInt(removed), errStr,
	); err != nil {
		log.Printf("wtjanitor: journal %s: %v", wt.Path, err)
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Service) minIdle() time.Duration {
	if s.MinIdle > 0 {
		return s.MinIdle
	}
	return DefaultMinIdle
}
