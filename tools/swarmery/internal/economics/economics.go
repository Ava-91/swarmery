// FNXC:AgentEconomics 2026-07-29-22:26 — the agent-system audit must cite what the fleet actually spends, so the five token-economy metrics are computed here from the session index instead of being estimated by hand.

// Package economics aggregates token spend from the session index into the five
// metrics the agent-system audit is measured on: cost per completed task, cache
// efficiency, delegation cost, wasted work, and model mix.
//
// Read-only by contract: no statement in this package writes to the database —
// the daemon may be serving the same WAL while a report runs. Every query is a
// named constant in queries.go; none of them is an INSERT, UPDATE or DELETE.
//
// Honesty rule, inherited from internal/cost: "an unknown model yields a nil
// cost (stored as SQL NULL), never 0 — a zero would silently corrupt aggregate
// sums". This package continues it. No aggregation here sums NULL as zero:
// SQL SUM skips NULL rows, and every metric that unpriced turns can distort
// carries a Coverage — the share of its rows that had a cost. A metric with
// Coverage below 0.8 renders with a "low-coverage" marker.
//
// Window semantics (deliberate, so numbers are comparable between runs):
//
//   - Turn-scoped metrics (cache efficiency, model mix, cost by agent_name and
//     the turn counters in Sample) filter on turns.started_at and on the
//     session's project.
//   - Task-scoped metrics (cost per task, delegation shape, waste) select tasks
//     by tasks.created_at and tasks.project_id, then sum ALL of each task's
//     turns. A task is never truncated mid-flight by the window boundary.
package economics

import (
	"database/sql"
	"fmt"
	"math"
	"sort"
	"time"
)

// Options bounds a report. The zero value means whole history, all projects.
type Options struct {
	Since     string `json:"since,omitempty"`      // YYYY-MM-DD inclusive; "" = no lower bound
	Until     string `json:"until,omitempty"`      // YYYY-MM-DD inclusive; "" = no upper bound
	ProjectID int64  `json:"project_id,omitempty"` // 0 = all projects
}

// dayFormat is the only accepted shape for the window bounds. Dates are
// compared as strings against substr(<ts>,1,10), which is correct because
// timestamps are stored RFC3339 (YYYY-MM-DDThh:mm:ssZ) and YYYY-MM-DD sorts
// lexicographically the same way it sorts chronologically.
const dayFormat = "2006-01-02"

// validate rejects malformed bounds before any query runs, so a typo surfaces
// as a usage error rather than as a silently empty report.
func (o Options) validate() error {
	for _, f := range []struct{ name, val string }{{"since", o.Since}, {"until", o.Until}} {
		if f.val == "" {
			continue
		}
		if _, err := time.Parse(dayFormat, f.val); err != nil {
			return fmt.Errorf("economics: --%s must be YYYY-MM-DD, got %q", f.name, f.val)
		}
	}
	if o.Since != "" && o.Until != "" && o.Since > o.Until {
		return fmt.Errorf("economics: --since %s is after --until %s", o.Since, o.Until)
	}
	if o.ProjectID < 0 {
		return fmt.Errorf("economics: --project must be >= 0, got %d", o.ProjectID)
	}
	return nil
}

// args returns the bind values for turnScope and taskScope, which deliberately
// share one parameter order so every query in this package binds the same
// slice: since, since, until, until, project, project.
func (o Options) args() []any {
	return []any{o.Since, o.Since, o.Until, o.Until, o.ProjectID, o.ProjectID}
}

// Sample carries the provenance every audit claim must cite: which DB, which
// window, and how many rows backed the numbers.
type Sample struct {
	Sessions      int `json:"sessions"`
	Turns         int `json:"turns"`
	TurnsPriced   int `json:"turns_priced"`   // cost_usd IS NOT NULL
	TurnsUnpriced int `json:"turns_unpriced"` // usage present, unknown model → NULL cost
	TurnsNoAgent  int `json:"turns_no_agent"` // agent_name IS NULL — the delegation-attribution limit
	Tasks         int `json:"tasks"`
	TasksDone     int `json:"tasks_done"`
	Delegations   int `json:"delegations"`
}

// Report is one snapshot. GeneratedAt is the only non-deterministic field:
// re-running against an unchanged database reproduces everything else byte for
// byte (see TestRenderIsDeterministic).
type Report struct {
	GeneratedAt string                `json:"generated_at"`
	DBPath      string                `json:"db_path"`
	Options     Options               `json:"options"`
	Sample      Sample                `json:"sample"`
	CostPerTask CostPerTaskMetric     `json:"cost_per_task"`
	CacheEff    CacheEfficiencyMetric `json:"cache_efficiency"`
	Delegation  DelegationMetric      `json:"delegation"`
	Waste       WasteMetric           `json:"waste"`
	ModelMix    ModelMixMetric        `json:"model_mix"`
}

// Compute reads the five metrics out of db. It never writes: db may be the
// live database of a running daemon.
func Compute(db *sql.DB, opts Options) (*Report, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	rep := &Report{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		DBPath:      mainDBPath(db),
		Options:     opts,
	}

	var err error
	if rep.Sample, err = loadSample(db, opts); err != nil {
		return nil, err
	}
	if rep.CostPerTask, err = loadCostPerTask(db, opts); err != nil {
		return nil, err
	}
	if rep.CacheEff, err = loadCacheEfficiency(db, opts); err != nil {
		return nil, err
	}
	if rep.Delegation, err = loadDelegation(db, opts); err != nil {
		return nil, err
	}
	if rep.Waste, err = loadWaste(db, opts); err != nil {
		return nil, err
	}
	if rep.ModelMix, err = loadModelMix(db, opts); err != nil {
		return nil, err
	}
	return rep, nil
}

// mainDBPath asks SQLite which file it has open, so the report can cite its
// own source without Compute needing the path passed in. Empty for an
// in-memory database; a failure here is never fatal to a report.
func mainDBPath(db *sql.DB) string {
	rows, err := db.Query(`PRAGMA database_list`)
	if err != nil {
		return ""
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var name string
		var file sql.NullString
		if err := rows.Scan(&seq, &name, &file); err != nil {
			return ""
		}
		if name == "main" {
			return file.String
		}
	}
	return ""
}

// coverage is the priced share of a metric's rows. total == 0 yields 0, which
// the renderer prints as "n/a" rather than as a low-coverage warning.
func coverage(priced, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(priced) / float64(total)
}

// share is a safe ratio for percentage columns.
func share(part, whole float64) float64 {
	if whole == 0 {
		return 0
	}
	return part / whole
}

// distribution summarises per-task costs. Values are copied before sorting so
// callers keep their slice order.
func distribution(values []float64) CostDistribution {
	d := CostDistribution{Tasks: len(values)}
	if len(values) == 0 {
		return d
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	for _, v := range sorted {
		d.Sum += v
	}
	d.Mean = d.Sum / float64(len(sorted))
	d.Median = percentile(sorted, 0.5)
	d.P90 = percentile(sorted, 0.9)
	d.Min = sorted[0]
	d.Max = sorted[len(sorted)-1]
	return d
}

// percentile uses the nearest-rank method on an ascending slice
// (rank = ceil(p*n), clamped to [1,n]); the median of an even-sized sample is
// the mean of the two middle values. Stated explicitly because a 31-task
// sample makes the choice of estimator visible in the result.
func percentile(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if p == 0.5 && n%2 == 0 {
		return (sorted[n/2-1] + sorted[n/2]) / 2
	}
	rank := int(math.Ceil(p * float64(n)))
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return sorted[rank-1]
}

func nullFloat(v sql.NullFloat64) float64 {
	if v.Valid {
		return v.Float64
	}
	return 0
}

func nullInt(v sql.NullInt64) int64 {
	if v.Valid {
		return v.Int64
	}
	return 0
}
