package sysscan

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// intQuery returns a single int64 result (ids, mins) — count() cousin.
func intQuery(t *testing.T, db *sql.DB, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("query %s: %v", q, err)
	}
	return n
}

// activeCount counts unresolved findings for (target, rule).
func activeCount(t *testing.T, db *sql.DB, target, rule string) int {
	t.Helper()
	return count(t, db,
		`SELECT COUNT(*) FROM config_lint_findings WHERE target = ? AND rule = ? AND resolved_at IS NULL`,
		target, rule)
}

// markAlive inserts one freshly-timestamped event attributed to agentID so
// the agent_dead rule sees it as alive (plain agent_id join, no heuristics).
func markAlive(t *testing.T, db *sql.DB, agentID int64) {
	t.Helper()
	mustExec(t, db,
		`INSERT INTO sessions (project_id, session_uuid, started_at) VALUES (1, 'lint-test-session', ?)`,
		time.Now().UTC().Format(time.RFC3339))
	sessionID := intQuery(t, db, `SELECT id FROM sessions WHERE session_uuid = 'lint-test-session'`)
	mustExec(t, db,
		`INSERT INTO events (session_id, ts, type, agent_id) VALUES (?, ?, 'subagent_start', ?)`,
		sessionID, time.Now().UTC().Format(time.RFC3339), agentID)
}

// TestLintRulesFireOnFixtures drives all 7 linter rules over the fixture
// tree: each rule fires on exactly its planted fixture and stays quiet on the
// clean ones.
func TestLintRulesFireOnFixtures(t *testing.T) {
	db, cfg, root := setup(t)
	s := New(db, cfg, nil)
	if _, err := s.Scan(); err != nil {
		t.Fatalf("scan: %v", err)
	}

	// global-agent gets recent telemetry — every other agent is "dead".
	aliveID := intQuery(t, db, `SELECT id FROM agents WHERE name = 'global-agent'`)
	markAlive(t, db, aliveID)

	st, err := s.Lint()
	if err != nil {
		t.Fatalf("lint: %v", err)
	}

	lintPoor := intQuery(t, db, `SELECT id FROM agents WHERE name = 'lint-poor'`)
	shortSkill := intQuery(t, db, `SELECT id FROM skills WHERE name = 'short-desc'`)
	slowHook := intQuery(t, db, `SELECT id FROM hooks WHERE event = 'PostToolUse'`)
	dupTarget := intQuery(t, db, `SELECT MIN(id) FROM agents WHERE name = 'x' AND origin <> 'plugin'`)
	claudeMD := filepath.Join(root, "project", "CLAUDE.md")

	tests := []struct {
		rule     string
		target   string
		severity string
		total    int // active findings for the rule across the whole pass
	}{
		{RuleAgentNoBoundaries, fmt.Sprintf("agent:%d", lintPoor), "warn", 1},
		{RuleAgentNoDescription, fmt.Sprintf("agent:%d", lintPoor), "warn", 1},
		{RuleSkillShortDesc, fmt.Sprintf("skill:%d", shortSkill), "warn", 1},
		{RuleClaudeMDOversized, "claude_md:" + claudeMD, "warn", 1},
		{RuleHookNoTimeout, fmt.Sprintf("hook:%d", slowHook), "warn", 1},
		{RuleAgentNameDuplicate, fmt.Sprintf("agent:%d", dupTarget), "warn", 1},
		// 10 live agents, 1 marked alive → 9 dead by available telemetry.
		{RuleAgentDead, fmt.Sprintf("agent:%d", lintPoor), "info", 9},
	}
	for _, tc := range tests {
		t.Run(tc.rule, func(t *testing.T) {
			if n := count(t, db,
				`SELECT COUNT(*) FROM config_lint_findings WHERE target = ? AND rule = ? AND severity = ? AND resolved_at IS NULL`,
				tc.target, tc.rule, tc.severity); n != 1 {
				t.Errorf("%s on %s: active findings = %d, want 1", tc.rule, tc.target, n)
			}
			if n := count(t, db,
				`SELECT COUNT(*) FROM config_lint_findings WHERE rule = ? AND resolved_at IS NULL`,
				tc.rule); n != tc.total {
				t.Errorf("%s total active = %d, want %d", tc.rule, n, tc.total)
			}
			if st.PerRule[tc.rule] != tc.total {
				t.Errorf("LintStats[%s] = %d, want %d", tc.rule, st.PerRule[tc.rule], tc.total)
			}
		})
	}

	// Clean fixtures stay quiet.
	cleanAgent := intQuery(t, db, `SELECT id FROM agents WHERE name = 'global-agent'`)
	for _, rule := range []string{RuleAgentNoBoundaries, RuleAgentNoDescription, RuleAgentDead} {
		if n := activeCount(t, db, fmt.Sprintf("agent:%d", cleanAgent), rule); n != 0 {
			t.Errorf("clean global-agent flagged by %s", rule)
		}
	}
	// broken-agent is owned by the scanner's parse_error — the content rules
	// must skip it, and the linter must not touch the parse_error row itself.
	broken := intQuery(t, db, `SELECT id FROM agents WHERE name = 'broken-agent'`)
	for _, rule := range []string{RuleAgentNoBoundaries, RuleAgentNoDescription} {
		if n := activeCount(t, db, fmt.Sprintf("agent:%d", broken), rule); n != 0 {
			t.Errorf("unparseable broken-agent flagged by %s (parse_error owns it)", rule)
		}
	}
	if n := count(t, db, `SELECT COUNT(*) FROM config_lint_findings WHERE rule = 'parse_error'`); n != 1 {
		t.Errorf("parse_error rows = %d, want 1 (linter must not touch the scanner's rule)", n)
	}
	cleanSkill := intQuery(t, db, `SELECT id FROM skills WHERE name = 'global-skill'`)
	if n := activeCount(t, db, fmt.Sprintf("skill:%d", cleanSkill), RuleSkillShortDesc); n != 0 {
		t.Errorf("clean global-skill flagged by %s", RuleSkillShortDesc)
	}
}

// TestLintLifecycle exercises the (target, rule) lifecycle: no duplicate
// actives on re-lint, resolved_at on fix, and a NEW history row on relapse.
func TestLintLifecycle(t *testing.T) {
	db, cfg, root := setup(t)
	s := New(db, cfg, nil)
	if _, err := s.Scan(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if _, err := s.Lint(); err != nil {
		t.Fatalf("lint: %v", err)
	}

	lintPoor := fmt.Sprintf("agent:%d", intQuery(t, db, `SELECT id FROM agents WHERE name = 'lint-poor'`))

	// Re-lint without changes: zero duplicate active (target, rule) pairs.
	if _, err := s.Lint(); err != nil {
		t.Fatalf("re-lint: %v", err)
	}
	if n := count(t, db,
		`SELECT COUNT(*) FROM (SELECT target, rule FROM config_lint_findings
		  WHERE resolved_at IS NULL GROUP BY target, rule HAVING COUNT(*) > 1)`); n != 0 {
		t.Fatalf("%d duplicate active (target, rule) pairs after double lint, want 0", n)
	}
	if n := activeCount(t, db, lintPoor, RuleAgentNoBoundaries); n != 1 {
		t.Fatalf("active agent_no_boundaries after double lint = %d, want 1", n)
	}

	// Fix the fixture: description added, Boundaries section added.
	agentPath := filepath.Join(root, "claude", "agents", "lint-poor.md")
	fixed := `---
name: lint-poor
description: Now with a description — the lint fixture got fixed.
model: claude-haiku-4-5
---

# lint-poor

Fixture body.

## Boundaries

- Now bounded.
`
	if err := os.WriteFile(agentPath, []byte(fixed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Scan(); err != nil {
		t.Fatalf("rescan: %v", err)
	}
	ls, err := s.Lint()
	if err != nil {
		t.Fatalf("lint after fix: %v", err)
	}
	if ls.Resolved != 2 {
		t.Errorf("Resolved after fix = %d, want 2 (boundaries + description)", ls.Resolved)
	}
	for _, rule := range []string{RuleAgentNoBoundaries, RuleAgentNoDescription} {
		if n := activeCount(t, db, lintPoor, rule); n != 0 {
			t.Errorf("%s still active after the fixture was fixed", rule)
		}
		if n := count(t, db,
			`SELECT COUNT(*) FROM config_lint_findings WHERE target = ? AND rule = ? AND resolved_at IS NOT NULL`,
			lintPoor, rule); n != 1 {
			t.Errorf("%s resolved rows = %d, want 1", rule, n)
		}
	}

	// Relapse (Boundaries removed again, description kept): a NEW row opens,
	// the resolved one stays — history is never rewritten.
	relapsed := `---
name: lint-poor
description: Now with a description — the lint fixture got fixed.
model: claude-haiku-4-5
---

# lint-poor

Fixture body without the section again.
`
	if err := os.WriteFile(agentPath, []byte(relapsed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Scan(); err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if _, err := s.Lint(); err != nil {
		t.Fatalf("lint after relapse: %v", err)
	}
	if n := activeCount(t, db, lintPoor, RuleAgentNoBoundaries); n != 1 {
		t.Errorf("active agent_no_boundaries after relapse = %d, want 1", n)
	}
	if n := count(t, db,
		`SELECT COUNT(*) FROM config_lint_findings WHERE target = ? AND rule = ?`,
		lintPoor, RuleAgentNoBoundaries); n != 2 {
		t.Errorf("agent_no_boundaries history rows = %d, want 2 (resolved + active)", n)
	}
	// The description stayed fixed — no new row for it.
	if n := count(t, db,
		`SELECT COUNT(*) FROM config_lint_findings WHERE target = ? AND rule = ?`,
		lintPoor, RuleAgentNoDescription); n != 1 {
		t.Errorf("agent_no_description history rows = %d, want 1 (resolved only)", n)
	}
}

// ---- usage-guide rules (docs/system-docs-format.md) ------------------------

// lintTarget resolves the lint target ("<kind>:<id>") of a named registry row.
func lintTarget(t *testing.T, db *sql.DB, kind, table, name string) string {
	t.Helper()
	return fmt.Sprintf("%s:%d", kind,
		intQuery(t, db, `SELECT id FROM `+table+` WHERE name = ?`, name))
}

// activeRule counts every active finding of one rule, across all targets.
func activeRule(t *testing.T, db *sql.DB, rule string) int {
	t.Helper()
	return count(t, db,
		`SELECT COUNT(*) FROM config_lint_findings WHERE rule = ? AND resolved_at IS NULL`, rule)
}

// docsRules is the rule set the guide pass owns — used to assert that a fully
// documented item is quiet on ALL of them, not just the one under test.
var docsRules = []string{
	RuleDocsMissing, RuleDocsIncomplete, RuleDocsDuplicate, RuleDocsStale, RuleDocsUnreviewed,
}

// TestLintDocs drives the five usage-guide rules over the phase-1 fixtures:
// one complete+reviewed agent (documented-agent), one generated+stale agent
// with the required subsections only (stale-docs-agent), one skill missing a
// required subsection (documented-skill), and every other live item with no
// guide at all.
//
// MinDocsSection is set EXPLICITLY so an ambient SWARMERY_LINT_MIN_DOCS_SECTION
// cannot move these counts (precedence: explicit > env > default) — the env
// wiring itself is proved by TestLintDocsEnvOverride.
func TestLintDocs(t *testing.T) {
	db, cfg, root := setup(t)
	cfg.MinDocsSection = DefaultMinDocsSection
	s := New(db, cfg, nil)
	if _, err := s.Scan(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	st, err := s.Lint()
	if err != nil {
		t.Fatalf("lint: %v", err)
	}

	complete := lintTarget(t, db, "agent", "agents", "documented-agent")
	stale := lintTarget(t, db, "agent", "agents", "stale-docs-agent")
	partial := lintTarget(t, db, "skill", "skills", "documented-skill")
	undocumented := lintTarget(t, db, "agent", "agents", "global-agent")

	tests := []struct {
		rule     string
		target   string
		severity string
		total    int // active findings for the rule across the whole pass
	}{
		// 11 live items carry no guide: 7 agents (10 live minus the documented,
		// the stale and the unparseable broken-agent the parse_error rule owns),
		// 3 skills and the one command.
		{RuleDocsMissing, undocumented, "warn", 11},
		{RuleDocsIncomplete, partial, "warn", 1},
		{RuleDocsStale, stale, "info", 1},
		{RuleDocsUnreviewed, stale, "info", 1},
	}
	for _, tc := range tests {
		t.Run(tc.rule, func(t *testing.T) {
			if n := count(t, db,
				`SELECT COUNT(*) FROM config_lint_findings WHERE target = ? AND rule = ? AND severity = ? AND resolved_at IS NULL`,
				tc.target, tc.rule, tc.severity); n != 1 {
				t.Errorf("%s on %s at severity %s: active findings = %d, want 1",
					tc.rule, tc.target, tc.severity, n)
			}
			if n := activeRule(t, db, tc.rule); n != tc.total {
				t.Errorf("%s total active = %d, want %d", tc.rule, n, tc.total)
			}
			if st.PerRule[tc.rule] != tc.total {
				t.Errorf("LintStats[%s] = %d, want %d", tc.rule, st.PerRule[tc.rule], tc.total)
			}
		})
	}

	// A complete, reviewed, current guide is quiet on every guide rule.
	t.Run("complete_guide_is_quiet", func(t *testing.T) {
		for _, rule := range docsRules {
			if n := activeCount(t, db, complete, rule); n != 0 {
				t.Errorf("documented-agent flagged by %s", rule)
			}
		}
	})

	// An absent guide is ONE finding, not four: reporting incomplete/stale/
	// unreviewed on top of docs_missing would bury the fact that matters.
	t.Run("missing_guide_reports_once", func(t *testing.T) {
		for _, rule := range []string{RuleDocsIncomplete, RuleDocsDuplicate, RuleDocsStale, RuleDocsUnreviewed} {
			if n := activeCount(t, db, undocumented, rule); n != 0 {
				t.Errorf("undocumented global-agent also flagged by %s", rule)
			}
		}
	})

	// The unparseable fixture stays the parse_error rule's business.
	t.Run("unparseable_item_is_skipped", func(t *testing.T) {
		broken := lintTarget(t, db, "agent", "agents", "broken-agent")
		for _, rule := range docsRules {
			if n := activeCount(t, db, broken, rule); n != 0 {
				t.Errorf("unparseable broken-agent flagged by %s (parse_error owns it)", rule)
			}
		}
	})

	// §5.4: a second `# How to use` H1 is a violation, never a merge.
	t.Run("docs_duplicate_block", func(t *testing.T) {
		dupPath := filepath.Join(root, "claude", "agents", "dup-docs-agent.md")
		if err := os.WriteFile(dupPath, []byte(duplicateGuideAgent), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Scan(); err != nil {
			t.Fatalf("rescan: %v", err)
		}
		if _, err := s.Lint(); err != nil {
			t.Fatalf("re-lint: %v", err)
		}
		dup := lintTarget(t, db, "agent", "agents", "dup-docs-agent")
		if n := count(t, db,
			`SELECT COUNT(*) FROM config_lint_findings
			  WHERE target = ? AND rule = ? AND severity = 'warn' AND resolved_at IS NULL`,
			dup, RuleDocsDuplicate); n != 1 {
			t.Errorf("active docs_duplicate_block on %s = %d, want 1", dup, n)
		}
	})

	// Lifecycle contract: a guide rule that stops firing RESOLVES its row.
	t.Run("resolves_after_fix", func(t *testing.T) {
		skillPath := filepath.Join(root, "claude", "skills", "documented-skill", "SKILL.md")
		raw, err := os.ReadFile(skillPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(skillPath, append(raw, []byte(workedExampleSection)...), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Scan(); err != nil {
			t.Fatalf("rescan: %v", err)
		}
		if _, err := s.Lint(); err != nil {
			t.Fatalf("lint after fix: %v", err)
		}
		if n := activeCount(t, db, partial, RuleDocsIncomplete); n != 0 {
			t.Errorf("docs_incomplete still active on %s after the gap was filled", partial)
		}
		if n := count(t, db,
			`SELECT COUNT(*) FROM config_lint_findings
			  WHERE target = ? AND rule = ? AND resolved_at IS NOT NULL`,
			partial, RuleDocsIncomplete); n != 1 {
			t.Errorf("resolved docs_incomplete rows on %s = %d, want 1", partial, n)
		}
	})
}

// duplicateGuideAgent carries two `# How to use` H1s — the §5.4 fixture.
const duplicateGuideAgent = `---
name: dup-docs-agent
description: Fixture agent carrying two usage-guide blocks — the §5.4 duplicate violation.
---

# Boundaries

- Fixture boundary.

# How to use

## What it does
Prices an order from its line items so nobody has to sum the lines by hand again.

## When to use it
- A caller sent line items and wants one priced order back.
- A line changed and the total has to be recomputed from scratch.

## How to invoke
` + "```" + `
@dup-docs-agent price orders/line-items/1042
` + "```" + `
Pass the order path; everything else is read from the order document itself.

## Worked example
` + "```" + `
> @dup-docs-agent price orders/line-items/1042
PRICED | lines: 3 | total: 148.20
` + "```" + `
You end up with the same document, now carrying a total and a per-line breakdown.

# How to use

## What it does
A second block: only the first is read, and this one is a violation, not a merge.
`

// workedExampleSection fills documented-skill's one gap. It is appended INSIDE
// the guide block (the fixture's guide runs to EOF), so §4 excludes it from the
// fingerprint and fixing the gap can never mark the item stale.
const workedExampleSection = `
## Worked example
` + "```" + `
> use documented-skill for the orders service
renders the deploy command for the orders service, and nothing else
` + "```" + `
You end up with the exact command in front of you, ready to run yourself.
`

// TestLintDocsEnvOverride proves SWARMERY_LINT_MIN_DOCS_SECTION reaches the
// docs_incomplete rule: at the 40-rune default only documented-skill's one gap
// fires; at 200 runes every guide in the fixture tree falls short.
func TestLintDocsEnvOverride(t *testing.T) {
	db, cfg, _ := setup(t)
	s := New(db, cfg, nil)
	if _, err := s.Scan(); err != nil {
		t.Fatalf("scan: %v", err)
	}

	// Explicit config wins over any ambient env — the baseline.
	relaxed := cfg
	relaxed.MinDocsSection = DefaultMinDocsSection
	if _, err := Lint(db, relaxed); err != nil {
		t.Fatalf("lint: %v", err)
	}
	base := activeRule(t, db, RuleDocsIncomplete)
	if base != 1 {
		t.Fatalf("active docs_incomplete at the %d-rune default = %d, want 1",
			DefaultMinDocsSection, base)
	}

	// Env wins over the default: cfg leaves MinDocsSection unset, so
	// withDefaults resolves it through envInt(EnvMinDocsSection, …).
	t.Setenv(EnvMinDocsSection, "200")
	if _, err := Lint(db, cfg); err != nil {
		t.Fatalf("strict lint: %v", err)
	}
	strict := activeRule(t, db, RuleDocsIncomplete)
	t.Logf("docs_incomplete: %d at min=%d, %d at min=200", base, DefaultMinDocsSection, strict)
	if strict <= base {
		t.Errorf("active docs_incomplete with %s=200 = %d, want more than the %d at the default",
			EnvMinDocsSection, strict, base)
	}
	// Every guide in the tree (documented-agent, stale-docs-agent,
	// documented-skill) falls short of 200 runes per required subsection.
	if strict != 3 {
		t.Errorf("active docs_incomplete with %s=200 = %d, want 3", EnvMinDocsSection, strict)
	}
}

// TestLintThresholdEnvOverride: precedence is explicit Config > env > default
// (SWARMERY_LINT_MIN_SKILL_DESC drives skill_short_description here).
func TestLintThresholdEnvOverride(t *testing.T) {
	t.Setenv(EnvMinSkillDescription, "5")

	db, cfg, _ := setup(t)
	if _, err := New(db, cfg, nil).Scan(); err != nil {
		t.Fatalf("scan: %v", err)
	}

	// Env wins over the 40-char default: "Too short." is 10 runes ≥ 5.
	if _, err := Lint(db, cfg); err != nil {
		t.Fatalf("lint: %v", err)
	}
	if n := count(t, db,
		`SELECT COUNT(*) FROM config_lint_findings WHERE rule = ? AND resolved_at IS NULL`,
		RuleSkillShortDesc); n != 0 {
		t.Errorf("active skill_short_description with env min=5: %d, want 0", n)
	}

	// Explicit config wins over env: min=100 flags all four fixture skills.
	strict := cfg
	strict.MinSkillDescription = 100
	if _, err := Lint(db, strict); err != nil {
		t.Fatalf("strict lint: %v", err)
	}
	if n := count(t, db,
		`SELECT COUNT(*) FROM config_lint_findings WHERE rule = ? AND resolved_at IS NULL`,
		RuleSkillShortDesc); n != 4 {
		t.Errorf("active skill_short_description with config min=100: %d, want 4", n)
	}
}
