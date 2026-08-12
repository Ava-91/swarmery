package claudeacct

// Provisioning: creating and removing the config dir a NON-DEFAULT account
// lives in.
//
// # What provisioning deliberately is not
//
// It is not a login. swarmery creates the directory and stops; a credential
// arrives in the dir in one of exactly two ways, neither of them here:
//
//   - the `claude` CLI performs the account's own OAuth login into it, under
//     its own CLAUDE_CONFIG_DIR — the manual path;
//   - the connect flow performs a ONE-TIME handoff of the account's
//     swarmery-owned credential into <dir>/.credentials.json
//     (usage.HandoffToConfigDir): written at most once per connect, NEVER
//     refreshed, and adopted — consumed — by the CLI from then on.
//
// The measured facts behind that split are in
// docs/claude-cli-credential-behaviour.md (2026-08-12, re-run of the
// 2026-08-06 spike on CLI 2.1.220): a config dir with no credential of its own
// fails authentication outright and does NOT fall back to the default account
// — so a config dir really is the whole account boundary — and the CLI's store
// deletes <dir>/.credentials.json after a successful Keychain write. That is
// what makes a write-once handoff safe and a REFRESHING writer unsafe: token
// rotation stays exclusively in swarmery's own store (internal/usage/store.go).
//
// This package still touches no credential itself: Provision creates
// directories; Remove deletes a directory tree. The only credential-shaped
// thing in this file is the 0700 mode, which exists precisely BECAUSE a token
// may later land in the dir — via the CLI's plaintext fallback or the handoff.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/ingest"
)

// configDirMode is the permission an account's config dir gets. It matches
// internal/usage's own store (storeDirMode = 0o700) for one reason: the `claude`
// CLI may drop a .credentials.json in here — that is its documented plaintext
// fallback when the Keychain is unavailable — so the directory is treated as
// credential-bearing from the moment it is created, not from the moment a token
// first appears in it.
const configDirMode = 0o700

// projectsDirName is the transcript tree inside a config dir. An empty one is
// all the daemon's ingest glob (ProjectsRoots) needs to see the account; the CLI
// fills in everything else — .claude.json, plugins/, sessions/ — on its first
// run there.
const projectsDirName = "projects"

// ErrDefaultAccount is returned by Provision and Remove for the default
// account. It is not an "unsupported" placeholder: the default account is the
// operator's primary login, it already exists at ~/.claude, and swarmery
// creating or (far worse) deleting it would destroy the very login every other
// account is managed from.
var ErrDefaultAccount = errors.New(
	"claudeacct: the default account is never provisioned or removed by swarmery — " +
		"it is the operator's primary login and already exists at ~/.claude")

// ProjectsRoot is the account's transcript root — the path that has to appear in
// the daemon's live ingest roots for this account's sessions to be indexed.
//
// A method rather than a literal join at each call site so that "which directory
// is an account ingested from" has exactly ONE definition, shared with
// ProjectsRoots' glob. A reader answering that question from a hand-written
// filepath.Join in another package is how the two silently drift apart.
func (a Account) ProjectsRoot() string {
	return filepath.Join(a.ConfigDir, projectsDirName)
}

// Provision creates the config dir for an account and returns it.
//
// Deliberately minimal: <dir> at 0700 plus an empty <dir>/projects, and nothing
// else. See the file header for why it stops there.
//
// IDEMPOTENT: an existing dir is success, not a conflict — the operator asked
// for the account to exist, not for a creation receipt, and a retried click on a
// dashboard button must not read as an error. An existing dir keeps the mode it
// has: provisioning twice must not silently re-permission a directory swarmery
// did not create.
//
// Refuses an invalid key (ValidKey) and the default account (ErrDefaultAccount).
func Provision(key string) (Account, error) {
	dir, err := provisionTarget(key)
	if err != nil {
		return Account{}, err
	}
	if err := mkdirPrivate(dir); err != nil {
		return Account{}, err
	}
	if err := mkdirPrivate(filepath.Join(dir, projectsDirName)); err != nil {
		return Account{}, err
	}
	return Account{Key: strings.TrimSpace(key), ConfigDir: dir, IsDefault: false}, nil
}

// Remove deletes an account's config dir, with the SAME guards as Provision and
// never for the default account.
//
// This is the destructive operation of the feature — it takes the account's
// transcripts with it — so it is explicit and is never implied by a disconnect,
// an unbind, or any other lesser action.
//
// IDEMPOTENT for the same reason as Provision: an account whose dir is already
// gone is already removed.
func Remove(key string) error {
	dir, err := provisionTarget(key)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("claudeacct: cannot remove %s: %w", dir, err)
	}
	return nil
}

// provisionTarget resolves — and fences — the one directory swarmery may create
// or delete for key. Shared by Provision and Remove so the two can never disagree
// about which path they operate on.
//
// An account that ALREADY exists is operated on where it actually lives: an
// operator whose dir is ~/.claude.work (dot, not dash) keys as "work", and only
// discovery knows that. Provisioning it at the canonical ~/.claude-work would
// give one account two dirs, and removing the canonical path would leave the
// real one behind while reporting success.
func provisionTarget(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == ingest.DefaultAccount {
		return "", ErrDefaultAccount
	}
	// Validate before anything touches the filesystem: the key becomes a
	// directory name under $HOME, so it is validated, never trusted.
	if !ValidKey(key) {
		return "", fmt.Errorf("claudeacct: %q is not a valid account key", key)
	}
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("claudeacct: cannot resolve home dir: %w", err)
	}
	if home == "" {
		return "", errors.New("claudeacct: cannot resolve home dir: empty")
	}

	dir := ""
	for _, a := range Discover() {
		if a.Key != key {
			continue
		}
		if a.IsDefault {
			return "", ErrDefaultAccount
		}
		dir = a.ConfigDir
		break
	}
	if dir == "" {
		if dir, err = ConfigDirFor(key); err != nil {
			return "", err
		}
	}

	// Final fence, independent of HOW dir was resolved. Both resolution paths
	// are already safe on their own (ValidKey forbids separators and "..", and
	// the discovery glob only yields ~/.claude*), so this is belt-and-braces —
	// which is the right amount of paranoia for the one function in this package
	// that can call os.RemoveAll.
	clean := filepath.Clean(dir)
	if clean == filepath.Clean(filepath.Join(home, ".claude")) {
		return "", ErrDefaultAccount
	}
	if filepath.Dir(clean) != filepath.Clean(home) ||
		!strings.HasPrefix(filepath.Base(clean), ".claude") {
		return "", fmt.Errorf(
			"claudeacct: refusing to manage %s — an account config dir is a .claude* "+
				"directory directly under the home directory", clean)
	}
	return clean, nil
}

// mkdirPrivate creates dir at EXACTLY configDirMode, and reports an existing dir
// as success.
//
// The explicit Chmod is not redundant: os.MkdirAll applies the process umask, so
// the mode passed to it is a ceiling rather than a guarantee, and a daemon
// started under an unusual umask would otherwise create a dir the CLI cannot
// populate. It runs only on a dir this call created — see Provision on why an
// existing dir keeps its mode.
func mkdirPrivate(dir string) error {
	switch fi, err := os.Stat(dir); {
	case err == nil && fi.IsDir():
		return nil
	case err == nil:
		return fmt.Errorf("claudeacct: %s exists and is not a directory", dir)
	case !os.IsNotExist(err):
		return fmt.Errorf("claudeacct: cannot inspect %s: %w", dir, err)
	}
	if err := os.MkdirAll(dir, configDirMode); err != nil {
		return fmt.Errorf("claudeacct: cannot create %s: %w", dir, err)
	}
	if err := os.Chmod(dir, configDirMode); err != nil {
		return fmt.Errorf("claudeacct: cannot secure %s: %w", dir, err)
	}
	return nil
}
