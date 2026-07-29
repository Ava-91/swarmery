// FNXC:AgentEconomics 2026-07-29-22:26 — every number in an economics report must be traceable to one named, read-only SQL statement, so the audit can be re-run and disputed against the exact query that produced it.

package economics

import (
	"database/sql"
	"fmt"
	"sort"
)

// turnScope and taskScope are the two window/project filters. They share one
// bind order — since, since, until, until, project, project — so every query
// in this file takes Options.args() unchanged.
//
// A "?" compared against '' or 0 is how an unset bound disables its own
// predicate: with the zero Options the whole history of every project matches.
const turnScope = `(? = '' OR substr(tu.started_at, 1, 10) >= ?)
      AND (? = '' OR substr(tu.started_at, 1, 10) <= ?)
      AND (? = 0  OR s.project_id = ?)`

const taskScope = `(? = '' OR substr(t.created_at, 1, 10) >= ?)
      AND (? = '' OR substr(t.created_at, 1, 10) <= ?)
      AND (? = 0  OR t.project_id = ?)`

// ---------------------------------------------------------------- sample ----

// qSample counts the rows behind the report. TurnsUnpriced counts turns that
// carry usage but no cost — the unknown-model case that internal/cost stores
// as NULL rather than 0. Turns without any usage at all (user turns) are
// neither priced nor unpriced.
const qSample = `
SELECT COUNT(*),
       COUNT(DISTINCT tu.session_id),
       SUM(CASE WHEN tu.cost_usd IS NOT NULL THEN 1 ELSE 0 END),
       SUM(CASE WHEN tu.cost_usd IS NULL
                 AND (tu.tokens_in IS NOT NULL OR tu.tokens_out IS NOT NULL
                   OR tu.tokens_cache_read IS NOT NULL OR tu.tokens_cache_write IS NOT NULL)
                THEN 1 ELSE 0 END),
       SUM(CASE WHEN tu.role = 'assistant' AND tu.agent_name IS NULL THEN 1 ELSE 0 END)
FROM turns tu
JOIN sessions s ON s.id = tu.session_id
WHERE ` + turnScope

const qTaskSample = `
SELECT COUNT(*), SUM(CASE WHEN t.status = 'done' THEN 1 ELSE 0 END)
FROM tasks t
WHERE ` + taskScope

const qDelegationCount = `
SELECT COUNT(*)
FROM task_delegations d
JOIN tasks t ON t.id = d.task_id
WHERE ` + taskScope

// ------------------------------------------------------- task cost shape ----

// The task-cost queries share one column list so a single scanner serves all
// of them. priced (the last column) is what makes Coverage computable: it is
// the count of turns that actually carried a cost, never a NULL treated as 0.
const taskCostHead = `
       t.id,
       t.title,
       COALESCE(t.started_at, ''),
       t.retry_count,
       `

const taskCostTail = `,
       SUM(tu.cost_usd),
       SUM(tu.tokens_in),
       SUM(tu.tokens_out),
       COUNT(tu.id),
       SUM(CASE WHEN tu.cost_usd IS NOT NULL THEN 1 ELSE 0 END)`

const taskCostJoin = `
FROM tasks t
JOIN task_sessions ts ON ts.task_id = t.id
JOIN turns tu         ON tu.session_id = ts.session_id`

// --------------------------------------------- metric 1: cost per task ------

// qCostPerTask: cost of every session linked to a done task. link_source is
// carried through so the audit can state how much of the number rests on
// heuristic inference rather than on an explicit link.
const qCostPerTask = `SELECT` + taskCostHead + `ts.link_source` + taskCostTail + taskCostJoin + `
WHERE t.status = 'done' AND ` + taskScope + `
GROUP BY t.id, ts.link_source
ORDER BY t.id, ts.link_source`

// ------------------------------------------ metric 2: cache efficiency ------

// qCacheEfficiency: cache read share of prompt-side tokens, per agent and
// model. tokens_in excludes cache reads in the Claude usage shape, so the
// ratio is cache_read / (cache_read + tokens_in) — the share of prompt bytes
// served from cache rather than re-billed at the full input rate.
const qCacheEfficiency = `
SELECT COALESCE(tu.agent_name, '(main-session)') AS agent,
       COALESCE(tu.model, '(unknown)')           AS model,
       SUM(tu.tokens_in),
       SUM(tu.tokens_cache_read),
       SUM(tu.tokens_cache_write),
       SUM(tu.cost_usd),
       COUNT(*),
       SUM(CASE WHEN tu.cost_usd IS NOT NULL THEN 1 ELSE 0 END)
FROM turns tu
JOIN sessions s ON s.id = tu.session_id
WHERE tu.role = 'assistant' AND ` + turnScope + `
GROUP BY agent, model
ORDER BY SUM(tu.cost_usd) DESC, agent, model`

// ---------------------------------------------- metric 3: delegation --------

// qCostByAgent is the first delegation slice: what subagent-attributed turns
// cost against main-session turns. Its confidence limit is Sample.TurnsNoAgent.
const qCostByAgent = `
SELECT COALESCE(tu.agent_name, '(main-session)') AS agent,
       COUNT(*),
       SUM(tu.tokens_in),
       SUM(tu.tokens_out),
       SUM(tu.cost_usd),
       SUM(CASE WHEN tu.cost_usd IS NOT NULL THEN 1 ELSE 0 END)
FROM turns tu
JOIN sessions s ON s.id = tu.session_id
WHERE tu.role = 'assistant' AND ` + turnScope + `
GROUP BY agent
ORDER BY SUM(tu.cost_usd) DESC, agent`

// qDelegationShape is the second slice: width (distinct agents), depth
// (max seq) and repeat rate per task, with the ledger's loops and quality.
const qDelegationShape = `
SELECT d.task_id,
       COUNT(*),
       COUNT(DISTINCT d.agent),
       MAX(d.seq),
       SUM(COALESCE(d.loops, 0)),
       AVG(NULLIF(d.quality, 0))
FROM task_delegations d
JOIN tasks t ON t.id = d.task_id
WHERE ` + taskScope + `
GROUP BY d.task_id
ORDER BY d.task_id`

// -------------------------------------------------- metric 4: waste ---------

// qWasteReverted: tasks explicitly marked as rolled back.
const qWasteReverted = `SELECT` + taskCostHead + `''` + taskCostTail + taskCostJoin + `
WHERE t.reverted = 1 AND ` + taskScope + `
GROUP BY t.id
ORDER BY t.id`

// qWasteRetried: tasks that needed at least one re-dispatch.
const qWasteRetried = `SELECT` + taskCostHead + `''` + taskCostTail + taskCostJoin + `
WHERE t.retry_count > 0 AND ` + taskScope + `
GROUP BY t.id
ORDER BY t.id`

// qRetrySum totals the re-dispatches themselves, which the cost column cannot
// express: no schema column attributes a turn to an attempt.
const qRetrySum = `
SELECT COALESCE(SUM(t.retry_count), 0)
FROM tasks t
WHERE t.retry_count > 0 AND ` + taskScope

// qWasteLooped: tasks whose quality gate re-dispatched more than once.
const qWasteLooped = `SELECT` + taskCostHead + `''` + taskCostTail + taskCostJoin + `
WHERE ` + taskScope + `
  AND t.id IN (SELECT task_id FROM task_loops GROUP BY task_id HAVING MAX(loop_n) > 1)
GROUP BY t.id
ORDER BY t.id`

// qLoopBuckets is the loop_n breakdown behind the looped component.
const qLoopBuckets = `
SELECT l.loop_n, COUNT(DISTINCT l.task_id)
FROM task_loops l
JOIN tasks t ON t.id = l.task_id
WHERE ` + taskScope + `
GROUP BY l.loop_n
ORDER BY l.loop_n`

// qWasteStranded: tasks stuck in running whose linked sessions have all
// ended — spend with no verdict at the other end.
const qWasteStranded = `SELECT` + taskCostHead + `''` + taskCostTail + `
FROM tasks t
JOIN task_sessions ts ON ts.task_id = t.id
JOIN sessions s       ON s.id = ts.session_id
JOIN turns tu         ON tu.session_id = s.id
WHERE t.status = 'running' AND ` + taskScope + `
GROUP BY t.id
HAVING SUM(CASE WHEN s.ended_at IS NULL THEN 1 ELSE 0 END) = 0
ORDER BY t.id`

// ---------------------------------------------- metric 5: model mix ---------

const qModelMix = `
SELECT COALESCE(tu.model, '(unknown)') AS model,
       COUNT(*),
       SUM(tu.tokens_in),
       SUM(tu.tokens_out),
       SUM(tu.cost_usd),
       SUM(CASE WHEN tu.cost_usd IS NOT NULL THEN 1 ELSE 0 END)
FROM turns tu
JOIN sessions s ON s.id = tu.session_id
WHERE tu.role = 'assistant' AND ` + turnScope + `
GROUP BY model
ORDER BY SUM(tu.cost_usd) DESC, model`

// ------------------------------------------------------------ loaders -------

func loadSample(db *sql.DB, o Options) (Sample, error) {
	var s Sample
	var priced, unpriced, noAgent sql.NullInt64
	if err := db.QueryRow(qSample, o.args()...).
		Scan(&s.Turns, &s.Sessions, &priced, &unpriced, &noAgent); err != nil {
		return s, fmt.Errorf("economics: sample turns: %w", err)
	}
	s.TurnsPriced = int(nullInt(priced))
	s.TurnsUnpriced = int(nullInt(unpriced))
	s.TurnsNoAgent = int(nullInt(noAgent))

	var done sql.NullInt64
	if err := db.QueryRow(qTaskSample, o.args()...).Scan(&s.Tasks, &done); err != nil {
		return s, fmt.Errorf("economics: sample tasks: %w", err)
	}
	s.TasksDone = int(nullInt(done))

	if err := db.QueryRow(qDelegationCount, o.args()...).Scan(&s.Delegations); err != nil {
		return s, fmt.Errorf("economics: sample delegations: %w", err)
	}
	return s, nil
}

// loadTaskCosts runs any query built from the shared task-cost column list and
// returns its rows plus the turn totals needed for Coverage.
func loadTaskCosts(db *sql.DB, query string, args []any) (rowsOut []TaskCost, turns, priced int, err error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("economics: task costs: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			tc                 TaskCost
			cost               sql.NullFloat64
			tokensIn, tokenOut sql.NullInt64
		)
		if err := rows.Scan(&tc.TaskID, &tc.Title, &tc.StartedAt, &tc.RetryCount,
			&tc.LinkSource, &cost, &tokensIn, &tokenOut, &tc.Turns, &tc.TurnsPriced); err != nil {
			return nil, 0, 0, fmt.Errorf("economics: scan task cost: %w", err)
		}
		// SUM() over an all-NULL group is NULL, not 0: the task had turns but
		// none of them could be priced. Coverage — not the sum — is what
		// carries that fact to the reader.
		tc.CostUSD = nullFloat(cost)
		tc.TokensIn = nullInt(tokensIn)
		tc.TokensOut = nullInt(tokenOut)
		turns += tc.Turns
		priced += tc.TurnsPriced
		rowsOut = append(rowsOut, tc)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, fmt.Errorf("economics: task costs: %w", err)
	}
	return rowsOut, turns, priced, nil
}

// sortTaskCosts imposes a total order (cost desc, then id, then link_source)
// so the rendered table never depends on scan order.
func sortTaskCosts(rows []TaskCost) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].CostUSD != rows[j].CostUSD {
			return rows[i].CostUSD > rows[j].CostUSD
		}
		if rows[i].TaskID != rows[j].TaskID {
			return rows[i].TaskID < rows[j].TaskID
		}
		return rows[i].LinkSource < rows[j].LinkSource
	})
}

func loadCostPerTask(db *sql.DB, o Options) (CostPerTaskMetric, error) {
	var m CostPerTaskMetric
	rows, turns, priced, err := loadTaskCosts(db, qCostPerTask, o.args())
	if err != nil {
		return m, err
	}
	m.Coverage = coverage(priced, turns)

	// One task can hold both an explicit and a heuristic link, so it appears
	// once per link_source. Per-task totals are summed back up here; the
	// explicit-only slice is the same arithmetic over explicit rows alone.
	allByTask := map[int64]float64{}
	explicitByTask := map[int64]float64{}
	var order []int64
	for _, r := range rows {
		if _, seen := allByTask[r.TaskID]; !seen {
			order = append(order, r.TaskID)
		}
		allByTask[r.TaskID] += r.CostUSD
		if r.LinkSource == linkExplicit {
			explicitByTask[r.TaskID] += r.CostUSD
		}
	}
	// order follows the query's ORDER BY t.id, so both value slices are built
	// in a fixed sequence; distribution() sorts a copy anyway.
	allValues := make([]float64, 0, len(order))
	explicitValues := make([]float64, 0, len(order))
	for _, id := range order {
		allValues = append(allValues, allByTask[id])
		if v, ok := explicitByTask[id]; ok {
			explicitValues = append(explicitValues, v)
		}
	}
	m.All = distribution(allValues)
	m.Explicit = distribution(explicitValues)

	sortTaskCosts(rows)
	m.Tasks = rows
	return m, nil
}

func loadCacheEfficiency(db *sql.DB, o Options) (CacheEfficiencyMetric, error) {
	var m CacheEfficiencyMetric
	rows, err := db.Query(qCacheEfficiency, o.args()...)
	if err != nil {
		return m, fmt.Errorf("economics: cache efficiency: %w", err)
	}
	defer rows.Close()

	var turns, priced int
	for rows.Next() {
		var (
			r                            CacheRow
			in, cacheRead, cacheWrite    sql.NullInt64
			cost                         sql.NullFloat64
			rowTurns, rowPricedTurnCount int
		)
		if err := rows.Scan(&r.Agent, &r.Model, &in, &cacheRead, &cacheWrite,
			&cost, &rowTurns, &rowPricedTurnCount); err != nil {
			return m, fmt.Errorf("economics: scan cache row: %w", err)
		}
		r.TokensIn = nullInt(in)
		r.CacheRead = nullInt(cacheRead)
		r.CacheWrite = nullInt(cacheWrite)
		r.CostUSD = nullFloat(cost)
		r.Turns = rowTurns
		r.TurnsPriced = rowPricedTurnCount
		r.CacheHit = share(float64(r.CacheRead), float64(r.CacheRead+r.TokensIn))

		turns += r.Turns
		priced += r.TurnsPriced
		m.Overall.TokensIn += r.TokensIn
		m.Overall.CacheRead += r.CacheRead
		m.Overall.CacheWrite += r.CacheWrite
		m.Overall.CostUSD += r.CostUSD
		m.Overall.Turns += r.Turns
		m.Overall.TurnsPriced += r.TurnsPriced
		m.Rows = append(m.Rows, r)
	}
	if err := rows.Err(); err != nil {
		return m, fmt.Errorf("economics: cache efficiency: %w", err)
	}

	m.Overall.Agent = "(all)"
	m.Overall.Model = "(all)"
	m.Overall.CacheHit = share(float64(m.Overall.CacheRead),
		float64(m.Overall.CacheRead+m.Overall.TokensIn))
	m.Coverage = coverage(priced, turns)

	// The SQL already orders by cost; re-sorting in Go makes the order
	// independent of how the driver ranks NULL sums.
	sort.SliceStable(m.Rows, func(i, j int) bool {
		if m.Rows[i].CostUSD != m.Rows[j].CostUSD {
			return m.Rows[i].CostUSD > m.Rows[j].CostUSD
		}
		if m.Rows[i].Agent != m.Rows[j].Agent {
			return m.Rows[i].Agent < m.Rows[j].Agent
		}
		return m.Rows[i].Model < m.Rows[j].Model
	})
	return m, nil
}

func loadDelegation(db *sql.DB, o Options) (DelegationMetric, error) {
	var m DelegationMetric

	byAgent, err := db.Query(qCostByAgent, o.args()...)
	if err != nil {
		return m, fmt.Errorf("economics: delegation by agent: %w", err)
	}
	var turns, priced int
	var totalCost float64
	for byAgent.Next() {
		var (
			a       AgentCost
			in, out sql.NullInt64
			cost    sql.NullFloat64
		)
		if err := byAgent.Scan(&a.Agent, &a.Turns, &in, &out, &cost, &a.TurnsPriced); err != nil {
			byAgent.Close()
			return m, fmt.Errorf("economics: scan agent cost: %w", err)
		}
		a.TokensIn = nullInt(in)
		a.TokensOut = nullInt(out)
		a.CostUSD = nullFloat(cost)
		turns += a.Turns
		priced += a.TurnsPriced
		totalCost += a.CostUSD
		m.ByAgent = append(m.ByAgent, a)
	}
	byAgent.Close()
	if err := byAgent.Err(); err != nil {
		return m, fmt.Errorf("economics: delegation by agent: %w", err)
	}
	for i := range m.ByAgent {
		m.ByAgent[i].CostShare = share(m.ByAgent[i].CostUSD, totalCost)
	}
	sort.SliceStable(m.ByAgent, func(i, j int) bool {
		if m.ByAgent[i].CostUSD != m.ByAgent[j].CostUSD {
			return m.ByAgent[i].CostUSD > m.ByAgent[j].CostUSD
		}
		return m.ByAgent[i].Agent < m.ByAgent[j].Agent
	})
	m.Coverage = coverage(priced, turns)

	shapes, err := db.Query(qDelegationShape, o.args()...)
	if err != nil {
		return m, fmt.Errorf("economics: delegation shape: %w", err)
	}
	defer shapes.Close()

	var (
		widthSum   int
		loopSum    int
		qualitySum float64
	)
	for shapes.Next() {
		var (
			s       DelegationShape
			maxSeq  sql.NullInt64
			loops   sql.NullInt64
			quality sql.NullFloat64
		)
		if err := shapes.Scan(&s.TaskID, &s.Delegations, &s.DistinctAgents,
			&maxSeq, &loops, &quality); err != nil {
			return m, fmt.Errorf("economics: scan delegation shape: %w", err)
		}
		s.MaxSeq = int(nullInt(maxSeq))
		s.Loops = int(nullInt(loops))
		// AVG(NULLIF(quality,0)) is NULL when a task has no rated delegation.
		// Recording that as 0 would drag every fleet-wide average down, so the
		// flag carries the absence instead.
		if quality.Valid {
			s.AvgQuality = quality.Float64
			s.HasQuality = true
			qualitySum += quality.Float64
			m.QualityTasks++
		}
		if s.Delegations > s.DistinctAgents {
			m.RepeatTasks++
		}
		if s.MaxSeq > m.MaxDepth {
			m.MaxDepth = s.MaxSeq
		}
		widthSum += s.DistinctAgents
		loopSum += s.Loops
		m.Delegations += s.Delegations
		m.Shapes = append(m.Shapes, s)
	}
	if err := shapes.Err(); err != nil {
		return m, fmt.Errorf("economics: delegation shape: %w", err)
	}

	m.Tasks = len(m.Shapes)
	if m.Tasks > 0 {
		m.AvgWidth = float64(widthSum) / float64(m.Tasks)
		m.AvgLoops = float64(loopSum) / float64(m.Tasks)
		m.RepeatRate = float64(m.RepeatTasks) / float64(m.Tasks)
	}
	if m.QualityTasks > 0 {
		m.AvgQuality = qualitySum / float64(m.QualityTasks)
	}
	return m, nil
}

func loadWaste(db *sql.DB, o Options) (WasteMetric, error) {
	var m WasteMetric

	components := []struct {
		dst   *WasteComponent
		label string
		query string
	}{
		{&m.Reverted, "reverted (tasks.reverted = 1)", qWasteReverted},
		{&m.Retried, "retried (tasks.retry_count > 0)", qWasteRetried},
		{&m.Looped, "looped (task_loops, > 1 loop)", qWasteLooped},
		{&m.Stranded, "stranded (running, all sessions ended)", qWasteStranded},
	}
	for _, c := range components {
		rows, turns, priced, err := loadTaskCosts(db, c.query, o.args())
		if err != nil {
			return m, err
		}
		sortTaskCosts(rows)
		comp := WasteComponent{
			Label:       c.label,
			Tasks:       len(rows),
			Turns:       turns,
			TurnsPriced: priced,
			Coverage:    coverage(priced, turns),
			Items:       rows,
		}
		for _, r := range rows {
			comp.CostUSD += r.CostUSD
		}
		*c.dst = comp
	}

	if err := db.QueryRow(qRetrySum, o.args()...).Scan(&m.Retries); err != nil {
		return m, fmt.Errorf("economics: retry sum: %w", err)
	}

	buckets, err := db.Query(qLoopBuckets, o.args()...)
	if err != nil {
		return m, fmt.Errorf("economics: loop buckets: %w", err)
	}
	defer buckets.Close()
	for buckets.Next() {
		var b LoopBucket
		if err := buckets.Scan(&b.LoopN, &b.Tasks); err != nil {
			return m, fmt.Errorf("economics: scan loop bucket: %w", err)
		}
		m.ByLoopN = append(m.ByLoopN, b)
	}
	if err := buckets.Err(); err != nil {
		return m, fmt.Errorf("economics: loop buckets: %w", err)
	}
	return m, nil
}

func loadModelMix(db *sql.DB, o Options) (ModelMixMetric, error) {
	var m ModelMixMetric
	rows, err := db.Query(qModelMix, o.args()...)
	if err != nil {
		return m, fmt.Errorf("economics: model mix: %w", err)
	}
	defer rows.Close()

	var turns, priced int
	for rows.Next() {
		var (
			r       ModelCost
			in, out sql.NullInt64
			cost    sql.NullFloat64
		)
		if err := rows.Scan(&r.Model, &r.Turns, &in, &out, &cost, &r.TurnsPriced); err != nil {
			return m, fmt.Errorf("economics: scan model row: %w", err)
		}
		r.TokensIn = nullInt(in)
		r.TokensOut = nullInt(out)
		r.CostUSD = nullFloat(cost)
		turns += r.Turns
		priced += r.TurnsPriced
		m.TotalCost += r.CostUSD
		m.Rows = append(m.Rows, r)
	}
	if err := rows.Err(); err != nil {
		return m, fmt.Errorf("economics: model mix: %w", err)
	}
	for i := range m.Rows {
		m.Rows[i].CostShare = share(m.Rows[i].CostUSD, m.TotalCost)
	}
	sort.SliceStable(m.Rows, func(i, j int) bool {
		if m.Rows[i].CostUSD != m.Rows[j].CostUSD {
			return m.Rows[i].CostUSD > m.Rows[j].CostUSD
		}
		return m.Rows[i].Model < m.Rows[j].Model
	})
	m.Coverage = coverage(priced, turns)
	return m, nil
}
