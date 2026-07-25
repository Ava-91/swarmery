package improve

import (
	"errors"
	"testing"
)

// An agent shipping in the apply repo at origin/main appears in the set and
// yields a full evidence bundle sourced from origin/main.
func TestEvidenceAndRegistrySetPresent(t *testing.T) {
	db := openDB(t)
	const body = "---\nname: tech-lead\n---\nbody"
	seedAgent(t, db, 1, "tech-lead", "local", "/repo/.claude/agents/tech-lead.md", "stale db body")
	s := &Service{DB: db, Repo: "/repo", Exec: repoExecFor("tech-lead", body)}

	set, err := s.RegistryAgentSet()
	if err != nil {
		t.Fatalf("RegistryAgentSet: %v", err)
	}
	if _, ok := set["tech-lead"]; !ok {
		t.Fatalf("registry set %v missing tech-lead", set)
	}

	ev, err := s.Evidence("tech-lead")
	if err != nil {
		t.Fatalf("Evidence: %v", err)
	}
	if ev.Bundle == "" {
		t.Error("Evidence returned an empty bundle")
	}
	if ev.AgentPath != "plugins/core/agents/tech-lead.md" {
		t.Errorf("AgentPath = %q, want the repo-relative origin/main path", ev.AgentPath)
	}
	if ev.AgentContent != body {
		t.Errorf("AgentContent = %q, want the origin/main content", ev.AgentContent)
	}
	if ev.BaseSHA256 == "" {
		t.Error("Evidence returned an empty BaseSHA256")
	}
}

// An agent that does NOT ship in the apply repo (built-in like debugger, or a
// cross-project specialist that lives only in another checkout) is absent from
// the set, and Evidence returns ErrAgentNotFound — even though a DB row exists.
func TestEvidenceAndRegistrySetAbsent(t *testing.T) {
	db := openDB(t)
	seedAgent(t, db, 1, "tech-lead", "local", "/x/tech-lead.md", "body")
	// origin/main ships only tech-lead; debugger is not in the apply repo.
	s := &Service{DB: db, Repo: "/repo", Exec: repoExecFor("tech-lead", "body")}

	set, err := s.RegistryAgentSet()
	if err != nil {
		t.Fatalf("RegistryAgentSet: %v", err)
	}
	if _, ok := set["debugger"]; ok {
		t.Errorf("registry set %v unexpectedly contains debugger (not in apply repo)", set)
	}

	if _, err := s.Evidence("debugger"); !errors.Is(err, ErrAgentNotFound) {
		t.Fatalf("Evidence(debugger) err = %v, want ErrAgentNotFound", err)
	}
}
