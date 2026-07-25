package improve

// Evidence assembles the read-only phase-3 evidence bundle for one (normalized)
// agent key — the same bundle Generate feeds the model, exposed so the
// dashboard can PREVIEW it before triggering a (minutes-long) generation. Never
// mutates any row. Returns ErrAgentNotFound for an agent that does not ship in
// the apply repo at origin/main.
func (s *Service) Evidence(agent string) (*Evidence, error) {
	return s.buildEvidence(agent)
}

// RegistryAgentSet returns the set of agent names that ship in the APPLY REPO
// at origin/main (plugins/*/agents/<name>.md) — the agents Evidence/Generate
// can act on. Built-in agents (Explore, general-purpose) and cross-project
// specialists that live only in another checkout are absent. Backed by
// repoAgentSet; used by the scorecards handler to gate the Improve button. A
// missing repo/exec yields an empty set (never a panic), so the button hides.
func (s *Service) RegistryAgentSet() (map[string]struct{}, error) {
	return repoAgentSet(s.Exec, s.Repo)
}
