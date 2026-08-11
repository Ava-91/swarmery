// FNXC:AgentEconomics 2026-07-29-22:26 — an audit number is only usable if the same database renders the same bytes twice, so the report types and their renderer are deterministic by construction and carry their own coverage caveats.

package economics

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// linkExplicit is the task_sessions.link_source value for a link the workspace
// stated outright; anything else was inferred by the heuristic linker.
const linkExplicit = "explicit"

// coverageFloor is the priced share below which a metric is flagged. Below it
// the sums are missing enough rows that quoting them without the caveat would
// misrepresent the fleet.
const coverageFloor = 0.8

// topN bounds the per-task listings in the text render. The JSON report always
// carries every row.
const topN = 10

// titleWidth truncates task titles in the text render only.
const titleWidth = 44

// ------------------------------------------------------------- metrics -----

// CostDistribution summarises per-task costs. Median and P90 use the
// nearest-rank method (see percentile).
type CostDistribution struct {
	Tasks  int     `json:"tasks"`
	Sum    float64 `json:"sum_usd"`
	Mean   float64 `json:"mean_usd"`
	Median float64 `json:"median_usd"`
	P90    float64 `json:"p90_usd"`
	Min    float64 `json:"min_usd"`
	Max    float64 `json:"max_usd"`
}

// TaskCost is one task's spend. In the cost-per-task metric a task appears
// once per link_source; in the waste components LinkSource is empty.
type TaskCost struct {
	TaskID      int64   `json:"task_id"`
	Title       string  `json:"title"`
	StartedAt   string  `json:"started_at,omitempty"`
	RetryCount  int     `json:"retry_count"`
	LinkSource  string  `json:"link_source,omitempty"`
	CostUSD     float64 `json:"cost_usd"`
	TokensIn    int64   `json:"tokens_in"`
	TokensOut   int64   `json:"tokens_out"`
	Turns       int     `json:"turns"`
	TurnsPriced int     `json:"turns_priced"`
}

// CostPerTaskMetric is metric 1. Explicit repeats the arithmetic over
// explicitly linked sessions only: a 31-task sample cannot also absorb the
// error heuristic links add.
type CostPerTaskMetric struct {
	Coverage float64          `json:"coverage"`
	All      CostDistribution `json:"all"`
	Explicit CostDistribution `json:"explicit_only"`
	Tasks    []TaskCost       `json:"tasks"`
}

// CacheRow is metric 2's grain: one agent × model pair.
type CacheRow struct {
	Agent       string  `json:"agent"`
	Model       string  `json:"model"`
	TokensIn    int64   `json:"tokens_in"`
	CacheRead   int64   `json:"cache_read"`
	CacheWrite  int64   `json:"cache_write"`
	CostUSD     float64 `json:"cost_usd"`
	Turns       int     `json:"turns"`
	TurnsPriced int     `json:"turns_priced"`
	CacheHit    float64 `json:"cache_hit"`
}

// CacheEfficiencyMetric is metric 2. High cost with a low cache_hit is the
// prompt-shape smell; this package only measures it.
type CacheEfficiencyMetric struct {
	Coverage float64    `json:"coverage"`
	Overall  CacheRow   `json:"overall"`
	Rows     []CacheRow `json:"rows"`
}

// AgentCost is the turns.agent_name slice of metric 3.
type AgentCost struct {
	Agent       string  `json:"agent"`
	Turns       int     `json:"turns"`
	TurnsPriced int     `json:"turns_priced"`
	TokensIn    int64   `json:"tokens_in"`
	TokensOut   int64   `json:"tokens_out"`
	CostUSD     float64 `json:"cost_usd"`
	CostShare   float64 `json:"cost_share"`
}

// DelegationShape is the task_delegations slice of metric 3: width, depth and
// repeat per task. HasQuality separates "rated 0" from "never rated".
type DelegationShape struct {
	TaskID         int64   `json:"task_id"`
	Delegations    int     `json:"delegations"`
	DistinctAgents int     `json:"distinct_agents"`
	MaxSeq         int     `json:"max_seq"`
	Loops          int     `json:"loops"`
	AvgQuality     float64 `json:"avg_quality,omitempty"`
	HasQuality     bool    `json:"has_quality"`
}

// DelegationMetric is metric 3. Neither slice is complete on its own: the
// agent_name slice misses turns the ingester could not attribute, and the
// ledger slice only covers tasks whose ORCHESTRATION/agents log was ingested.
type DelegationMetric struct {
	Coverage     float64           `json:"coverage"`
	ByAgent      []AgentCost       `json:"by_agent"`
	Tasks        int               `json:"tasks"`
	Delegations  int               `json:"delegations"`
	AvgWidth     float64           `json:"avg_width"`
	MaxDepth     int               `json:"max_depth"`
	RepeatTasks  int               `json:"repeat_tasks"`
	RepeatRate   float64           `json:"repeat_rate"`
	AvgLoops     float64           `json:"avg_loops"`
	AvgQuality   float64           `json:"avg_quality"`
	QualityTasks int               `json:"quality_tasks"`
	Shapes       []DelegationShape `json:"shapes"`
}

// WasteComponent is one of metric 4's four counts.
type WasteComponent struct {
	Label       string     `json:"label"`
	Tasks       int        `json:"tasks"`
	Turns       int        `json:"turns"`
	TurnsPriced int        `json:"turns_priced"`
	CostUSD     float64    `json:"cost_usd"`
	Coverage    float64    `json:"coverage"`
	Items       []TaskCost `json:"items"`
}

// LoopBucket is the task count at one quality-gate loop number.
type LoopBucket struct {
	LoopN int `json:"loop_n"`
	Tasks int `json:"tasks"`
}

// WasteMetric is metric 4. The four components are deliberately NOT summed
// into one number: the definition of wasted work is contested, and the
// components overlap (a reverted task may also have been retried).
type WasteMetric struct {
	Reverted WasteComponent `json:"reverted"`
	Retried  WasteComponent `json:"retried"`
	Retries  int            `json:"retries"`
	Looped   WasteComponent `json:"looped"`
	ByLoopN  []LoopBucket   `json:"by_loop_n"`
	Stranded WasteComponent `json:"stranded"`
}

// ModelCost is one row of metric 5.
type ModelCost struct {
	Model       string  `json:"model"`
	Turns       int     `json:"turns"`
	TurnsPriced int     `json:"turns_priced"`
	TokensIn    int64   `json:"tokens_in"`
	TokensOut   int64   `json:"tokens_out"`
	CostUSD     float64 `json:"cost_usd"`
	CostShare   float64 `json:"cost_share"`
}

// ModelMixMetric is metric 5: where the money actually went, by model.
type ModelMixMetric struct {
	Coverage  float64     `json:"coverage"`
	TotalCost float64     `json:"total_usd"`
	Rows      []ModelCost `json:"rows"`
}

// -------------------------------------------------------------- render -----

// Render writes rep as aligned text, or as JSON when asJSON is set. Both forms
// are byte-stable for an unchanged Report: no map is ever ranged over, and
// every slice was given a total order at load time.
func Render(w io.Writer, rep *Report, asJSON bool) error {
	if rep == nil {
		return fmt.Errorf("economics: nil report")
	}
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(rep)
	}
	var b bytes.Buffer
	renderText(&b, rep)
	_, err := w.Write(b.Bytes())
	return err
}

func renderText(b *bytes.Buffer, rep *Report) {
	fmt.Fprintf(b, "swarmery economics — token economy of the agent system\n")
	fmt.Fprintf(b, "generated  %s\n", rep.GeneratedAt)
	fmt.Fprintf(b, "database   %s\n", orDash(rep.DBPath))
	fmt.Fprintf(b, "window     %s\n", windowLabel(rep.Options))
	fmt.Fprintf(b, "project    %s\n", projectLabel(rep.Options))

	renderSample(b, rep.Sample)
	renderCostPerTask(b, rep.CostPerTask)
	renderCacheEfficiency(b, rep.CacheEff)
	renderDelegation(b, rep.Delegation, rep.Sample)
	renderWaste(b, rep.Waste)
	renderModelMix(b, rep.ModelMix)
}

func renderSample(b *bytes.Buffer, s Sample) {
	b.WriteString("\nSAMPLE\n")
	t := &table{cols: []column{{header: "row"}, {header: "count", right: true}, {header: "note"}}}
	t.add("sessions", itoa(s.Sessions), "")
	t.add("turns", itoa(s.Turns), "")
	t.add("turns priced", itoa(s.TurnsPriced), "cost_usd present")
	t.add("turns unpriced", itoa(s.TurnsUnpriced), "usage present, model unpriced → NULL, never 0")
	t.add("turns no agent", itoa(s.TurnsNoAgent), "agent_name NULL — delegation attribution limit")
	t.add("tasks", itoa(s.Tasks), "")
	t.add("tasks done", itoa(s.TasksDone), "")
	t.add("delegations", itoa(s.Delegations), "task_delegations rows")
	t.render(b, "  ")
}

func renderCostPerTask(b *bytes.Buffer, m CostPerTaskMetric) {
	section(b, 1, "COST PER COMPLETED TASK", coverageLabel(m.Coverage, len(m.Tasks)))
	b.WriteString("  NULL costs are skipped, never summed as 0 — coverage is the priced share of turns.\n")
	if len(m.Tasks) == 0 {
		b.WriteString("  (no completed tasks with linked sessions in this window)\n")
		return
	}
	t := &table{cols: []column{
		{header: "slice"},
		{header: "tasks", right: true},
		{header: "sum $", right: true},
		{header: "mean $", right: true},
		{header: "median $", right: true},
		{header: "p90 $", right: true},
		{header: "min $", right: true},
		{header: "max $", right: true},
	}}
	addDist := func(label string, d CostDistribution) {
		t.add(label, itoa(d.Tasks), usd(d.Sum), usd(d.Mean), usd(d.Median), usd(d.P90), usd(d.Min), usd(d.Max))
	}
	addDist("all links", m.All)
	addDist("explicit only", m.Explicit)
	t.render(b, "  ")

	b.WriteString("\n  top tasks by cost\n")
	tt := &table{cols: []column{
		{header: "task", right: true},
		{header: "link"},
		{header: "cost $", right: true},
		{header: "turns", right: true},
		{header: "priced", right: true},
		{header: "title"},
	}}
	for _, r := range head(m.Tasks, topN) {
		tt.add(i64toa(r.TaskID), orDash(r.LinkSource), usd(r.CostUSD),
			itoa(r.Turns), itoa(r.TurnsPriced), clip(r.Title, titleWidth))
	}
	tt.render(b, "  ")
	more(b, len(m.Tasks), topN, "tasks")
}

func renderCacheEfficiency(b *bytes.Buffer, m CacheEfficiencyMetric) {
	section(b, 2, "CACHE EFFICIENCY", coverageLabel(m.Coverage, len(m.Rows)))
	b.WriteString("  cache_hit = cache_read / (cache_read + tokens_in) — the share of prompt tokens\n")
	b.WriteString("  served from cache instead of being re-billed at the full input rate.\n")
	if len(m.Rows) == 0 {
		b.WriteString("  (no assistant turns in this window)\n")
		return
	}
	t := &table{cols: []column{
		{header: "agent"},
		{header: "model"},
		{header: "turns", right: true},
		{header: "cache_hit", right: true},
		{header: "cost $", right: true},
		{header: "cache_read", right: true},
		{header: "cache_write", right: true},
		{header: "tokens_in", right: true},
	}}
	for _, r := range head(m.Rows, topN*2) {
		t.add(r.Agent, r.Model, itoa(r.Turns), pct(r.CacheHit), usd(r.CostUSD),
			i64toa(r.CacheRead), i64toa(r.CacheWrite), i64toa(r.TokensIn))
	}
	o := m.Overall
	t.add(o.Agent, o.Model, itoa(o.Turns), pct(o.CacheHit), usd(o.CostUSD),
		i64toa(o.CacheRead), i64toa(o.CacheWrite), i64toa(o.TokensIn))
	t.render(b, "  ")
	more(b, len(m.Rows), topN*2, "agent×model rows")
}

func renderDelegation(b *bytes.Buffer, m DelegationMetric, s Sample) {
	section(b, 3, "DELEGATION COST", coverageLabel(m.Coverage, len(m.ByAgent)))
	fmt.Fprintf(b, "  slice A — turns.agent_name (attribution limit: %d turns carry no agent_name)\n",
		s.TurnsNoAgent)
	if len(m.ByAgent) == 0 {
		b.WriteString("  (no assistant turns in this window)\n")
	} else {
		t := &table{cols: []column{
			{header: "agent"},
			{header: "turns", right: true},
			{header: "cost $", right: true},
			{header: "share", right: true},
			{header: "tokens_in", right: true},
			{header: "tokens_out", right: true},
		}}
		for _, a := range head(m.ByAgent, topN*2) {
			t.add(a.Agent, itoa(a.Turns), usd(a.CostUSD), pct(a.CostShare),
				i64toa(a.TokensIn), i64toa(a.TokensOut))
		}
		t.render(b, "  ")
		more(b, len(m.ByAgent), topN*2, "agents")
	}

	b.WriteString("\n  slice B — task_delegations (shape of the delegation tree)\n")
	if m.Tasks == 0 {
		b.WriteString("  (no delegation ledger rows in this window)\n")
		return
	}
	fmt.Fprintf(b, "  tasks %d | delegations %d | avg width %.2f | max depth %d | repeats %d (%s) | avg loops %.2f | avg quality %s\n",
		m.Tasks, m.Delegations, m.AvgWidth, m.MaxDepth, m.RepeatTasks, pct(m.RepeatRate),
		m.AvgLoops, qualityLabel(m.AvgQuality, m.QualityTasks))
	t := &table{cols: []column{
		{header: "task", right: true},
		{header: "delegations", right: true},
		{header: "agents", right: true},
		{header: "depth", right: true},
		{header: "loops", right: true},
		{header: "quality", right: true},
	}}
	for _, sh := range head(m.Shapes, topN) {
		q := "—"
		if sh.HasQuality {
			q = fmt.Sprintf("%.2f", sh.AvgQuality)
		}
		t.add(i64toa(sh.TaskID), itoa(sh.Delegations), itoa(sh.DistinctAgents),
			itoa(sh.MaxSeq), itoa(sh.Loops), q)
	}
	t.render(b, "  ")
	more(b, len(m.Shapes), topN, "tasks")
}

func renderWaste(b *bytes.Buffer, m WasteMetric) {
	section(b, 4, "WASTED WORK", "")
	b.WriteString("  Four components, reported separately and never summed: the definition of\n")
	b.WriteString("  \"waste\" is contested and the components overlap (a reverted task may also\n")
	b.WriteString("  have been retried).\n")
	t := &table{cols: []column{
		{header: "component"},
		{header: "tasks", right: true},
		{header: "turns", right: true},
		{header: "cost $", right: true},
		{header: "coverage"},
	}}
	for _, c := range []WasteComponent{m.Reverted, m.Retried, m.Looped, m.Stranded} {
		t.add(c.Label, itoa(c.Tasks), itoa(c.Turns), usd(c.CostUSD),
			coverageLabel(c.Coverage, c.Turns))
	}
	t.render(b, "  ")

	fmt.Fprintf(b, "  retried: %d re-dispatches across %d tasks. The cost column is those tasks' FULL\n",
		m.Retries, m.Retried.Tasks)
	b.WriteString("  cost, not the incremental cost of the retries — no column attributes a turn to\n")
	b.WriteString("  an attempt, and inventing a split would be a guess dressed as a measurement.\n")

	if len(m.ByLoopN) > 0 {
		b.WriteString("  loops by loop_n:")
		for _, bucket := range m.ByLoopN {
			fmt.Fprintf(b, "  %d→%d tasks", bucket.LoopN, bucket.Tasks)
		}
		b.WriteString("\n")
	}

	for _, c := range []WasteComponent{m.Reverted, m.Retried, m.Looped, m.Stranded} {
		if len(c.Items) == 0 {
			continue
		}
		fmt.Fprintf(b, "\n  %s — top tasks\n", c.Label)
		tt := &table{cols: []column{
			{header: "task", right: true},
			{header: "cost $", right: true},
			{header: "turns", right: true},
			{header: "retries", right: true},
			{header: "title"},
		}}
		for _, r := range head(c.Items, topN) {
			tt.add(i64toa(r.TaskID), usd(r.CostUSD), itoa(r.Turns), itoa(r.RetryCount),
				clip(r.Title, titleWidth))
		}
		tt.render(b, "  ")
		more(b, len(c.Items), topN, "tasks")
	}
}

func renderModelMix(b *bytes.Buffer, m ModelMixMetric) {
	section(b, 5, "MODEL MIX", coverageLabel(m.Coverage, len(m.Rows)))
	if len(m.Rows) == 0 {
		b.WriteString("  (no assistant turns in this window)\n")
		return
	}
	fmt.Fprintf(b, "  total priced spend: $%s\n", usd(m.TotalCost))
	t := &table{cols: []column{
		{header: "model"},
		{header: "turns", right: true},
		{header: "cost $", right: true},
		{header: "share", right: true},
		{header: "tokens_in", right: true},
		{header: "tokens_out", right: true},
	}}
	for _, r := range m.Rows {
		t.add(r.Model, itoa(r.Turns), usd(r.CostUSD), pct(r.CostShare),
			i64toa(r.TokensIn), i64toa(r.TokensOut))
	}
	t.render(b, "  ")
}

// --------------------------------------------------------- render utils -----

func section(b *bytes.Buffer, n int, title, note string) {
	b.WriteString("\n")
	if note == "" {
		fmt.Fprintf(b, "%d  %s\n", n, title)
		return
	}
	fmt.Fprintf(b, "%d  %s  (%s)\n", n, title, note)
}

// coverageLabel renders a metric's priced share, flagging anything under the
// floor. rows == 0 means there was nothing to price, which is "n/a", not a
// coverage failure.
func coverageLabel(cov float64, rows int) string {
	if rows == 0 {
		return "coverage n/a"
	}
	s := "coverage " + pct(cov)
	if cov < coverageFloor {
		s += " low-coverage"
	}
	return s
}

func qualityLabel(avg float64, n int) string {
	if n == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.2f (n=%d)", avg, n)
}

func windowLabel(o Options) string {
	switch {
	case o.Since == "" && o.Until == "":
		return "all history"
	case o.Since == "":
		return "up to " + o.Until
	case o.Until == "":
		return "from " + o.Since
	default:
		return o.Since + " .. " + o.Until
	}
}

func projectLabel(o Options) string {
	if o.ProjectID == 0 {
		return "all"
	}
	return "id " + i64toa(o.ProjectID)
}

func more(b *bytes.Buffer, total, shown int, unit string) {
	if total > shown {
		fmt.Fprintf(b, "  … %d more %s (full list in --json)\n", total-shown, unit)
	}
}

func head[T any](rows []T, n int) []T {
	if len(rows) <= n {
		return rows
	}
	return rows[:n]
}

func usd(v float64) string  { return fmt.Sprintf("%.4f", v) }
func pct(v float64) string  { return fmt.Sprintf("%.1f%%", v*100) }
func itoa(v int) string     { return fmt.Sprintf("%d", v) }
func i64toa(v int64) string { return fmt.Sprintf("%d", v) }
func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func clip(s string, width int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\t", " ")
	if utf8.RuneCountInString(s) <= width {
		return s
	}
	return string([]rune(s)[:width-1]) + "…"
}

// column and table are a minimal fixed-width table writer. text/tabwriter
// cannot right-align one column and left-align another in the same table, and
// counts bytes rather than runes — both matter here, because task titles are
// not ASCII-only and numeric columns must line up on their decimal point.
type column struct {
	header string
	right  bool
}

type table struct {
	cols []column
	rows [][]string
}

func (t *table) add(cells ...string) { t.rows = append(t.rows, cells) }

func (t *table) render(b *bytes.Buffer, indent string) {
	widths := make([]int, len(t.cols))
	for i, c := range t.cols {
		widths[i] = utf8.RuneCountInString(c.header)
	}
	for _, r := range t.rows {
		for i, cell := range r {
			if i < len(widths) {
				if n := utf8.RuneCountInString(cell); n > widths[i] {
					widths[i] = n
				}
			}
		}
	}
	writeRow := func(cells []string) {
		var line strings.Builder
		line.WriteString(indent)
		for i, cell := range cells {
			if i >= len(t.cols) {
				break
			}
			if i > 0 {
				line.WriteString("  ")
			}
			if i == len(cells)-1 && !t.cols[i].right {
				line.WriteString(cell)
				continue
			}
			line.WriteString(padCell(cell, widths[i], t.cols[i].right))
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		b.WriteString("\n")
	}
	headers := make([]string, len(t.cols))
	for i, c := range t.cols {
		headers[i] = c.header
	}
	writeRow(headers)
	for _, r := range t.rows {
		writeRow(r)
	}
}

func padCell(s string, width int, right bool) string {
	n := width - utf8.RuneCountInString(s)
	if n < 0 {
		n = 0
	}
	sp := strings.Repeat(" ", n)
	if right {
		return sp + s
	}
	return s + sp
}
