package modeleval

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "modeleval.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// seed writes one judged trajectory: a session, a turn attributing the agent
// to a SUBJECT model, and the judgment itself (whose own `model` column is the
// JUDGE — deliberately different here, because conflating the two is the bug
// this package exists to avoid).
func seed(t *testing.T, db *sql.DB, sessionID int64, agent, subjectModel string, overall float64) {
	t.Helper()
	// Plain INSERT with a guard, not INSERT OR IGNORE: OR IGNORE swallows
	// constraint violations, so a missing NOT NULL column here would surface
	// three statements later as an unrelated foreign-key failure.
	var have int
	if err := db.QueryRow(`SELECT COUNT(*) FROM projects WHERE id = 1`).Scan(&have); err != nil {
		t.Fatal(err)
	}
	if have == 0 {
		if _, err := db.Exec(
			`INSERT INTO projects (id, path, slug, name, first_seen)
			 VALUES (1, '/p', 'p', 'p', '2026-09-01T00:00:00Z')`); err != nil {
			t.Fatalf("seed project: %v", err)
		}
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = ?`, sessionID).Scan(&have); err != nil {
		t.Fatal(err)
	}
	if have == 0 {
		if _, err := db.Exec(
			`INSERT INTO sessions (id, project_id, session_uuid, started_at)
			 VALUES (?, 1, ?, '2026-09-01T00:00:00Z')`,
			sessionID, fmt.Sprintf("sess-%d", sessionID)); err != nil {
			t.Fatalf("seed session: %v", err)
		}
	}
	if _, err := db.Exec(
		`INSERT INTO turns (session_id, seq, role, started_at, model, agent_name)
		 VALUES (?, 1, 'assistant', '2026-09-01T00:00:00Z', ?, ?)`,
		sessionID, subjectModel, agent); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO trajectory_judgments
		   (session_id, agent, model, judged_at, end_result, instruction_compliance,
		    pitfalls, tool_calls, overall, review)
		 VALUES (?, ?, 'claude-haiku-4-5', '2026-09-01T00:00:00Z', 4,4,4,4, ?, 'r')`,
		sessionID, agent, overall); err != nil {
		t.Fatal(err)
	}
}

// seedMain seeds an orchestrator ('main') judgment: its turns carry
// agent_name IS NULL, which is the grain the live corpus actually has.
func seedMain(t *testing.T, db *sql.DB, sessionID int64, subjectModel string, overall float64) {
	t.Helper()
	seed(t, db, sessionID, "main", subjectModel, overall)
	if _, err := db.Exec(
		`INSERT INTO turns (session_id, seq, role, started_at, model, agent_name)
		 VALUES (?, 2, 'assistant', '2026-09-01T00:00:00Z', ?, NULL)`,
		sessionID, subjectModel); err != nil {
		t.Fatal(err)
	}
}

func mkSet(t *testing.T, agents ...string) *GoldenSet {
	t.Helper()
	gs := &GoldenSet{Version: "test-1"}
	for i, a := range agents {
		gs.Cases = append(gs.Cases, Case{ID: "c", Agent: a, Rubric: "r", Weight: 1})
		_ = i
	}
	return gs
}

// A model nobody has run has no evidence either way. Saying "fail" there would
// be a lie about a model that was never tried.
func TestEvaluateUnrunModelIsInconclusive(t *testing.T) {
	db := openDB(t)
	gs := mkSet(t, "a", "b", "c", "d", "e", "f")

	res, err := Evaluate(db, gs, "claude-opus-6")
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != "inconclusive" {
		t.Errorf("verdict = %q, want inconclusive for a model with no trajectories", res.Verdict)
	}
	if res.Trajectories != 0 {
		t.Errorf("trajectories = %d, want 0", res.Trajectories)
	}
}

// Too little evidence is still inconclusive, never a pass: one good run is not
// a verdict about a model.
func TestEvaluateBelowMinTrajectoriesIsInconclusive(t *testing.T) {
	db := openDB(t)
	gs := mkSet(t, "a", "b", "c", "d", "e", "f")
	for i, a := range []string{"a", "b"} { // 2 < MinTrajectories
		seed(t, db, int64(100+i), a, "claude-opus-6", 5.0)
	}

	res, err := Evaluate(db, gs, "claude-opus-6")
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != "inconclusive" {
		t.Errorf("verdict = %q with %d trajectories, want inconclusive (MinTrajectories=%d)",
			res.Verdict, res.Trajectories, MinTrajectories)
	}
}

// The gate is relative, so a candidate is judged against an incumbent — never
// against an absolute bar. Measured on the live corpus, every real model sits
// near 3.0, so an absolute bar high enough to feel like "good" fails the model
// currently in use. These cases pin the relative behaviour instead.
func TestEvaluateIsRelativeToTheIncumbent(t *testing.T) {
	const incumbent, candidate = "claude-opus-5", "claude-opus-6"

	newDBWithIncumbent := func(t *testing.T, at float64) *sql.DB {
		db := openDB(t)
		for i := 0; i < 6; i++ {
			seedMain(t, db, int64(700+i), incumbent, at)
		}
		return db
	}

	t.Run("candidate matching the incumbent passes", func(t *testing.T) {
		db := newDBWithIncumbent(t, 3.0)
		for i := 0; i < 6; i++ {
			seedMain(t, db, int64(800+i), candidate, 3.0)
		}
		res, err := Evaluate(db, mkSet(t, "core:tech-lead"), candidate)
		if err != nil {
			t.Fatal(err)
		}
		if res.Verdict != "pass" {
			t.Errorf("res = %+v, want pass: 3.0 is not a regression from 3.0, and an "+
				"absolute bar here is what blocked every real model", res)
		}
	})

	t.Run("candidate clearly worse fails", func(t *testing.T) {
		db := newDBWithIncumbent(t, 3.4)
		for i := 0; i < 6; i++ {
			seedMain(t, db, int64(900+i), candidate, 1.6) // the sonnet-5 shape
		}
		res, err := Evaluate(db, mkSet(t, "core:tech-lead"), candidate)
		if err != nil {
			t.Fatal(err)
		}
		if res.Verdict != "fail" {
			t.Errorf("res = %+v, want fail: 1.8 below baseline is far past the %.2f margin",
				res, RegressionMargin)
		}
	})

	t.Run("a small dip stays within the margin", func(t *testing.T) {
		db := newDBWithIncumbent(t, 3.2)
		for i := 0; i < 6; i++ {
			seedMain(t, db, int64(1000+i), candidate, 3.0) // 0.2 < margin
		}
		res, err := Evaluate(db, mkSet(t, "core:tech-lead"), candidate)
		if err != nil {
			t.Fatal(err)
		}
		if res.Verdict != "pass" {
			t.Errorf("res = %+v, want pass: judge noise must not read as a regression", res)
		}
	})

	t.Run("no incumbent means inconclusive, not an invented pass", func(t *testing.T) {
		db := openDB(t)
		for i := 0; i < 6; i++ {
			seedMain(t, db, int64(1100+i), candidate, 4.5)
		}
		res, err := Evaluate(db, mkSet(t, "core:tech-lead"), candidate)
		if err != nil {
			t.Fatal(err)
		}
		if res.Verdict != "inconclusive" {
			t.Errorf("res = %+v, want inconclusive: there is nothing to regress from yet", res)
		}
	})
}

// The judge model must never be mistaken for the subject model. Every seeded
// judgment above is judged by haiku; asking about haiku must find nothing.
func TestEvaluateIgnoresJudgeModel(t *testing.T) {
	db := openDB(t)
	agents := []string{"a", "b", "c", "d", "e", "f"}
	for i, a := range agents {
		seed(t, db, int64(400+i), a, "claude-opus-6", 5.0)
	}

	res, err := Evaluate(db, mkSet(t, agents...), "claude-haiku-4-5")
	if err != nil {
		t.Fatal(err)
	}
	if res.Trajectories != 0 {
		t.Errorf("scored %d trajectories for the JUDGE model — trajectory_judgments.model "+
			"is provenance, not the model under test", res.Trajectories)
	}
}

// The orchestrator grain is what the pipeline actually produces today: a
// judgment for 'main' must match the session's orchestrator turns, which carry
// agent_name IS NULL. Getting this join wrong makes every verdict inconclusive
// forever — a gate that can never open, which is worse than no gate.
func TestEvaluateMatchesMainGrain(t *testing.T) {
	db := openDB(t)
	for i := 0; i < 6; i++ { // an incumbent, so a verdict is reachable
		seedMain(t, db, int64(560+i), "claude-opus-5", 3.1)
	}
	for i := 0; i < 6; i++ {
		seedMain(t, db, int64(600+i), "claude-opus-6", 4.6)
	}
	res, err := Evaluate(db, mkSet(t, "core:tech-lead", "core:debugger"), "claude-opus-6")
	if err != nil {
		t.Fatal(err)
	}
	if res.Trajectories != 6 {
		t.Fatalf("trajectories = %d, want 6 — 'main' judgments must join to "+
			"orchestrator turns (agent_name IS NULL), and only the candidate's", res.Trajectories)
	}
	if res.Verdict != "pass" {
		t.Errorf("verdict = %q at mean 4.6 vs a 3.1 incumbent, want pass", res.Verdict)
	}
	// Coverage is reported honestly: main-grain evidence covers no named agent.
	if res.AgentsCovered != 0 {
		t.Errorf("agentsCovered = %d, want 0 — orchestrator evidence is not "+
			"evidence about a named agent, and a pass must say so", res.AgentsCovered)
	}
	if !contains(res.Detail, "0/2") {
		t.Errorf("detail %q should surface the thin coverage", res.Detail)
	}
}

// Re-running must converge, not accumulate — otherwise "the newest verdict"
// becomes ambiguous, which is the whole reason for the UNIQUE.
func TestPersistUpsertsInPlace(t *testing.T) {
	db := openDB(t)
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

	first := Result{Model: "m", GoldenSetVersion: "v1", Verdict: "fail", Score: 2, Trajectories: 6}
	if err := Persist(db, first, now); err != nil {
		t.Fatal(err)
	}
	second := first
	second.Verdict, second.Score = "pass", 4.6
	if err := Persist(db, second, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM model_validations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rows = %d, want 1 — UNIQUE(model, golden_set_version) must upsert", n)
	}

	got, ok, err := Newest(db, "m")
	if err != nil || !ok {
		t.Fatalf("Newest: ok=%v err=%v", ok, err)
	}
	if got.Verdict != "pass" {
		t.Errorf("verdict = %q, want the updated pass", got.Verdict)
	}
}

func TestNewestMissingModel(t *testing.T) {
	db := openDB(t)
	if _, ok, err := Newest(db, "never-seen"); err != nil || ok {
		t.Errorf("Newest = ok:%v err:%v, want ok=false — unknown must not read as fine", ok, err)
	}
}

// The shipped manifest is a contract too: no transcript text, every agent
// carries a rubric, and the version is present.
func TestShippedManifest(t *testing.T) {
	gs, err := LoadGoldenSet(filepath.Join("..", "..", "testdata", "goldenset", "manifest.json"))
	if err != nil {
		t.Fatalf("shipped manifest: %v", err)
	}
	if len(gs.Cases) < 20 {
		t.Errorf("cases = %d, want >= 20", len(gs.Cases))
	}

	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "goldenset", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatal(err)
	}
	// Selection only: a case must never carry transcript-shaped fields.
	for _, c := range probe["cases"].([]any) {
		for k := range c.(map[string]any) {
			switch k {
			case "id", "agent", "rubric", "weight":
			default:
				t.Errorf("case carries unexpected field %q — the manifest selects and "+
					"grades; transcript content belongs in SQLite, not the repo", k)
			}
		}
	}

	// Known-bad cases are what stop the set becoming a rubber stamp.
	var knownBad int
	for _, c := range gs.Cases {
		if len(c.Rubric) > 0 && (contains(c.Rubric, "KNOWN-BAD")) {
			knownBad++
		}
	}
	if knownBad < 3 {
		t.Errorf("known-bad cases = %d, want >= 3: a set that only contains healthy "+
			"runs cannot tell 'handled a failure well' from 'never met one'", knownBad)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func TestLoadGoldenSetRejectsBadManifests(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"no version":  `{"cases":[{"id":"a","agent":"x","rubric":"r","weight":1}]}`,
		"no cases":    `{"golden_set_version":"v"}`,
		"no rubric":   `{"golden_set_version":"v","cases":[{"id":"a","agent":"x","weight":1}]}`,
		"zero weight": `{"golden_set_version":"v","cases":[{"id":"a","agent":"x","rubric":"r","weight":0}]}`,
	} {
		p := filepath.Join(dir, "m.json")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadGoldenSet(p); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
