package improve

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// resolverExec is a fake Exec for resolver tests: it answers `git ls-tree`
// with lsTree, `git show origin/main:<path>` from the show map keyed by <path>,
// and swallows `git fetch` (optionally erroring to prove fetch is best-effort).
// It records the full arg lists so tests can assert the exact git plumbing.
type resolverExec struct {
	lsTree   string
	lsErr    error
	show     map[string]string // relPath → content
	fetchErr error
	calls    [][]string
}

func (r *resolverExec) Run(_ context.Context, _ string, name string, args ...string) (string, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	if name != "git" || len(args) == 0 {
		return "", nil
	}
	switch args[0] {
	case "fetch":
		return "", r.fetchErr
	case "ls-tree":
		return r.lsTree, r.lsErr
	case "show":
		// args[len-1] is origin/main:<relPath>
		ref := args[len(args)-1]
		if i := strings.IndexByte(ref, ':'); i >= 0 {
			rel := ref[i+1:]
			if c, ok := r.show[rel]; ok {
				return c, nil
			}
		}
		return "", errors.New("no such object: " + ref)
	}
	return "", nil
}

func (r *resolverExec) ReadFile(string) ([]byte, error) { return nil, errors.New("unused") }
func (r *resolverExec) WriteFile(string, []byte) error  { return errors.New("unused") }
func (r *resolverExec) MkdirTemp() (string, error)      { return "", errors.New("unused") }
func (r *resolverExec) RemoveAll(string) error          { return nil }

func (r *resolverExec) ranFetch() bool {
	for _, c := range r.calls {
		if len(c) >= 2 && c[0] == "git" && c[1] == "fetch" {
			return true
		}
	}
	return false
}

const coreLsLine = "plugins/core/agents/full-stack-feature.md"

func TestResolveAgentInRepoCore(t *testing.T) {
	ex := &resolverExec{
		lsTree: "README.md\n" + coreLsLine + "\nplugins/core/skills/x.md\n",
		show:   map[string]string{coreLsLine: "---\nname: full-stack-feature\n---\ncore body\n"},
	}
	rel, content, err := resolveAgentInRepo(ex, "/repo", "full-stack-feature")
	if err != nil {
		t.Fatalf("resolveAgentInRepo: %v", err)
	}
	if rel != coreLsLine {
		t.Errorf("relPath = %q, want %q", rel, coreLsLine)
	}
	if content != "---\nname: full-stack-feature\n---\ncore body\n" {
		t.Errorf("content = %q, want the origin/main show output", content)
	}
	if !ex.ranFetch() {
		t.Error("resolver must best-effort `git fetch origin main`")
	}
}

// A pack-only agent (no core copy) resolves to its pack path.
func TestResolveAgentInRepoPackOnly(t *testing.T) {
	const p = "plugins/uav-pack/agents/mission-planner.md"
	ex := &resolverExec{
		lsTree: p + "\n",
		show:   map[string]string{p: "pack body\n"},
	}
	rel, content, err := resolveAgentInRepo(ex, "/repo", "mission-planner")
	if err != nil {
		t.Fatalf("resolveAgentInRepo: %v", err)
	}
	if rel != p {
		t.Errorf("relPath = %q, want %q", rel, p)
	}
	if content != "pack body\n" {
		t.Errorf("content = %q", content)
	}
}

// Best-effort fetch: a fetch error does NOT hard-fail resolution.
func TestResolveAgentInRepoFetchErrorTolerated(t *testing.T) {
	ex := &resolverExec{
		lsTree:   coreLsLine + "\n",
		show:     map[string]string{coreLsLine: "body\n"},
		fetchErr: errors.New("could not read from remote"),
	}
	if _, _, err := resolveAgentInRepo(ex, "/repo", "full-stack-feature"); err != nil {
		t.Fatalf("fetch error must be tolerated, got: %v", err)
	}
}

func TestResolveAgentInRepoAbsent(t *testing.T) {
	ex := &resolverExec{lsTree: "plugins/core/agents/other.md\n"}
	if _, _, err := resolveAgentInRepo(ex, "/repo", "ghost"); !errors.Is(err, ErrAgentNotFound) {
		t.Fatalf("absent agent err = %v, want ErrAgentNotFound", err)
	}
}

// Traversal names must be rejected before any repo path is built.
func TestResolveAgentInRepoTraversalBlocked(t *testing.T) {
	ex := &resolverExec{lsTree: coreLsLine + "\n"}
	for _, bad := range []string{"..", "../evil", "a/b", "foo.md", ".", "core:x", "UPPER"} {
		if _, _, err := resolveAgentInRepo(ex, "/repo", bad); !errors.Is(err, ErrAgentNotFound) {
			t.Errorf("agent %q: err = %v, want ErrAgentNotFound (traversal blocked)", bad, err)
		}
	}
	// A rejected name must never reach git ls-tree/show.
	for _, c := range ex.calls {
		if len(c) >= 2 && (c[1] == "ls-tree" || c[1] == "show") {
			t.Errorf("invalid name reached git %v", c)
		}
	}
}

// core + pack ship the same-named agent → core wins.
func TestResolveAgentInRepoCoreWinsCollision(t *testing.T) {
	const core = "plugins/core/agents/tech-lead.md"
	const pack = "plugins/uav-pack/agents/tech-lead.md"
	ex := &resolverExec{
		lsTree: pack + "\n" + core + "\n", // pack listed first on purpose
		show:   map[string]string{core: "CORE\n", pack: "PACK\n"},
	}
	rel, content, err := resolveAgentInRepo(ex, "/repo", "tech-lead")
	if err != nil {
		t.Fatalf("resolveAgentInRepo: %v", err)
	}
	if rel != core {
		t.Errorf("relPath = %q, want core %q", rel, core)
	}
	if content != "CORE\n" {
		t.Errorf("content = %q, want core content", content)
	}
}

// Two non-core packs → lexicographically smallest path (deterministic).
func TestResolveAgentInRepoLexicographicTie(t *testing.T) {
	const a = "plugins/aaa-pack/agents/shared.md"
	const z = "plugins/zzz-pack/agents/shared.md"
	ex := &resolverExec{
		lsTree: z + "\n" + a + "\n", // listed z-first
		show:   map[string]string{a: "A\n", z: "Z\n"},
	}
	rel, _, err := resolveAgentInRepo(ex, "/repo", "shared")
	if err != nil {
		t.Fatalf("resolveAgentInRepo: %v", err)
	}
	if rel != a {
		t.Errorf("relPath = %q, want lexicographically smallest %q", rel, a)
	}
}

func TestRepoAgentSet(t *testing.T) {
	ex := &resolverExec{
		lsTree: "README.md\n" +
			"plugins/core/agents/tech-lead.md\n" +
			"plugins/core/agents/full-stack-feature.md\n" +
			"plugins/uav-pack/agents/mission-planner.md\n" +
			"plugins/core/skills/x.md\n" +
			"plugins/core/agents/nested/deep.md\n", // not a direct agent file
	}
	set, err := repoAgentSet(ex, "/repo")
	if err != nil {
		t.Fatalf("repoAgentSet: %v", err)
	}
	for _, want := range []string{"tech-lead", "full-stack-feature", "mission-planner"} {
		if _, ok := set[want]; !ok {
			t.Errorf("set missing %q: %v", want, set)
		}
	}
	if _, ok := set["deep"]; ok {
		t.Error("nested non-agent path leaked into the set")
	}
	if _, ok := set["x"]; ok {
		t.Error("skill path leaked into the set")
	}
}

// Repo empty → empty set, no panic (improvable:false for every agent).
func TestRepoAgentSetEmptyRepo(t *testing.T) {
	set, err := repoAgentSet(&resolverExec{}, "")
	if err != nil {
		t.Fatalf("repoAgentSet: %v", err)
	}
	if len(set) != 0 {
		t.Errorf("empty repo set = %v, want empty", set)
	}
	// nil exec also → empty set.
	set, err = repoAgentSet(nil, "/repo")
	if err != nil {
		t.Fatalf("repoAgentSet nil exec: %v", err)
	}
	if len(set) != 0 {
		t.Errorf("nil-exec set = %v, want empty", set)
	}
}
