package improve

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveAgentInRepoRealGit proves the git plumbing end-to-end against REAL
// git via OSExec (not the canned resolverExec): a repo whose origin/main ships
// plugins/core/agents/foo.md resolves to that repo-relative path with the
// committed content. Mirrors apply_realgit_test.go's initAgentRepo pattern, but
// wires an `origin` remote with a `main` branch so `git show origin/main:…` and
// `git ls-tree origin/main` resolve.
func TestResolveAgentInRepoRealGit(t *testing.T) {
	ex := OSExec{}
	ctx := context.Background()

	// Bare origin the working clone pushes main to.
	origin := t.TempDir()
	if out, err := ex.Run(ctx, origin, "git", "init", "--bare", "-q", "-b", "main"); err != nil {
		t.Fatalf("git init --bare: %v (%s)", err, out)
	}

	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if out, err := ex.Run(ctx, repo, "git", args...); err != nil {
			t.Fatalf("git %s: %v (%s)", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	run("config", "commit.gpgsign", "false")
	run("remote", "add", "origin", origin)

	const agentRel = "plugins/core/agents/foo.md"
	const body = "---\nname: foo\ndescription: a test agent\n---\nfoo body\n"
	if err := os.MkdirAll(filepath.Join(repo, "plugins/core/agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, agentRel), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "add foo agent")
	run("push", "-q", "origin", "main")

	relPath, content, err := resolveAgentInRepo(ex, repo, "foo")
	if err != nil {
		t.Fatalf("resolveAgentInRepo: %v", err)
	}
	if relPath != agentRel {
		t.Errorf("relPath = %q, want %q", relPath, agentRel)
	}
	if content != body {
		t.Errorf("content = %q, want the committed origin/main content %q", content, body)
	}

	// The repo agent set built off the same origin/main includes foo and only foo.
	set, err := repoAgentSet(ex, repo)
	if err != nil {
		t.Fatalf("repoAgentSet: %v", err)
	}
	if _, ok := set["foo"]; !ok || len(set) != 1 {
		t.Errorf("repoAgentSet = %v, want exactly {foo}", set)
	}

	// An agent absent from origin/main is ErrAgentNotFound.
	if _, _, err := resolveAgentInRepo(ex, repo, "missing"); err == nil {
		t.Error("resolveAgentInRepo(missing) = nil, want ErrAgentNotFound")
	}
}
