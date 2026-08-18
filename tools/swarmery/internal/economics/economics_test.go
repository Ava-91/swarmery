// FNXC:AgentEconomics 2026-07-29-22:26 — the audit's conclusions are only as trustworthy as these metrics, so every one of them is pinned to a fixture database whose expected numbers are worked out by hand.

package economics

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
)

const eps = 1e-9

func almost(a, b float64) bool { return math.Abs(a-b) < eps }

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %s: %v", query, err)
	}
}

// nullable turns the empty string into a SQL NULL, which is how the fixture
// expresses "this turn has no model / no agent".
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

type turnRow struct {
	id, session, seq              int
	role, model, agent            string
	cost                          any // nil = SQL NULL (unpriced or no usage)
	in, out, cacheRead, cacheWrit any // nil = SQL NULL
	startedAt                     string
}

func insertTurn(t *testing.T, db *sql.DB, r turnRow) {
	t.Helper()
	mustExec(t, db, `INSERT INTO turns
		(id, session_id, seq, role, started_at, tokens_in, tokens_out,
		 tokens_cache_read, tokens_cache_write, cost_usd, model, agent_name)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.id, r.session, r.seq, r.role, r.startedAt,
		r.in, r.out, r.cacheRead, r.cacheWrit, r.cost,
		nullable(r.model), nullable(r.agent))
}

// newFixtureDB builds the schema from the real migrations and fills it with a
// hand-computed sample. Every expectation in this file is derived from these
// rows; see the comment blocks for the arithmetic.
//
// Shape:
//
//	project 1: tasks 1-6, sessions 1-7
//	project 2: task 7, session 8   (isolates the --project filter)
//
//	task 1  done,   explicit  → sessions 1+2, cost 0.60  (3 priced turns)
//	task 2  done,   heuristic → session 3,    cost 0.05  (1 priced, 2 unpriced)
//	task 3  running           → session 5 (ended)   → stranded
//	task 4  running           → session 4 (running) → NOT stranded
//	task 5  failed, reverted=1→ session 6, cost 0.25
//	task 6  retry_count=2     → session 7, cost 0.35 (+1 user turn, no usage)
//	task 7  done,   explicit  → session 8, cost 1.00 (project 2)
func newFixtureDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "economics.db"))
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	mustExec(t, db, `INSERT INTO projects (id, path, slug, first_seen)
		VALUES (1, '/p1', 'p1', '2026-07-01T00:00:00Z'),
		       (2, '/p2', 'p2', '2026-07-01T00:00:00Z')`)

	// ended_at NULL on session 4 only: that is what keeps task 4 out of the
	// stranded count while task 3 falls into it.
	mustExec(t, db, `INSERT INTO sessions (id, project_id, session_uuid, started_at, ended_at, model)
		VALUES (1, 1, 'u1', '2026-07-10T00:00:00Z', '2026-07-10T01:00:00Z', 'model-a'),
		       (2, 1, 'u2', '2026-07-10T00:00:00Z', '2026-07-10T01:00:00Z', 'model-b'),
		       (3, 1, 'u3', '2026-07-10T00:00:00Z', '2026-07-10T01:00:00Z', 'model-a'),
		       (4, 1, 'u4', '2026-07-10T00:00:00Z', NULL,                   'model-b'),
		       (5, 1, 'u5', '2026-07-10T00:00:00Z', '2026-07-10T01:00:00Z', 'model-b'),
		       (6, 1, 'u6', '2026-07-10T00:00:00Z', '2026-07-10T01:00:00Z', 'model-a'),
		       (7, 1, 'u7', '2026-07-10T00:00:00Z', '2026-07-10T01:00:00Z', 'model-a'),
		       (8, 2, 'u8', '2026-07-20T00:00:00Z', '2026-07-20T01:00:00Z', 'model-c')`)

	turns := []turnRow{
		// session 1 — one unattributed turn and one subagent turn
		{id: 1, session: 1, seq: 1, role: "assistant", model: "model-a", agent: "", cost: 0.10, in: 100, out: 10, cacheRead: 900, cacheWrit: 50, startedAt: "2026-07-10T00:10:00Z"},
		{id: 2, session: 1, seq: 2, role: "assistant", model: "model-a", agent: "implementation-agent", cost: 0.20, in: 200, out: 20, cacheRead: 800, cacheWrit: 0, startedAt: "2026-07-10T00:20:00Z"},
		// session 2
		{id: 3, session: 2, seq: 1, role: "assistant", model: "model-b", agent: "implementation-agent", cost: 0.30, in: 300, out: 30, cacheRead: 700, cacheWrit: 0, startedAt: "2026-07-10T00:30:00Z"},
		// session 3 — two unpriced turns: usage present, model unknown → NULL cost
		{id: 4, session: 3, seq: 1, role: "assistant", model: "model-a", agent: "", cost: 0.05, in: 50, out: 5, cacheRead: 100, cacheWrit: 0, startedAt: "2026-07-10T00:40:00Z"},
		{id: 5, session: 3, seq: 2, role: "assistant", model: "model-unknown", agent: "doc-agent", cost: nil, in: 500, out: 50, cacheRead: 0, cacheWrit: 0, startedAt: "2026-07-10T00:41:00Z"},
		{id: 6, session: 3, seq: 3, role: "assistant", model: "model-unknown", agent: "doc-agent", cost: nil, in: 600, out: 60, cacheRead: 0, cacheWrit: 0, startedAt: "2026-07-10T00:42:00Z"},
		// session 4 (still running)
		{id: 7, session: 4, seq: 1, role: "assistant", model: "model-b", agent: "", cost: 0.15, in: 150, out: 15, cacheRead: 200, cacheWrit: 0, startedAt: "2026-07-10T00:50:00Z"},
		// session 5
		{id: 8, session: 5, seq: 1, role: "assistant", model: "model-b", agent: "verification-agent", cost: 0.40, in: 400, out: 40, cacheRead: 600, cacheWrit: 0, startedAt: "2026-07-10T00:55:00Z"},
		// session 6
		{id: 9, session: 6, seq: 1, role: "assistant", model: "model-a", agent: "", cost: 0.25, in: 250, out: 25, cacheRead: 300, cacheWrit: 0, startedAt: "2026-07-10T00:56:00Z"},
		// session 7 — plus a user turn with no usage at all (neither priced nor unpriced)
		{id: 10, session: 7, seq: 1, role: "assistant", model: "model-a", agent: "", cost: 0.35, in: 350, out: 35, cacheRead: 350, cacheWrit: 0, startedAt: "2026-07-10T00:57:00Z"},
		{id: 11, session: 7, seq: 2, role: "user", model: "", agent: "", cost: nil, in: nil, out: nil, cacheRead: nil, cacheWrit: nil, startedAt: "2026-07-10T00:58:00Z"},
		// session 8 (project 2)
		{id: 12, session: 8, seq: 1, role: "assistant", model: "model-c", agent: "other", cost: 1.00, in: 1000, out: 100, cacheRead: 1000, cacheWrit: 0, startedAt: "2026-07-20T00:10:00Z"},
	}
	for _, r := range turns {
		insertTurn(t, db, r)
	}

	mustExec(t, db, `INSERT INTO tasks
		(id, project_id, title, prompt, status, created_at, started_at, reverted, retry_count)
		VALUES
		 (1, 1, 'explicit done task',  'p', 'done',         '2026-07-10T00:00:00Z', '2026-07-10T00:00:00Z', 0, 0),
		 (2, 1, 'heuristic done task', 'p', 'done',         '2026-07-10T00:00:00Z', '2026-07-10T00:00:00Z', 0, 0),
		 (3, 1, 'stranded task',       'p', 'running',      '2026-07-10T00:00:00Z', '2026-07-10T00:00:00Z', 0, 0),
		 (4, 1, 'live running task',   'p', 'running',      '2026-07-10T00:00:00Z', '2026-07-10T00:00:00Z', 0, 0),
		 (5, 1, 'reverted task',       'p', 'failed',       '2026-07-10T00:00:00Z', '2026-07-10T00:00:00Z', 1, 0),
		 (6, 1, 'retried task',        'p', 'needs_review', '2026-07-10T00:00:00Z', '2026-07-10T00:00:00Z', 0, 2),
		 (7, 2, 'other project task',  'p', 'done',         '2026-07-20T00:00:00Z', '2026-07-20T00:00:00Z', 0, 0)`)

	mustExec(t, db, `INSERT INTO task_sessions (task_id, session_id, link_source, confidence)
		VALUES (1, 1, 'explicit', NULL),
		       (1, 2, 'explicit', NULL),
		       (2, 3, 'heuristic', 0.6),
		       (3, 5, 'explicit', NULL),
		       (4, 4, 'explicit', NULL),
		       (5, 6, 'explicit', NULL),
		       (6, 7, 'explicit', NULL),
		       (7, 8, 'explicit', NULL)`)

	// task 1 delegates twice to the same agent (a repeat) and once to another;
	// the third row is unrated, so AVG(NULLIF(quality,0)) sees only 4 and 5.
	mustExec(t, db, `INSERT INTO task_delegations (task_id, seq, agent, phase, verdict, artifact, loops, quality)
		VALUES (1, 1, 'implementation-agent', 'phase-1', 'pass', 'a1', 1, 4),
		       (1, 2, 'implementation-agent', 'phase-1', 'pass', 'a2', 0, 5),
		       (1, 3, 'verification-agent',   'phase-1', 'pass', 'a3', 0, NULL),
		       (2, 1, 'task-documenter',      'phase-1', 'pass', 'a4', 0, 0)`)

	// task 1 looped twice (counts as wasted); task 2 looped once (does not).
	mustExec(t, db, `INSERT INTO task_loops (task_id, loop_n, failed, brief_delta)
		VALUES (1, 1, 'criteria unmet', 'tighten brief'),
		       (1, 2, 'criteria unmet', 'tighten brief again'),
		       (2, 1, 'criteria unmet', 'tighten brief')`)

	return db
}

func mustCompute(t *testing.T, db *sql.DB, o Options) *Report {
	t.Helper()
	rep, err := Compute(db, o)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	return rep
}

func TestComputeSample(t *testing.T) {
	rep := mustCompute(t, newFixtureDB(t), Options{})
	s := rep.Sample

	// 12 turns over 8 sessions: 9 priced, 2 unpriced (usage but unknown model),
	// 1 user turn with no usage at all — which is neither.
	checks := []struct {
		name      string
		got, want int
	}{
		{"sessions", s.Sessions, 8},
		{"turns", s.Turns, 12},
		{"turns priced", s.TurnsPriced, 9},
		{"turns unpriced", s.TurnsUnpriced, 2},
		{"turns no agent", s.TurnsNoAgent, 5},
		{"tasks", s.Tasks, 7},
		{"tasks done", s.TasksDone, 3},
		{"delegations", s.Delegations, 4},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("Sample.%s = %d, want %d", c.name, c.got, c.want)
		}
	}
	if s.TurnsPriced+s.TurnsUnpriced >= s.Turns {
		t.Errorf("the no-usage user turn must be neither priced nor unpriced: %+v", s)
	}
}

func TestCostPerTaskSplitsExplicitFromHeuristic(t *testing.T) {
	m := mustCompute(t, newFixtureDB(t), Options{}).CostPerTask

	// all links: task1 0.60, task2 0.05, task7 1.00
	if m.All.Tasks != 3 {
		t.Fatalf("All.Tasks = %d, want 3", m.All.Tasks)
	}
	if !almost(m.All.Sum, 1.65) {
		t.Errorf("All.Sum = %v, want 1.65", m.All.Sum)
	}
	if !almost(m.All.Median, 0.60) {
		t.Errorf("All.Median = %v, want 0.60", m.All.Median)
	}
	if !almost(m.All.Min, 0.05) || !almost(m.All.Max, 1.00) {
		t.Errorf("All min/max = %v/%v, want 0.05/1.00", m.All.Min, m.All.Max)
	}

	// explicit only drops the heuristically linked task 2 entirely.
	if m.Explicit.Tasks != 2 {
		t.Fatalf("Explicit.Tasks = %d, want 2", m.Explicit.Tasks)
	}
	if !almost(m.Explicit.Sum, 1.60) {
		t.Errorf("Explicit.Sum = %v, want 1.60", m.Explicit.Sum)
	}
	// even sample → mean of the two middle values
	if !almost(m.Explicit.Median, 0.80) {
		t.Errorf("Explicit.Median = %v, want 0.80", m.Explicit.Median)
	}
	for _, row := range m.Tasks {
		if row.TaskID == 2 && row.LinkSource != "heuristic" {
			t.Errorf("task 2 link_source = %q, want heuristic", row.LinkSource)
		}
	}
}

// The unknown-model turns must lower coverage instead of being summed as zero.
func TestUnpricedTurnsLowerCoverageAndAreNeverZero(t *testing.T) {
	m := mustCompute(t, newFixtureDB(t), Options{}).CostPerTask

	// 7 turns across the three done tasks, 5 of them priced.
	if !almost(m.Coverage, 5.0/7.0) {
		t.Errorf("Coverage = %v, want %v", m.Coverage, 5.0/7.0)
	}
	if m.Coverage >= coverageFloor {
		t.Fatalf("fixture must exercise the low-coverage path, got %v", m.Coverage)
	}
	if got := coverageLabel(m.Coverage, len(m.Tasks)); !strings.Contains(got, "low-coverage") {
		t.Errorf("coverageLabel(%v) = %q, want a low-coverage marker", m.Coverage, got)
	}

	// Task 2's two NULL-cost turns contribute nothing to its sum: 0.05, not 0.05+0+0
	// treated as priced, and certainly not a silent 0 in place of the unknown.
	for _, row := range m.Tasks {
		if row.TaskID != 2 {
			continue
		}
		if !almost(row.CostUSD, 0.05) {
			t.Errorf("task 2 cost = %v, want 0.05", row.CostUSD)
		}
		if row.Turns != 3 || row.TurnsPriced != 1 {
			t.Errorf("task 2 turns/priced = %d/%d, want 3/1", row.Turns, row.TurnsPriced)
		}
	}
}

func TestCacheEfficiency(t *testing.T) {
	m := mustCompute(t, newFixtureDB(t), Options{}).CacheEff
	if len(m.Rows) != 7 {
		t.Fatalf("rows = %d, want 7 agent×model pairs", len(m.Rows))
	}
	// sorted by cost desc: project 2's turn is the single most expensive row.
	if m.Rows[0].Agent != "other" || !almost(m.Rows[0].CostUSD, 1.00) {
		t.Errorf("first row = %+v, want the 'other' agent at 1.00", m.Rows[0])
	}
	for i := 1; i < len(m.Rows); i++ {
		if m.Rows[i-1].CostUSD < m.Rows[i].CostUSD {
			t.Fatalf("rows not sorted by cost desc at %d: %v < %v",
				i, m.Rows[i-1].CostUSD, m.Rows[i].CostUSD)
		}
	}

	// (main-session) × model-a: cache_read 1650, tokens_in 750
	var found bool
	for _, r := range m.Rows {
		if r.Agent == "(main-session)" && r.Model == "model-a" {
			found = true
			if r.CacheRead != 1650 || r.TokensIn != 750 {
				t.Errorf("main/model-a tokens = read %d in %d, want 1650/750", r.CacheRead, r.TokensIn)
			}
			if !almost(r.CacheHit, 1650.0/2400.0) {
				t.Errorf("main/model-a cache_hit = %v, want %v", r.CacheHit, 1650.0/2400.0)
			}
		}
	}
	if !found {
		t.Fatal("no (main-session)×model-a row")
	}
	// The overall row is the sum of the parts, not a re-query.
	if m.Overall.Turns != 11 {
		t.Errorf("overall turns = %d, want 11 assistant turns", m.Overall.Turns)
	}
}

func TestDelegationBothSlices(t *testing.T) {
	m := mustCompute(t, newFixtureDB(t), Options{}).Delegation

	// slice A — turns.agent_name
	var main, impl *AgentCost
	for i := range m.ByAgent {
		switch m.ByAgent[i].Agent {
		case "(main-session)":
			main = &m.ByAgent[i]
		case "implementation-agent":
			impl = &m.ByAgent[i]
		}
	}
	if main == nil || impl == nil {
		t.Fatalf("missing agent rows: %+v", m.ByAgent)
	}
	if !almost(main.CostUSD, 0.90) {
		t.Errorf("main-session cost = %v, want 0.90", main.CostUSD)
	}
	if !almost(impl.CostUSD, 0.50) {
		t.Errorf("implementation-agent cost = %v, want 0.50", impl.CostUSD)
	}
	if !almost(impl.CostShare, 0.50/2.80) {
		t.Errorf("implementation-agent share = %v, want %v", impl.CostShare, 0.50/2.80)
	}

	// slice B — task_delegations shape
	if m.Tasks != 2 || m.Delegations != 4 {
		t.Errorf("tasks/delegations = %d/%d, want 2/4", m.Tasks, m.Delegations)
	}
	if m.MaxDepth != 3 {
		t.Errorf("MaxDepth = %d, want 3", m.MaxDepth)
	}
	if m.RepeatTasks != 1 || !almost(m.RepeatRate, 0.5) {
		t.Errorf("repeats = %d (%v), want 1 (0.5)", m.RepeatTasks, m.RepeatRate)
	}
	if !almost(m.AvgWidth, 1.5) {
		t.Errorf("AvgWidth = %v, want 1.5", m.AvgWidth)
	}
	// quality: task 1 averages 4 and 5; task 2's only rating is 0, which
	// NULLIF turns into "unrated" rather than into a zero score.
	if m.QualityTasks != 1 || !almost(m.AvgQuality, 4.5) {
		t.Errorf("quality = %v over %d tasks, want 4.5 over 1", m.AvgQuality, m.QualityTasks)
	}
	for _, s := range m.Shapes {
		if s.TaskID == 2 && s.HasQuality {
			t.Error("task 2 has only a 0 rating; it must not count as rated")
		}
	}
}

func TestWasteFourComponentsStaySeparate(t *testing.T) {
	m := mustCompute(t, newFixtureDB(t), Options{}).Waste

	if m.Reverted.Tasks != 1 || !almost(m.Reverted.CostUSD, 0.25) {
		t.Errorf("reverted = %d tasks / %v, want 1 / 0.25", m.Reverted.Tasks, m.Reverted.CostUSD)
	}
	if m.Retried.Tasks != 1 || !almost(m.Retried.CostUSD, 0.35) {
		t.Errorf("retried = %d tasks / %v, want 1 / 0.35", m.Retried.Tasks, m.Retried.CostUSD)
	}
	if m.Retries != 2 {
		t.Errorf("Retries = %d, want 2", m.Retries)
	}
	// only task 1 exceeded one loop
	if m.Looped.Tasks != 1 || !almost(m.Looped.CostUSD, 0.60) {
		t.Errorf("looped = %d tasks / %v, want 1 / 0.60", m.Looped.Tasks, m.Looped.CostUSD)
	}
	if m.Stranded.Tasks != 1 || !almost(m.Stranded.CostUSD, 0.40) {
		t.Errorf("stranded = %d tasks / %v, want 1 / 0.40", m.Stranded.Tasks, m.Stranded.CostUSD)
	}
	for _, item := range m.Stranded.Items {
		if item.TaskID == 4 {
			t.Error("task 4 still has a live session and must not count as stranded")
		}
	}
	// The retried component's coverage sees the user turn as unpriced-by-absence.
	if !almost(m.Retried.Coverage, 0.5) {
		t.Errorf("retried coverage = %v, want 0.5 (1 of 2 turns priced)", m.Retried.Coverage)
	}
}

// TestRetriedCountsBothBudgets pins the honesty fix: "retried" reads BOTH re-dispatch
// counters. 0051 split one column into two — retry_count (the dispatcher's no-progress
// heal) and verify_retry_count (the verification fix-chain) — and for as long as this
// metric read only the first, every task re-run because its verification failed was
// invisible waste while the label claimed to count retries.
func TestRetriedCountsBothBudgets(t *testing.T) {
	db := newFixtureDB(t)

	// Task 1 has zero dispatch retries and TWO verify fix-chain retries: under the old
	// query it was not "retried" at all. It is linked to sessions 1+2 (cost 0.10+0.50).
	mustExec(t, db, `UPDATE tasks SET verify_retry_count=2 WHERE id=1`)
	// Task 6 already has retry_count=2; give it one verify retry so the sum, not the
	// max and not either column alone, is what the total reflects.
	mustExec(t, db, `UPDATE tasks SET verify_retry_count=1 WHERE id=6`)

	m := mustCompute(t, db, Options{}).Waste

	if m.Retried.Tasks != 2 {
		t.Errorf("retried tasks = %d, want 2 — a verify-only retry is still a retry", m.Retried.Tasks)
	}
	// 2 (task 1 verify) + 2 (task 6 dispatch) + 1 (task 6 verify) = 5.
	if m.Retries != 5 {
		t.Errorf("Retries = %d, want 5 (retry_count + verify_retry_count across both tasks)", m.Retries)
	}
	if !almost(m.Retried.CostUSD, 0.95) {
		t.Errorf("retried cost = %v, want 0.95 (task 1's 0.60 + task 6's 0.35)", m.Retried.CostUSD)
	}
}

func TestModelMix(t *testing.T) {
	m := mustCompute(t, newFixtureDB(t), Options{}).ModelMix
	if len(m.Rows) != 4 {
		t.Fatalf("models = %d, want 4", len(m.Rows))
	}
	if !almost(m.TotalCost, 2.80) {
		t.Errorf("TotalCost = %v, want 2.80", m.TotalCost)
	}
	want := []string{"model-c", "model-a", "model-b", "model-unknown"}
	for i, w := range want {
		if m.Rows[i].Model != w {
			t.Errorf("row %d = %q, want %q (cost desc)", i, m.Rows[i].Model, w)
		}
	}
	// The unknown model priced nothing, so its cost is 0 with zero priced turns
	// — the sum is not lying, the coverage explains it.
	last := m.Rows[3]
	if last.Turns != 2 || last.TurnsPriced != 0 || !almost(last.CostUSD, 0) {
		t.Errorf("model-unknown = %+v, want 2 turns / 0 priced / 0 cost", last)
	}
	var sum float64
	for _, r := range m.Rows {
		sum += r.CostShare
	}
	if !almost(sum, 1.0) {
		t.Errorf("shares sum to %v, want 1.0", sum)
	}
}

// TestRenderIsDeterministic is the guarantee that a baseline captured today can
// be diffed against one captured later: same Report in, same bytes out.
func TestRenderIsDeterministic(t *testing.T) {
	rep := mustCompute(t, newFixtureDB(t), Options{})
	rep.GeneratedAt = "" // the single non-deterministic field

	for _, asJSON := range []bool{false, true} {
		var first, second bytes.Buffer
		if err := Render(&first, rep, asJSON); err != nil {
			t.Fatalf("render (json=%v): %v", asJSON, err)
		}
		if err := Render(&second, rep, asJSON); err != nil {
			t.Fatalf("render (json=%v): %v", asJSON, err)
		}
		if !bytes.Equal(first.Bytes(), second.Bytes()) {
			t.Errorf("render (json=%v) is not byte-stable", asJSON)
		}
		if first.Len() == 0 {
			t.Errorf("render (json=%v) produced nothing", asJSON)
		}
	}
}

func TestRenderJSONRoundTrips(t *testing.T) {
	rep := mustCompute(t, newFixtureDB(t), Options{})
	var buf bytes.Buffer
	if err := Render(&buf, rep, true); err != nil {
		t.Fatalf("render json: %v", err)
	}
	var back Report
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatalf("emitted JSON does not parse: %v", err)
	}
	if back.Sample.Turns != rep.Sample.Turns {
		t.Errorf("round-tripped turns = %d, want %d", back.Sample.Turns, rep.Sample.Turns)
	}
	if !almost(back.ModelMix.TotalCost, rep.ModelMix.TotalCost) {
		t.Errorf("round-tripped total = %v, want %v", back.ModelMix.TotalCost, rep.ModelMix.TotalCost)
	}
}

func TestRenderTextMentionsEveryMetricAndTheSampleBlock(t *testing.T) {
	rep := mustCompute(t, newFixtureDB(t), Options{})
	var buf bytes.Buffer
	if err := Render(&buf, rep, false); err != nil {
		t.Fatalf("render text: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"SAMPLE", "sessions", "turns priced", "turns unpriced", "turns no agent",
		"tasks done", "delegations",
		"COST PER COMPLETED TASK", "explicit only",
		"CACHE EFFICIENCY", "cache_hit",
		"DELEGATION COST", "task_delegations",
		"WASTED WORK", "reverted", "retried", "looped", "stranded",
		"MODEL MIX",
		"low-coverage",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text render is missing %q", want)
		}
	}
}

func TestRenderNilReport(t *testing.T) {
	if err := Render(&bytes.Buffer{}, nil, false); err == nil {
		t.Error("Render(nil) must fail rather than print an empty report")
	}
}

// TestPackageIssuesNoWrites enforces the read-only contract mechanically: the
// daemon may be serving the same WAL, so a write statement must never appear.
func TestPackageIssuesNoWrites(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	write := regexp.MustCompile(`(?i)\b(INSERT\s+INTO|UPDATE\s+\w+\s+SET|DELETE\s+FROM|DROP\s+TABLE|ALTER\s+TABLE)\b`)
	var scanned int
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if loc := write.FindString(string(body)); loc != "" {
			t.Errorf("%s contains a write statement (%q) — this package must stay read-only", name, loc)
		}
		scanned++
	}
	if scanned == 0 {
		t.Fatal("scanned no source files; the read-only guard is not actually running")
	}
}
