// Package retrodigest renders a retro report as a deterministic markdown
// digest — the evidence text the system-improver agent reasons over, and the
// text an accepted analysis carries into Planning Mode as its idea.
//
// The package is deliberately storage-free: it takes plain structs (filled in
// by internal/api from the same DTOs /retro already serves) and returns
// markdown. That keeps the formatting under cheap unit tests instead of
// behind a seeded database.
//
// Two invariants the callers depend on:
//
//   - Determinism. The same Report renders byte-identically every time: every
//     collection is sorted with a total order before rendering, no map is
//     iterated without sorting its keys, and no clock is read — the window is
//     a field, not time.Now().
//   - Citations. Every evidence line ends in at least one marker of the form
//     [E:<kind>:<id>], kind ∈ {agent, rec, error_group, session, task,
//     lesson}. The improver agent may only cite ids it finds here, and
//     internal/retroanalysis validates its output against that vocabulary.
package retrodigest

import (
	"fmt"
	"sort"
	"strings"
)

// Kind is the citation-marker vocabulary. An id that is not one of these is
// not citable, and an analysis quoting one is rejected downstream.
const (
	KindAgent      = "agent"
	KindRec        = "rec"
	KindErrorGroup = "error_group"
	KindSession    = "session"
	KindTask       = "task"
	KindLesson     = "lesson"
)

// Report is the storage-free shape of one /retro window. Field names mirror
// the DTOs in internal/api/retro.go; pointer fields stay pointers so "no data"
// renders as "n/a" rather than a misleading zero.
type Report struct {
	From   string // YYYY-MM-DD, inclusive
	To     string // YYYY-MM-DD, inclusive
	Scope  string // project slug; "" = whole fleet
	Approx bool   // the window overlaps rolled-up (pruned) days

	Main            Main
	Agents          []Agent
	Friction        Friction
	Lessons         []Lesson
	Tasks           []Task
	Recommendations []Recommendation

	// Partial names the sections whose query failed upstream. They render as
	// an explicit warning so the reader never mistakes a failed section for
	// an empty one.
	Partial []string
}

// Main is the orchestrator's own totals (it has no subagent_start of its own,
// so it has no run count).
type Main struct {
	CostUSD   float64
	TokensOut int64
	Errors    int64
}

// Agent is one scorecard row. ErrorRate is the behaviour-failed-run share, the
// same grain the advisor's R2 fires on.
type Agent struct {
	Name           string
	Runs           int64
	Sessions       int64
	CostUSD        float64
	TokensOut      int64
	Errors         int64
	ErrorRate      float64
	P95Ms          *int64
	SuccessRate    *float64
	ReDispatchRate *float64
	PrevErrorRate  float64
	PrevRuns       int64
	Improvable     bool
}

// impact ranks agents for the digest: the estimated number of behaviour-failed
// runs (rate × runs) is what an improvement would actually buy back, so it
// beats a raw run count at putting the worst offender first.
func (a Agent) impact() float64 { return a.ErrorRate * float64(a.Runs) }

// Friction is the friction board: denied tools, top error groups, approval waits.
type Friction struct {
	DeniedTools []DeniedTool
	ErrorGroups []ErrorGroup
	Approvals   Approvals
}

// DeniedTool is one tool with at least one denial in the window. HasRule says
// whether an enabled approval rule already covers it.
type DeniedTool struct {
	Tool    string
	Calls   int64
	Denied  int64
	HasRule bool
}

// ErrorGroup is one folded error signature plus the sessions it fired in.
type ErrorGroup struct {
	Key      string
	Example  string
	Count    int64
	LastTs   string
	Sessions []string
}

// Approvals is the permission-request wait summary. Pending is "pending now"
// and deliberately not window-filtered.
type Approvals struct {
	Resolved      int64
	Pending       int64
	AvgResolveSec *float64
	WaitTotalMin  float64
}

// Lesson is one parsed lessons-learned entry. TaskExternalID+Seq is its
// stable identity, and its citation id.
type Lesson struct {
	TaskExternalID string
	TaskTitle      string
	Date           string
	Seq            int64
	Title          string
	Action         string
	Body           string
}

// Task is one workspace task's estimation accuracy and orchestration churn.
type Task struct {
	ExternalID     string
	Title          string
	EstimatedHours *float64
	ActualHours    *float64
	VariancePct    *float64
	Loops          int64
	Delegations    int64
	VerdictOK      int64
	VerdictRedisp  int64
}

// Recommendation is one advisor row. DedupKey is rule:target — the identity
// migration 0019 dedupes on, and the digest's secondary sort key.
type Recommendation struct {
	ID         int64
	Rule       string
	TargetKind string
	Target     string
	DedupKey   string
	Title      string
	Detail     string
	Status     string
	// Sessions are the evidence session uuids, already extracted from the
	// advisor's evidence JSON by the caller.
	Sessions []string
}

// section is one rendered block plus the priority that decides who gets
// dropped first when the digest overflows.
type section struct {
	name string
	body string
	// prio: lower is dropped first.
	prio int
}

// Section priorities. Recommendations are the advisor's already-reasoned
// conclusions and agents are the raw health signal, so they survive longest;
// the estimation table is the first thing an improver can do without.
const (
	prioTasks    = 1
	prioLessons  = 2
	prioFriction = 3
	prioAgents   = 4
	prioRecs     = 5
)

// truncMarker is appended verbatim when sections were dropped; %d is the
// number omitted. Callers grep for the "digest truncated" prefix.
const truncMarker = "\n_(digest truncated: %d sections omitted)_\n"

// hardCutMarker terminates a digest whose header alone overflows the limit —
// the degenerate case a caller should never hit, kept honest rather than
// silently over-budget.
const hardCutMarker = "\n_(digest truncated)_\n"

// Build renders the report as markdown no longer than limit BYTES, and reports
// whether anything was dropped.
//
// Bytes, not runes, because the consumers measure bytes: len(idea) against
// maxPlanningIdeaLen in internal/api/planning.go, and the model's context
// budget. A rune-based cap would silently overshoot on Cyrillic prose.
//
// Sections are dropped whole, least-important first, until the result fits.
// The header (window, scope, partial-section warning) is never dropped: it is
// what tells the reader which slice of reality they are looking at.
func Build(r Report, limit int) (string, bool) {
	header := buildHeader(r)
	sections := []section{
		{name: "recommendations", body: buildRecommendations(r.Recommendations), prio: prioRecs},
		{name: "agents", body: buildAgents(r.Main, r.Agents), prio: prioAgents},
		{name: "friction", body: buildFriction(r.Friction), prio: prioFriction},
		{name: "lessons", body: buildLessons(r.Lessons), prio: prioLessons},
		{name: "tasks", body: buildTasks(r.Tasks), prio: prioTasks},
	}
	// Render order is fixed and independent of drop order.
	order := append([]section(nil), sections...)

	kept := map[string]bool{}
	for _, s := range sections {
		kept[s.name] = true
	}
	// Drop order: least important first, name as the tie-break so the order is
	// total even if two sections ever share a priority.
	dropOrder := append([]section(nil), sections...)
	sort.Slice(dropOrder, func(i, j int) bool {
		if dropOrder[i].prio != dropOrder[j].prio {
			return dropOrder[i].prio < dropOrder[j].prio
		}
		return dropOrder[i].name < dropOrder[j].name
	})

	assemble := func(kept map[string]bool, omitted int) string {
		var b strings.Builder
		b.WriteString(header)
		for _, s := range order {
			if kept[s.name] {
				b.WriteString(s.body)
			}
		}
		if omitted > 0 {
			b.WriteString(fmt.Sprintf(truncMarker, omitted))
		}
		return b.String()
	}

	out := assemble(kept, 0)
	if len(out) <= limit {
		return out, false
	}
	for i := 0; i < len(dropOrder); i++ {
		delete(kept, dropOrder[i].name)
		out = assemble(kept, i+1)
		if len(out) <= limit {
			return out, true
		}
	}
	// Every section is gone and the header still overflows: cut hard rather
	// than hand a caller a payload its own limit check will reject.
	return hardCut(out, limit), true
}

// hardCut trims s to at most limit bytes without splitting a UTF-8 rune,
// leaving room for the marker when the limit allows one at all.
func hardCut(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(s) <= limit {
		return s
	}
	body := limit
	if limit > len(hardCutMarker) {
		body = limit - len(hardCutMarker)
	}
	cut := s[:body]
	// Back off to a rune boundary: continuation bytes are 0b10xxxxxx.
	for len(cut) > 0 && cut[len(cut)-1]&0xC0 == 0x80 {
		cut = cut[:len(cut)-1]
	}
	if len(cut) > 0 && cut[len(cut)-1]&0x80 != 0 {
		cut = cut[:len(cut)-1] // a lead byte whose continuations were cut off
	}
	if body == limit {
		return cut
	}
	return cut + hardCutMarker
}

// cite renders one citation marker.
func cite(kind, id string) string { return "[E:" + kind + ":" + id + "]" }

func buildHeader(r Report) string {
	scope := "whole fleet"
	if r.Scope != "" {
		scope = "project " + r.Scope
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Retro digest %s → %s (%s)\n\n", r.From, r.To, scope)
	b.WriteString("Every evidence line ends in one or more `[E:kind:id]` citation markers. " +
		"Cite only ids that appear below — inventing one invalidates the analysis.\n")
	if r.Approx {
		b.WriteString("\n> The window overlaps rolled-up days: per-event detail there was pruned, so counts are approximate.\n")
	}
	if len(r.Partial) > 0 {
		p := append([]string(nil), r.Partial...)
		sort.Strings(p)
		fmt.Fprintf(&b, "\n> Sections that failed to load and are EMPTY here, not zero: %s.\n", strings.Join(p, ", "))
	}
	return b.String()
}

func buildAgents(main Main, agents []Agent) string {
	var b strings.Builder
	b.WriteString("\n## Agents\n\n")
	fmt.Fprintf(&b, "Orchestrator (main): $%.2f, %d tokens out, %d errors.\n\n",
		main.CostUSD, main.TokensOut, main.Errors)
	if len(agents) == 0 {
		b.WriteString("No agent ran in this window.\n")
		return b.String()
	}
	rows := append([]Agent(nil), agents...)
	sort.Slice(rows, func(i, j int) bool {
		if ii, jj := rows[i].impact(), rows[j].impact(); ii != jj {
			return ii > jj
		}
		if rows[i].Runs != rows[j].Runs {
			return rows[i].Runs > rows[j].Runs
		}
		return rows[i].Name < rows[j].Name
	})
	for _, a := range rows {
		fmt.Fprintf(&b, "- `%s` — %d runs in %d sessions, error rate %s (prev %s over %d runs), %d errors, $%.2f",
			a.Name, a.Runs, a.Sessions, pct(a.ErrorRate), pct(a.PrevErrorRate), a.PrevRuns, a.Errors, a.CostUSD)
		if a.SuccessRate != nil {
			fmt.Fprintf(&b, ", success %s", pct(*a.SuccessRate))
		}
		if a.ReDispatchRate != nil {
			fmt.Fprintf(&b, ", re-dispatch %s", pct(*a.ReDispatchRate))
		}
		if a.P95Ms != nil {
			fmt.Fprintf(&b, ", p95 %ds", *a.P95Ms/1000)
		}
		if !a.Improvable {
			b.WriteString(", no editable definition file")
		}
		fmt.Fprintf(&b, " %s\n", cite(KindAgent, a.Name))
	}
	return b.String()
}

func buildRecommendations(recs []Recommendation) string {
	var b strings.Builder
	b.WriteString("\n## Advisor recommendations\n\n")
	if len(recs) == 0 {
		b.WriteString("The rule engine produced no open recommendation for this window.\n")
		return b.String()
	}
	rows := append([]Recommendation(nil), recs...)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Rule != rows[j].Rule {
			return rows[i].Rule < rows[j].Rule
		}
		if rows[i].DedupKey != rows[j].DedupKey {
			return rows[i].DedupKey < rows[j].DedupKey
		}
		return rows[i].ID < rows[j].ID
	})
	for _, rc := range rows {
		fmt.Fprintf(&b, "- **%s** [%s] `%s` (%s) — %s %s",
			rc.Rule, rc.Status, rc.Target, rc.TargetKind, rc.Title, cite(KindRec, itoa(rc.ID)))
		if rc.Detail != "" {
			fmt.Fprintf(&b, "\n  - rationale: %s %s", oneLine(rc.Detail), cite(KindRec, itoa(rc.ID)))
		}
		if s := citeSessions(rc.Sessions); s != "" {
			fmt.Fprintf(&b, "\n  - evidence sessions: %s", s)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func buildFriction(f Friction) string {
	var b strings.Builder
	b.WriteString("\n## Friction\n\n")

	b.WriteString("### Denied tools\n\n")
	if len(f.DeniedTools) == 0 {
		b.WriteString("No tool call was denied in this window.\n")
	} else {
		rows := append([]DeniedTool(nil), f.DeniedTools...)
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Denied != rows[j].Denied {
				return rows[i].Denied > rows[j].Denied
			}
			return rows[i].Tool < rows[j].Tool
		})
		for _, d := range rows {
			rule := "no approval rule covers it"
			if d.HasRule {
				rule = "already covered by an approval rule"
			}
			fmt.Fprintf(&b, "- `%s` — %d of %d calls denied, %s\n", d.Tool, d.Denied, d.Calls, rule)
		}
	}

	b.WriteString("\n### Error groups\n\n")
	if len(f.ErrorGroups) == 0 {
		b.WriteString("No error fired in this window.\n")
	} else {
		rows := append([]ErrorGroup(nil), f.ErrorGroups...)
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Count != rows[j].Count {
				return rows[i].Count > rows[j].Count
			}
			return rows[i].Key < rows[j].Key
		})
		for _, g := range rows {
			fmt.Fprintf(&b, "- `%s` ×%d, last %s — %s %s",
				g.Key, g.Count, g.LastTs, oneLine(g.Example), cite(KindErrorGroup, g.Key))
			if s := citeSessions(g.Sessions); s != "" {
				fmt.Fprintf(&b, "\n  - sessions: %s", s)
			}
			b.WriteString("\n")
		}
	}

	fmt.Fprintf(&b, "\n### Approvals\n\n- %d resolved, %d pending now, %.1f minutes of total wait",
		f.Approvals.Resolved, f.Approvals.Pending, f.Approvals.WaitTotalMin)
	if f.Approvals.AvgResolveSec != nil {
		fmt.Fprintf(&b, ", %.0fs average", *f.Approvals.AvgResolveSec)
	}
	b.WriteString("\n")
	return b.String()
}

func buildLessons(lessons []Lesson) string {
	var b strings.Builder
	b.WriteString("\n## Lessons learned\n\n")
	if len(lessons) == 0 {
		b.WriteString("No task in this window recorded a lesson.\n")
		return b.String()
	}
	rows := append([]Lesson(nil), lessons...)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Date != rows[j].Date {
			return rows[i].Date > rows[j].Date
		}
		if rows[i].TaskExternalID != rows[j].TaskExternalID {
			return rows[i].TaskExternalID < rows[j].TaskExternalID
		}
		return rows[i].Seq < rows[j].Seq
	})
	for _, l := range rows {
		id := l.TaskExternalID + "#" + itoa(l.Seq)
		fmt.Fprintf(&b, "- %s (%s) — %s", l.Date, l.TaskExternalID, oneLine(l.Title))
		if l.Action != "" {
			fmt.Fprintf(&b, " → %s", oneLine(l.Action))
		}
		fmt.Fprintf(&b, " %s\n", cite(KindLesson, id))
	}
	return b.String()
}

func buildTasks(tasks []Task) string {
	var b strings.Builder
	b.WriteString("\n## Estimation accuracy and churn\n\n")
	if len(tasks) == 0 {
		b.WriteString("No workspace task in this window carried a parsed artifact.\n")
		return b.String()
	}
	rows := append([]Task(nil), tasks...)
	sort.Slice(rows, func(i, j int) bool {
		// Worst overrun first; unestimated tasks sort last.
		iv, jv := absVariance(rows[i].VariancePct), absVariance(rows[j].VariancePct)
		if iv != jv {
			return iv > jv
		}
		return rows[i].ExternalID < rows[j].ExternalID
	})
	for _, t := range rows {
		fmt.Fprintf(&b, "- `%s` — %s", t.ExternalID, oneLine(t.Title))
		if t.EstimatedHours != nil && t.ActualHours != nil {
			fmt.Fprintf(&b, ", estimated %.1fh vs actual %.1fh", *t.EstimatedHours, *t.ActualHours)
		}
		if t.VariancePct != nil {
			fmt.Fprintf(&b, " (%+.0f%%)", *t.VariancePct)
		}
		fmt.Fprintf(&b, ", %d loops, %d delegations (%d ok / %d re-dispatched) %s\n",
			t.Loops, t.Delegations, t.VerdictOK, t.VerdictRedisp, cite(KindTask, t.ExternalID))
	}
	return b.String()
}

// citeSessions renders a sorted, deduped, capped list of session citations.
// The cap keeps one noisy error group from crowding out whole sections.
const maxSessionCites = 5

func citeSessions(uuids []string) string {
	if len(uuids) == 0 {
		return ""
	}
	seen := map[string]bool{}
	uniq := make([]string, 0, len(uuids))
	for _, u := range uuids {
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		uniq = append(uniq, u)
	}
	if len(uniq) == 0 {
		return ""
	}
	sort.Strings(uniq)
	more := 0
	if len(uniq) > maxSessionCites {
		more = len(uniq) - maxSessionCites
		uniq = uniq[:maxSessionCites]
	}
	parts := make([]string, 0, len(uniq))
	for _, u := range uniq {
		parts = append(parts, cite(KindSession, u))
	}
	out := strings.Join(parts, " ")
	if more > 0 {
		out += fmt.Sprintf(" (+%d more)", more)
	}
	return out
}

// oneLine flattens prose onto a single markdown line: newlines and runs of
// whitespace collapse, so a multi-paragraph detail can never break the list.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func pct(v float64) string { return fmt.Sprintf("%.0f%%", v*100) }

func itoa(v int64) string { return fmt.Sprintf("%d", v) }

// absVariance ranks tasks by overrun magnitude; a task with no variance
// recorded sorts last rather than as a perfect estimate.
func absVariance(v *float64) float64 {
	if v == nil {
		return -1
	}
	if *v < 0 {
		return -*v
	}
	return *v
}
