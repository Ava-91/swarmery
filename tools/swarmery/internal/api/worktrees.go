package api

// Worktree inventory (worktree janitor, internal/wtjanitor): the LIVE list of
// checkouts per project, each annotated with the janitor's most recent decision
// about it, plus the recent sweep journal.
//
// Read-only by design. The janitor acts on its own schedule against proof it
// gathers itself; a "clean now" button would imply the decision is the
// operator's, which is exactly the burden this subsystem removes. What the
// operator needs is not a control but an account of what happened — so this
// endpoint exists to make a silent subsystem legible, and nothing more.

import (
	"database/sql"
	"log"
	"net/http"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/wtjanitor"
)

// worktreeSweepsCap bounds the journal list the panel renders.
const worktreeSweepsCap = 200

// worktreeRowDTO is one live worktree with its latest journal entry.
type worktreeRowDTO struct {
	ProjectSlug string  `json:"projectSlug"`
	Path        string  `json:"path"`
	Branch      *string `json:"branch"`
	IsMain      bool    `json:"isMain"`
	DirtyFiles  int     `json:"dirtyFiles"`
	// LastVerdict/LastReason/LastSweptAt come from the newest worktree_sweeps row
	// for this path; null when the janitor has never reached it.
	LastVerdict *string `json:"lastVerdict"`
	LastReason  *string `json:"lastReason"`
	LastSweptAt *string `json:"lastSweptAt"`
}

// worktreeSweepDTO is one journal row.
type worktreeSweepDTO struct {
	Ts            string  `json:"ts"`
	ProjectSlug   *string `json:"projectSlug"`
	Path          string  `json:"path"`
	Branch        *string `json:"branch"`
	Verdict       string  `json:"verdict"`
	Reason        string  `json:"reason"`
	SalvageBranch *string `json:"salvageBranch"`
	Files         int64   `json:"files"`
	Removed       bool    `json:"removed"`
	Error         *string `json:"error"`
}

type worktreesDTO struct {
	Live   []worktreeRowDTO   `json:"live"`
	Sweeps []worktreeSweepDTO `json:"sweeps"`
	// Enabled mirrors wtjanitor.Enabled() so the panel can say "the janitor is
	// off" instead of silently presenting a frozen history as current.
	Enabled bool `json:"enabled"`
}

// noopLiveness answers "nobody is here" without touching the process table.
// The inventory endpoint must not shell out to `ps` on every page open, and it
// does not render liveness anyway — the janitor itself asks the real question
// at the only moment it matters, immediately before removing something.
type noopLiveness struct{}

func (noopLiveness) Busy(string) (bool, error) { return false, nil }

// GET /api/worktrees?project=<slug|id|name>
func (h *Handler) listWorktrees(w http.ResponseWriter, r *http.Request) {
	out := worktreesDTO{
		Live:    []worktreeRowDTO{},
		Sweeps:  []worktreeSweepDTO{},
		Enabled: wtjanitor.Enabled(),
	}

	pf, pargs := scopeFilter(r)

	// Latest decision per path, for the annotation on each live row.
	latest, err := h.latestSweepByPath()
	if err != nil {
		writeErr(w, err)
		return
	}

	projects, err := h.DB.Query(`SELECT slug, path FROM projects p WHERE archived = 0`+pf, pargs...)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer projects.Close()
	type proj struct{ slug, path string }
	var list []proj
	for projects.Next() {
		var p proj
		if err := projects.Scan(&p.slug, &p.path); err != nil {
			writeErr(w, err)
			return
		}
		list = append(list, p)
	}
	if err := projects.Err(); err != nil {
		writeErr(w, err)
		return
	}

	for _, p := range list {
		wts, ierr := wtjanitor.RepoGit{}.Inspect(p.path, noopLiveness{})
		if ierr != nil {
			// A project that moved, was unmounted or is not a git repo any more
			// must not blank the panel for every other one.
			log.Printf("worktrees: inspect %s: %v", p.path, ierr)
			continue
		}
		for _, wt := range wts {
			row := worktreeRowDTO{
				ProjectSlug: p.slug, Path: wt.Path, IsMain: wt.IsMain, DirtyFiles: len(wt.Dirty),
			}
			if wt.Branch != "" {
				b := wt.Branch
				row.Branch = &b
			}
			if s, ok := latest[wt.Path]; ok {
				v, rs, ts := s.verdict, s.reason, s.ts
				row.LastVerdict, row.LastReason, row.LastSweptAt = &v, &rs, &ts
			}
			out.Live = append(out.Live, row)
		}
	}

	if out.Sweeps, err = h.recentSweeps(pf, pargs); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, out, nil)
}

type sweepMark struct{ verdict, reason, ts string }

// latestSweepByPath folds the journal to the newest row per path in ONE query —
// never a per-row lookup inside the inventory loop (the store caps the pool at
// one connection, so a query inside an open cursor would deadlock).
func (h *Handler) latestSweepByPath() (map[string]sweepMark, error) {
	rows, err := h.DB.Query(`
		SELECT s.path, s.verdict, s.reason, s.ts
		  FROM worktree_sweeps s
		  JOIN (SELECT path, MAX(id) AS id FROM worktree_sweeps GROUP BY path) m
		    ON m.id = s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]sweepMark{}
	for rows.Next() {
		var path string
		var m sweepMark
		if err := rows.Scan(&path, &m.verdict, &m.reason, &m.ts); err != nil {
			return nil, err
		}
		out[path] = m
	}
	return out, rows.Err()
}

func (h *Handler) recentSweeps(pf string, pargs []any) ([]worktreeSweepDTO, error) {
	rows, err := h.DB.Query(`
		SELECT s.ts, p.slug, s.path, s.branch, s.verdict, s.reason,
		       s.salvage_branch, s.files, s.removed, s.error
		  FROM worktree_sweeps s
		  LEFT JOIN projects p ON p.id = s.project_id
		 WHERE 1 = 1`+pf+`
		 ORDER BY s.ts DESC, s.id DESC
		 LIMIT ?`, append(append([]any{}, pargs...), worktreeSweepsCap)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []worktreeSweepDTO{}
	for rows.Next() {
		var d worktreeSweepDTO
		var slug, branch, salvage, errStr sql.NullString
		var removed int64
		if err := rows.Scan(&d.Ts, &slug, &d.Path, &branch, &d.Verdict, &d.Reason,
			&salvage, &d.Files, &removed, &errStr); err != nil {
			return nil, err
		}
		if slug.Valid {
			s := slug.String
			d.ProjectSlug = &s
		}
		if branch.Valid {
			b := branch.String
			d.Branch = &b
		}
		if salvage.Valid && salvage.String != "" {
			s := salvage.String
			d.SalvageBranch = &s
		}
		if errStr.Valid && errStr.String != "" {
			e := errStr.String
			d.Error = &e
		}
		d.Removed = removed != 0
		out = append(out, d)
	}
	return out, rows.Err()
}
