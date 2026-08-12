package usage

// One-time credential handoff — the write half of "Connect actually connects".
//
// This file is the ONLY place swarmery ever writes a CLI credential file, and
// it does so under a contract measured in docs/claude-cli-credential-behaviour.md
// (2026-08-12, re-run of the 2026-08-06 spike on CLI 2.1.220):
//
//   - a config dir with no credential fails authentication outright and does
//     NOT fall back to the default account — so the handed-over file is both
//     necessary and sufficient to make the account usable;
//   - the CLI's store deletes <dir>/.credentials.json after a successful
//     Keychain write — so the file is consumed, not co-owned.
//
// What stays forbidden is MAINTAINING that file: token rotation happens
// exclusively in swarmery's own store (store.go, writeStoredCreds — the only
// callers are the login and refresh paths, and both target the store path,
// never a config dir). Two writers chasing one rotating refresh token is the
// failure mode the 2026-08-06 spike ruled out, and it stays ruled out.

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/claudeacct"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/ingest"
)

var (
	// ErrHandoffExists is returned when <configDir>/.credentials.json already
	// exists. An existing credential there is the CLI's, not ours, and
	// clobbering it would strand a working login — the caller's signal to skip
	// straight to verification.
	ErrHandoffExists = errors.New(
		"usage: the config dir already holds a credential file — it is the CLI's, " +
			"never overwritten by swarmery")
	// ErrHandoffDefaultAccount mirrors claudeacct.ErrDefaultAccount: ~/.claude
	// is the operator's primary login, and swarmery writing into it could
	// shadow or corrupt the very login every other account is managed from.
	ErrHandoffDefaultAccount = errors.New(
		"usage: the default account never receives a credential handoff — it is " +
			"the operator's primary login and swarmery does not write into ~/.claude")
	// errHandoffDirMismatch refuses a configDir that is not the directory the
	// account registry (claudeacct) reports for the account. The registry is
	// the source of truth; a path taken from anywhere else — a request body
	// most of all — must never become a write target.
	errHandoffDirMismatch = errors.New(
		"usage: refusing handoff — the directory is not the account registry's " +
			"config dir for this account")
)

// HandoffToConfigDir writes the account's swarmery-owned credential into
// <configDir>/.credentials.json so the `claude` CLI can adopt it.
//
// WRITE-ONCE, BY CONTRACT. swarmery writes this file at most once per connect
// and NEVER refreshes it. The CLI adopts it — on macOS it migrates the
// credential into the suffixed login-Keychain item and deletes the file — and
// from that moment the CLI is its only owner. Rotation continues to happen
// exclusively in swarmery's own store (store.go), so the two never write one
// rotating refresh token.
//
// It refuses to overwrite an existing file: an existing credential there is
// the CLI's, not ours, and clobbering it would strand a working login.
// ErrHandoffExists is the caller's signal to skip straight to verification.
//
// The file carries exactly the {"claudeAiOauth": {...}} shape the CLI reads
// (storedBody — the same serializer the store uses), at mode 0600 in a dir
// created 0700 when absent. Nothing here logs credential material: a failure
// logs the account key and a fixed phrase, and the returned sentinel errors
// are fixed phrases too.
func HandoffToConfigDir(account, configDir string) error {
	dir, err := handoffRegistryDir(account)
	if err != nil {
		return handoffFailed(account, err)
	}
	if filepath.Clean(configDir) != dir {
		return handoffFailed(account, errHandoffDirMismatch)
	}
	// Read through the store's own reader — never a credential handed in from
	// an HTTP layer, and never a hand-rolled re-read of the file. A nil here
	// covers every "not connected through swarmery" state, including the
	// per-account disabled flag: a parked account hands nothing over.
	c := storedCreds(account)
	if c == nil {
		return handoffFailed(account, ErrNoCreds)
	}
	if err := handoffWrite(dir, c); err != nil {
		return handoffFailed(account, err)
	}
	return nil
}

// handoffRegistryDir resolves — and fences — the one directory a handoff for
// account may target: the config dir the account registry reports. Prefers the
// dir Discover reports (an account living at a non-canonical ~/.claude.work
// keys as "work", and only discovery knows that — same reasoning as
// claudeacct.provisionTarget), falling back to the canonical ConfigDirFor for
// an account whose dir does not exist yet. The default account is refused on
// every path.
func handoffRegistryDir(account string) (string, error) {
	if strings.TrimSpace(account) == ingest.DefaultAccount {
		return "", ErrHandoffDefaultAccount
	}
	for _, a := range claudeacct.Discover() {
		if a.Key != account {
			continue
		}
		if a.IsDefault {
			return "", ErrHandoffDefaultAccount
		}
		return filepath.Clean(a.ConfigDir), nil
	}
	dir, err := claudeacct.ConfigDirFor(account)
	if err != nil {
		return "", err
	}
	return filepath.Clean(dir), nil
}

// handoffWrite lands the credential at <dir>/.credentials.json with O_EXCL
// semantics AND content atomicity: the body goes through the store's own
// temp-file writer (storeWriteTemp — 0600, fsync'd), then is published with
// os.Link rather than os.Rename. Link is the rename that refuses to clobber:
// it fails with EEXIST when a destination appeared between the up-front check
// and the publish, so the CLI's own file can never be overwritten, not even by
// a race. The up-front Lstat exists to make the common already-exists case
// cheap and side-effect-free.
func handoffWrite(dir string, c *Creds) error {
	dst := filepath.Join(dir, credentialsFile)
	if _, err := os.Lstat(dst); err == nil {
		return ErrHandoffExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// 0700 matches claudeacct.configDirMode (and the store's own storeDirMode):
	// the dir is credential-bearing from the moment it exists. The explicit
	// Chmod defeats the process umask, but only on a dir this call created —
	// an existing dir keeps whatever mode the operator gave it.
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(dir, storeDirMode); err != nil {
			return err
		}
		if err := os.Chmod(dir, storeDirMode); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	body, err := storedBody(c)
	if err != nil {
		return err
	}
	tmpName, err := storeWriteTemp(dir, body)
	if err != nil {
		return err
	}
	if err := os.Link(tmpName, dst); err != nil {
		os.Remove(tmpName)
		if errors.Is(err, os.ErrExist) {
			return ErrHandoffExists
		}
		return err
	}
	os.Remove(tmpName)
	return nil
}

// handoffFailed logs the failure — the account key and a fixed phrase, nothing
// else; never the error, which on an os failure could carry a path — and hands
// the error back for the caller to classify.
func handoffFailed(account string, err error) error {
	log.Printf("usage: credential handoff failed: account=%s", account)
	return err
}
