package routines

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Seeding must converge. A routine duplicated on every deploy would fire its
// whole step list several times a month, and this particular routine spends
// API on two of its steps — so "seeded twice" is a bill, not a cosmetic bug.
func TestSeedIsIdempotentByName(t *testing.T) {
	db := migratedTestDB(t)
	svc := NewService(db, nil, nil, true)

	sf := &SeedFile{
		Name: "model-upgrade", Cron: "0 9 1 * *", Enabled: true,
		CatchUp: "run_one", TimeoutSec: 600,
		Steps: []Step{{Type: StepCommand, Name: "v", Command: "true"}},
	}

	first, created, err := svc.Seed(sf, sql.NullInt64{})
	if err != nil || !created {
		t.Fatalf("first seed: created=%v err=%v", created, err)
	}

	sf.Cron = "0 10 1 * *" // the definition moved on
	second, created, err := svc.Seed(sf, sql.NullInt64{})
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("second seed created a new routine — seeding must match on name and update")
	}
	if second.ID != first.ID {
		t.Errorf("id changed %s -> %s: the routine's identity must survive a re-seed",
			first.ID, second.ID)
	}
	if second.CronExpr != "0 10 1 * *" {
		t.Errorf("cron = %q, want the updated definition to win", second.CronExpr)
	}

	all, err := svc.List(0)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	for _, r := range all {
		if r.Name == "model-upgrade" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("routines named model-upgrade = %d, want 1", n)
	}
}

// The shipped definition is a contract with the executor: it must validate
// under the same rules the API applies, and use only the three step kinds that
// exist. If this fails, the routine was written against an imagined engine.
func TestShippedModelUpgradeRoutine(t *testing.T) {
	path := filepath.Join("..", "..", "config", "routines", "model-upgrade.json")
	sf, err := LoadSeedFile(path)
	if err != nil {
		t.Fatalf("shipped routine: %v", err)
	}

	if _, ok := NextRun(sf.Cron, time.Now().UTC()); !ok {
		t.Errorf("cron %q does not parse", sf.Cron)
	}

	kinds := map[string]int{}
	for _, s := range sf.Steps {
		kinds[s.Type]++
		switch s.Type {
		case StepCommand, StepAIPrompt, StepCreateTask:
		default:
			t.Errorf("step %q uses kind %q, which the executor does not implement", s.Name, s.Type)
		}
	}
	for _, want := range []string{StepCommand, StepAIPrompt, StepCreateTask} {
		if kinds[want] == 0 {
			t.Errorf("no %s step: audit 6.1 needs all three kinds "+
				"(checks, judgement, and a card when something drifted)", want)
		}
	}

	// The eval step must not abort the run: a new model legitimately returns a
	// non-pass, and the create-task step downstream is how that gets acted on.
	for _, s := range sf.Steps {
		if s.Name == "model eval" && !s.ContinueOnFailure {
			t.Error("the model eval step must set continueOnFailure, or a non-pass " +
				"verdict kills the run before the card that reports it")
		}
	}
}

// The prompt audit is the rule that took the roster from 42 to 13. If it
// drifts into a vague "review the agents", the routine stops doing the one
// thing that prevents the debt returning.
func TestPromptAuditStepKeepsItsQuestion(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "routines", "model-upgrade.json"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, must := range []string{"always", "never", "minimum count", "what actually breaks"} {
		if !contains(body, must) {
			t.Errorf("the prompt audit step no longer asks about %q", must)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
