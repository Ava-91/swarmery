package api

import (
	"database/sql"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
)

// retroReportServer wraps seedRetroReportDB in a full NewServer.
func retroReportServer(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()
	db := seedRetroReportDB(t)
	h, err := NewServer(db, false)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, db
}

// seedRetroReportDB seeds one project with something in EVERY section the
// report joins: a run with an error (agents + friction error groups), a denied
// tool, a resolved and a pending approval, a task with a retro (lessons +
// estimation) and a ledger row, and one advisor recommendation carrying
// evidence session ids. Shared with the analysis tests, which need the same
// evidence to have anything citable.
func seedRetroReportDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "report.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	today := retroDay(t, 0)
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("exec: %v\n%s", err, q)
		}
	}

	mustExec(`INSERT INTO projects (id, path, slug, name, first_seen) VALUES
		(1, '/work/alpha', '-work-alpha', 'Alpha', ?)`, today)
	mustExec(`INSERT INTO sessions (id, project_id, session_uuid, status, started_at, outcome) VALUES
		(1, 1, 'sess-alpha', 'completed', ?, 'fail')`, today)
	mustExec(`INSERT INTO turns (id, session_id, seq, role, started_at, tokens_in, tokens_out, cost_usd, agent_name) VALUES
		(1, 1, 0, 'assistant', ?, 10, 40, 0.75, NULL),
		(2, 1, 1, 'assistant', ?, 10, 90, 2.25, 'tech-lead')`, today, today)
	mustExec(`INSERT INTO events (session_id, ts, type, status, payload, duration_ms, dedup_key) VALUES
		(1, ?, 'subagent_start', 'ok', '{"subagent_type":"tech-lead"}', 4000, 'r-a1')`, today)
	mustExec(`INSERT INTO events (session_id, parent_event_id, ts, type, tool_name, status, payload, dedup_key) VALUES
		(1, 1, ?, 'tool_call', 'Bash', 'error',  '{"result":"boom"}', 'r-e1'),
		(1, NULL, ?, 'tool_call', 'Write', 'denied', '{}',            'r-d1'),
		(1, NULL, ?, 'tool_call', 'Write', 'ok',     '{}',            'r-d2')`, today, today, today)
	mustExec(`INSERT INTO permission_requests (session_id, tool_name, status, request_json, requested_at, resolved_at) VALUES
		(1, 'Write', 'allowed', '{}', ?, ?),
		(1, 'Bash',  'pending', '{}', ?, NULL)`, today, today, today)
	mustExec(`INSERT INTO tasks (id, project_id, title, prompt, status, created_at, started_at, source, external_id) VALUES
		(1, 1, 'Ship the loop', 'goal', 'done', ?, ?, 'workspace', 'task-loop')`, today, today)
	mustExec(`INSERT INTO task_retros (id, task_id, estimated_hours, actual_hours, variance_pct, ingested_at) VALUES
		(1, 1, 6, 9, 50, ?)`, today)
	mustExec(`INSERT INTO retro_lessons (retro_id, seq, title, body, action) VALUES
		(1, 1, 'Stage explicit paths', 'a shared checkout bites', 'never git add -A')`)
	mustExec(`INSERT INTO task_delegations (task_id, seq, agent, verdict) VALUES
		(1, 1, 'tech-lead', 'RE-DISPATCH')`)
	mustExec(`INSERT INTO recommendations
		(id, rule, target_kind, target, title, detail, evidence, status, dedup_key, created_at, updated_at) VALUES
		(1, 'R2', 'agent', 'tech-lead', 'High behaviour-failure rate',
		 '100% of runs failed in the window',
		 '{"window":14,"session_ids":["sess-alpha"]}', 'proposed', 'R2:tech-lead', ?, ?)`, today, today)

	return db
}

func TestRetroReportCarriesEverySection(t *testing.T) {
	srv, _ := retroReportServer(t)
	var out retroReportResponse
	getJSON(t, srv.URL+"/api/retro/report?"+retroRange(7), &out)

	if out.Report.Partial {
		t.Fatalf("partial = true on a healthy database: %v", out.Report.PartialSections)
	}
	if len(out.Report.Agents.Agents) != 1 || out.Report.Agents.Agents[0].Agent != "tech-lead" {
		t.Errorf("agents section = %+v, want one tech-lead row", out.Report.Agents.Agents)
	}
	if out.Report.Agents.Main.CostUSD != 0.75 {
		t.Errorf("main fold = %+v, want cost 0.75", out.Report.Agents.Main)
	}
	if len(out.Report.Friction.DeniedTools) != 1 || out.Report.Friction.DeniedTools[0].Tool != "Write" {
		t.Errorf("denied tools = %+v, want one Write row", out.Report.Friction.DeniedTools)
	}
	if len(out.Report.Friction.ErrorGroups) == 0 {
		t.Error("error groups section is empty despite a seeded error")
	}
	if out.Report.Friction.Approvals.Resolved != 1 || out.Report.Friction.Approvals.Pending != 1 {
		t.Errorf("approvals = %+v, want 1 resolved / 1 pending", out.Report.Friction.Approvals)
	}
	if len(out.Report.Lessons.Lessons) != 1 {
		t.Errorf("lessons = %+v, want 1", out.Report.Lessons.Lessons)
	}
	if len(out.Report.Tasks.Tasks) != 1 {
		t.Errorf("tasks = %+v, want 1", out.Report.Tasks.Tasks)
	}
	if len(out.Report.Recommendations.Recommendations) != 1 {
		t.Errorf("recommendations = %+v, want 1", out.Report.Recommendations.Recommendations)
	}
	if out.Report.From == "" || out.Report.To == "" {
		t.Errorf("report window is unset: %+v", out.Report)
	}
}

func TestRetroReportDigestQuotesTheSeededEvidence(t *testing.T) {
	srv, _ := retroReportServer(t)
	var out retroReportResponse
	getJSON(t, srv.URL+"/api/retro/report?"+retroRange(7), &out)

	if out.DigestTruncated {
		t.Errorf("a one-project fixture should not fill the 30KB digest budget")
	}
	if out.DigestSHA256 == "" {
		t.Error("digest sha is empty")
	}
	for _, want := range []string{
		"[E:agent:tech-lead]",
		"[E:rec:1]",
		"[E:session:sess-alpha]",
		"[E:task:task-loop]",
		"[E:lesson:task-loop#1]",
		"Stage explicit paths",
		"High behaviour-failure rate",
	} {
		if !strings.Contains(out.Digest, want) {
			t.Errorf("digest is missing %q:\n%s", want, out.Digest)
		}
	}
	// The recommendation's evidence session must reach the digest as a
	// citation — that plumbing (evidence JSON → Sessions) is the whole reason
	// the improver can point at a transcript.
	if !strings.Contains(out.Digest, "evidence sessions: [E:session:sess-alpha]") {
		t.Errorf("advisor evidence sessions did not reach the digest:\n%s", out.Digest)
	}
}

// A failing section must degrade to empty+named, not to a 500: the rest of the
// report is still usable evidence.
func TestRetroReportSectionFailureIsPartialNot500(t *testing.T) {
	srv, db := retroReportServer(t)
	if _, err := db.Exec(`DROP TABLE retro_lessons`); err != nil {
		t.Fatalf("drop retro_lessons: %v", err)
	}
	var out retroReportResponse
	getJSON(t, srv.URL+"/api/retro/report?"+retroRange(7), &out)

	if !out.Report.Partial {
		t.Fatal("partial = false after a section's table vanished")
	}
	if len(out.Report.PartialSections) != 1 || out.Report.PartialSections[0] != "lessons" {
		t.Errorf("partialSections = %v, want [lessons]", out.Report.PartialSections)
	}
	if len(out.Report.Lessons.Lessons) != 0 {
		t.Errorf("failed section is not empty: %+v", out.Report.Lessons.Lessons)
	}
	if len(out.Report.Agents.Agents) != 1 {
		t.Errorf("a healthy section was lost with the failing one: %+v", out.Report.Agents)
	}
	// The digest must say the section failed rather than let a reader take
	// "no lessons" at face value.
	if !strings.Contains(out.Digest, "failed to load and are EMPTY here, not zero: lessons") {
		t.Errorf("digest does not disclose the failed section:\n%s", out.Digest)
	}
}

func TestRetroReportRejectsABadWindow(t *testing.T) {
	srv, _ := retroReportServer(t)
	resp, err := srv.Client().Get(srv.URL + "/api/retro/report?from=nonsense")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// defaultRecStatuses must stay the parsed form of the filter string the
// handler defaults to — the report and the rail showing different statuses
// would make the digest quietly disagree with the page.
func TestDefaultRecStatusesMatchesTheFilterConst(t *testing.T) {
	got := strings.Join(defaultRecStatuses(), ",")
	if got != defaultRecStatusFilter {
		t.Errorf("defaultRecStatuses() = %q, want %q", got, defaultRecStatusFilter)
	}
}
