package plugindrift

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/findings"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
)

// tickerDB opens a migrated temp DB.
func tickerDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// project registers a project row whose settings.json enables the given ids,
// and returns its path.
func project(t *testing.T, db *sql.DB, slug string, enabled ...string) string {
	t.Helper()
	dir := t.TempDir()
	if len(enabled) > 0 {
		body := `{"enabledPlugins":{`
		for i, id := range enabled {
			if i > 0 {
				body += ","
			}
			body += `"` + id + `":true`
		}
		body += `}}`
		mustWrite(t, filepath.Join(dir, ".claude", "settings.json"), body)
	}
	if _, err := db.Exec(
		`INSERT INTO projects (path, slug, first_seen) VALUES (?, ?, '2026-07-28T00:00:00Z')`,
		dir, slug); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	return dir
}

// activeRows returns target→message for a rule's unresolved rows.
func activeRows(t *testing.T, db *sql.DB, rule string) map[string]string {
	t.Helper()
	rows, err := db.Query(
		`SELECT target, message FROM config_lint_findings WHERE rule = ? AND resolved_at IS NULL`, rule)
	if err != nil {
		t.Fatalf("query %s: %v", rule, err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var target, message string
		if err := rows.Scan(&target, &message); err != nil {
			t.Fatal(err)
		}
		out[target] = message
	}
	return out
}

func rowCount(t *testing.T, db *sql.DB, rule string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM config_lint_findings WHERE rule = ?`, rule).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// newErrorSpy records every OnNewError callback.
type newErrorSpy struct{ calls [][3]string }

func (s *newErrorSpy) fn(target, rule, message string) {
	s.calls = append(s.calls, [3]string{target, rule, message})
}

func TestTickerOnceInsertsMissingPlugin(t *testing.T) {
	db := tickerDB(t)
	path := project(t, db, "p1", "core@swarmery")
	spy := &newErrorSpy{}
	tk := &Ticker{
		DB:         db,
		Detector:   &Detector{ClaudeDir: t.TempDir(), Runner: stubRunner{out: listJSON(t)}},
		OnNewError: spy.fn,
	}

	tk.Once(context.Background())

	want := Target("core@swarmery", path)
	got := activeRows(t, db, RuleEnabledNotInstalled)
	if len(got) != 1 || got[want] == "" {
		t.Fatalf("want one active row for %q, got %v", want, got)
	}
	if len(spy.calls) != 1 || spy.calls[0][0] != want || spy.calls[0][1] != RuleEnabledNotInstalled {
		t.Fatalf("OnNewError calls = %v", spy.calls)
	}
}

// A second identical pass must refresh the row in place, not duplicate it, and
// must not re-fire OnNewError — that dedup is what keeps the phase-6 webhook
// from repeating every 5 minutes.
func TestTickerOnceIsIdempotent(t *testing.T) {
	db := tickerDB(t)
	project(t, db, "p1", "core@swarmery")
	spy := &newErrorSpy{}
	tk := &Ticker{
		DB:         db,
		Detector:   &Detector{ClaudeDir: t.TempDir(), Runner: stubRunner{out: listJSON(t)}},
		OnNewError: spy.fn,
	}

	tk.Once(context.Background())
	tk.Once(context.Background())

	if n := rowCount(t, db, RuleEnabledNotInstalled); n != 1 {
		t.Fatalf("want exactly 1 row after two passes, got %d", n)
	}
	if len(spy.calls) != 1 {
		t.Fatalf("OnNewError must fire once, fired %d times: %v", len(spy.calls), spy.calls)
	}
}

func TestTickerOnceResolvesWhenPluginAppears(t *testing.T) {
	db := tickerDB(t)
	path := project(t, db, "p1", "core@swarmery")
	claudeDir := t.TempDir()
	tk := &Ticker{DB: db, Detector: &Detector{ClaudeDir: claudeDir, Runner: stubRunner{out: listJSON(t)}}}
	tk.Once(context.Background())
	if len(activeRows(t, db, RuleEnabledNotInstalled)) != 1 {
		t.Fatal("setup: expected one active finding")
	}

	// The plugin is now installed user-scoped, into a live directory.
	installPath := t.TempDir()
	tk.Detector.Runner = stubRunner{out: listJSON(t, Installed{
		ID: "core@swarmery", Version: "2.4.0", Scope: "user", Enabled: true, InstallPath: installPath,
	})}
	tk.Once(context.Background())

	if got := activeRows(t, db, RuleEnabledNotInstalled); len(got) != 0 {
		t.Fatalf("want no active rows after the plugin resolved, got %v", got)
	}
	var resolved int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM config_lint_findings WHERE rule = ? AND target = ? AND resolved_at IS NOT NULL`,
		RuleEnabledNotInstalled, Target("core@swarmery", path)).Scan(&resolved); err != nil {
		t.Fatal(err)
	}
	if resolved != 1 {
		t.Fatalf("want the row resolved (history preserved), got %d resolved rows", resolved)
	}
}

// The most important test in the phase: when the detector goes blind, existing
// findings must stay ACTIVE. Resolving them would flip every surface to green
// at exactly the moment detection stopped working.
func TestTickerOnceBlindDetectorKeepsFindingsActive(t *testing.T) {
	db := tickerDB(t)
	path := project(t, db, "p1", "core@swarmery")
	seeded := Target("core@swarmery", path)
	if err := findings.Upsert(db, seeded, RuleEnabledNotInstalled, "error", "seeded"); err != nil {
		t.Fatal(err)
	}

	tk := &Ticker{DB: db, Detector: &Detector{
		ClaudeDir: t.TempDir(),
		Runner:    stubRunner{err: errors.New("exec: \"claude\": executable file not found in $PATH")},
	}}
	tk.Once(context.Background())

	if got := activeRows(t, db, RuleEnabledNotInstalled); got[seeded] != "seeded" {
		t.Fatalf("blind pass must leave the seeded finding active, active rows = %v", got)
	}
	unavailable := activeRows(t, db, RuleDetectorUnavailable)
	if len(unavailable) != 1 || unavailable[detectorTarget] == "" {
		t.Fatalf("want an active %s row, got %v", RuleDetectorUnavailable, unavailable)
	}
}

func TestTickerOnceRecoversFromBlindness(t *testing.T) {
	db := tickerDB(t)
	project(t, db, "p1", "core@swarmery")
	tk := &Ticker{DB: db, Detector: &Detector{
		ClaudeDir: t.TempDir(),
		Runner:    stubRunner{err: errors.New("boom")},
	}}
	tk.Once(context.Background())
	if len(activeRows(t, db, RuleDetectorUnavailable)) != 1 {
		t.Fatal("setup: expected the detector-unavailable row")
	}

	tk.Detector.Runner = stubRunner{out: listJSON(t)}
	tk.Once(context.Background())

	if got := activeRows(t, db, RuleDetectorUnavailable); len(got) != 0 {
		t.Fatalf("a healthy pass must resolve %s, still active: %v", RuleDetectorUnavailable, got)
	}
}

// A malformed payload is blindness too, not "nothing found".
func TestTickerOnceUnparsablePayloadIsBlindness(t *testing.T) {
	db := tickerDB(t)
	project(t, db, "p1", "core@swarmery")
	tk := &Ticker{DB: db, Detector: &Detector{
		ClaudeDir: t.TempDir(),
		Runner:    stubRunner{out: []byte("not json")},
	}}
	tk.Once(context.Background())

	if len(activeRows(t, db, RuleDetectorUnavailable)) != 1 {
		t.Fatal("an unparsable payload must raise plugin_detector_unavailable")
	}
	if n := rowCount(t, db, RuleEnabledNotInstalled); n != 0 {
		t.Fatalf("a blind pass must not invent findings, got %d", n)
	}
}

func TestLoadProjectsSkipsProjectsEnablingNothing(t *testing.T) {
	db := tickerDB(t)
	project(t, db, "empty")   // no settings.json at all
	project(t, db, "off", "") // settings.json without a usable id
	withPlugins := project(t, db, "on", "core@swarmery")

	got, err := loadProjects(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != withPlugins {
		t.Fatalf("want only the plugin-enabling project, got %+v", got)
	}
}

func TestRecordUnavailableWritesErrorFinding(t *testing.T) {
	db := tickerDB(t)
	RecordUnavailable(db, errors.New("claude binary not found"))

	got := activeRows(t, db, RuleDetectorUnavailable)
	if len(got) != 1 {
		t.Fatalf("want one row, got %v", got)
	}
	var severity string
	if err := db.QueryRow(
		`SELECT severity FROM config_lint_findings WHERE rule = ? AND resolved_at IS NULL`,
		RuleDetectorUnavailable).Scan(&severity); err != nil {
		t.Fatal(err)
	}
	if severity != "error" {
		t.Fatalf("blindness must be an error, got %q", severity)
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	db := tickerDB(t)
	project(t, db, "p1", "core@swarmery")
	ctx, cancel := context.WithCancel(context.Background())
	tk := &Ticker{DB: db, Detector: &Detector{ClaudeDir: t.TempDir(), Runner: stubRunner{out: listJSON(t)}}}

	done := make(chan struct{})
	go func() { tk.Run(ctx); close(done) }()
	cancel()
	<-done // the initial pass ran and Run returned; a leak would hang the test

	if len(activeRows(t, db, RuleEnabledNotInstalled)) != 1 {
		t.Fatal("Run must scan once immediately, before the first tick")
	}
}
