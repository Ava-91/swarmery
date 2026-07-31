package ingest

// The per-session account dimension (migration 0047): deriving a subscription
// key from the transcript's config dir, and stamping it fill-only-when-empty.

import (
	"path/filepath"
	"testing"
)

// TestAccountForDerivesTheConfigDirKey covers the documented rule end to end:
// strip ".claude" off the config dir's basename, trim leading '-'/'.', and
// fall back to DefaultAccount when nothing is left. An unknown root stays ""
// so the column keeps its '' default (see AccountFor's doc comment).
func TestAccountForDerivesTheConfigDirKey(t *testing.T) {
	for _, tc := range []struct {
		name, root, want string
	}{
		{"stock config dir", "/home/dev/.claude/projects", DefaultAccount},
		{"named account", "/home/dev/.claude-nabu-org/projects", "nabu-org"},
		{"second named account", "/home/dev/.claude-science/projects", "science"},
		{"dot separator", "/home/dev/.claude.science/projects", "science"},
		{"double separator", "/home/dev/.claude--work/projects", "work"},

		// Trailing slash and uncleaned paths name the same config dir.
		{"trailing slash", "/home/dev/.claude-nabu-org/projects/", "nabu-org"},
		{"trailing slashes", "/home/dev/.claude-nabu-org/projects//", "nabu-org"},
		{"dot segment", "/home/dev/.claude-nabu-org/./projects", "nabu-org"},
		{"parent segment", "/home/dev/other/../.claude-nabu-org/projects", "nabu-org"},
		{"stock, trailing slash", "/home/dev/.claude/projects/", DefaultAccount},

		// Whitespace around an env-supplied root is not part of the name.
		{"padded", "  /home/dev/.claude-nabu-org/projects  ", "nabu-org"},

		// No root context at all — the ONE case that is not DefaultAccount.
		{"unknown root", "", ""},
		{"blank root", "   ", ""},

		// Degenerate roots that name no config dir fall back to the default
		// rather than inventing an account from a path fragment.
		{"relative bare root", "projects", DefaultAccount},
		{"filesystem root", "/projects", DefaultAccount},

		// A root that is not a .claude* dir keeps its basename verbatim: the
		// rule strips a prefix, it does not require one.
		{"custom dir", "/srv/transcripts/projects", "transcripts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := AccountFor(tc.root); got != tc.want {
				t.Errorf("AccountFor(%q) = %q, want %q", tc.root, got, tc.want)
			}
		})
	}
}

// TestIngestStampsTheAccountOfItsRoot: a transcript discovered under
// ~/.claude-nabu-org/projects lands with account 'nabu-org', the stock root lands
// with 'default', and a re-tail — including one through a caller with NO root
// context — never blanks or rewrites what was stamped.
func TestIngestStampsTheAccountOfItsRoot(t *testing.T) {
	db := testDB(t)

	// Two roots whose LAST TWO path segments are what the rule reads, so the
	// t.TempDir prefix is irrelevant: <tmp>/.claude-nabu-org/projects and
	// <tmp>/.claude/projects.
	base := t.TempDir()
	nabuOrgRoot := filepath.Join(base, ".claude-nabu-org", "projects")
	stockRoot := filepath.Join(base, ".claude", "projects")

	const nabuOrgUUID = "aaaaaaaa-0000-4000-8000-0000000000a1"
	const stockUUID = "bbbbbbbb-0000-4000-8000-0000000000b1"

	nabuOrgFile := filepath.Join(nabuOrgRoot, "-home-dev-alpha", nabuOrgUUID+".jsonl")
	mustWrite(t, nabuOrgFile,
		rootLine(nabuOrgUUID, "/home/dev/alpha", "u-i-1", "2026-07-31T10:00:00Z", "alpha prompt"))
	stockFile := filepath.Join(stockRoot, "-home-dev-beta", stockUUID+".jsonl")
	mustWrite(t, stockFile,
		rootLine(stockUUID, "/home/dev/beta", "u-s-1", "2026-07-31T10:00:00Z", "beta prompt"))

	p := NewPipeline(db, Config{ProjectsRoots: []string{nabuOrgRoot, stockRoot}}, nil)
	if m := p.Backfill(t.Context()); m.Files != 2 || m.Errors != 0 {
		t.Fatalf("backfill metrics = %s, want files=2 errors=0", m)
	}

	sessionAccount := func(uuid string) string {
		t.Helper()
		var account string
		if err := db.QueryRow(
			`SELECT account FROM sessions WHERE session_uuid = ?`, uuid).Scan(&account); err != nil {
			t.Fatalf("read account of %s: %v", uuid, err)
		}
		return account
	}

	if got := sessionAccount(nabuOrgUUID); got != "nabu-org" {
		t.Errorf("account of the ~/.claude-nabu-org session = %q, want nabu-org", got)
	}
	if got := sessionAccount(stockUUID); got != DefaultAccount {
		t.Errorf("account of the ~/.claude session = %q, want %s", got, DefaultAccount)
	}

	// Re-tail with new lines through the SAME root: unchanged.
	mustAppend(t, nabuOrgFile,
		rootLine(nabuOrgUUID, "/home/dev/alpha", "u-i-2", "2026-07-31T10:05:00Z", "more alpha"))
	if _, err := TailFile(db, nabuOrgFile, nabuOrgRoot, DefaultThresholds()); err != nil {
		t.Fatalf("re-tail: %v", err)
	}
	if got := sessionAccount(nabuOrgUUID); got != "nabu-org" {
		t.Errorf("account after a same-root re-tail = %q, want nabu-org", got)
	}

	// Re-tail through a caller with NO root context (the single-file
	// `swarmery ingest` path): the derived key is "" and must NOT blank the
	// stamp — this is the regression the fill-only-when-empty rule prevents.
	mustAppend(t, nabuOrgFile,
		rootLine(nabuOrgUUID, "/home/dev/alpha", "u-i-3", "2026-07-31T10:10:00Z", "even more alpha"))
	if _, err := TailFile(db, nabuOrgFile, "", DefaultThresholds()); err != nil {
		t.Fatalf("rootless re-tail: %v", err)
	}
	if got := sessionAccount(nabuOrgUUID); got != "nabu-org" {
		t.Errorf("account after a rootless re-tail = %q, want nabu-org (never blanked)", got)
	}

	// A full re-read from byte 0 (rebuild-text) through a DIFFERENT root does
	// not re-point an already-stamped session either: a session belongs to one
	// subscription for its whole life, so the first root that knew wins.
	if _, err := fileFrom(db, nabuOrgFile, stockRoot); err != nil {
		t.Fatalf("re-read under the stock root: %v", err)
	}
	if got := sessionAccount(nabuOrgUUID); got != "nabu-org" {
		t.Errorf("account after a foreign-root re-read = %q, want nabu-org", got)
	}
}

// TestIngestLeavesTheAccountEmptyWithoutARoot: File() (the single-file
// `swarmery ingest` subcommand) has no projects root, so its sessions keep the
// '' default and a later multi-root pass can still stamp them.
func TestIngestLeavesTheAccountEmptyWithoutARoot(t *testing.T) {
	db := testDB(t)
	if _, err := File(db, filepath.Join(fixtures, "simple-session.jsonl")); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	var account string
	if err := db.QueryRow(`SELECT account FROM sessions LIMIT 1`).Scan(&account); err != nil {
		t.Fatalf("read account: %v", err)
	}
	if account != "" {
		t.Errorf("account = %q, want '' (no root context)", account)
	}
}
