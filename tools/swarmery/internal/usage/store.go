package usage

// Swarmery's OWN credential store — rung 2 of the multi-account ladder.
//
// # Why a second store exists at all
//
// Rung 1 reads <configDir>/.credentials.json, which is where the `claude` CLI
// keeps an account's OAuth credential on Linux and Windows. On macOS the CLI
// keeps it in the login Keychain instead, and the per-config-dir item naming is
// undocumented CC-internal behaviour we deliberately do not build on. So on
// macOS a NON-DEFAULT account has no readable credential at all, and rung 1
// renders a permanent "Connect" card for it.
//
// Rung 2 closes that: the operator authorizes swarmery ONCE per account
// (login.go — authorization code + PKCE), and the resulting credential lands
// HERE, in a store swarmery owns end to end. It works the same on every OS,
// needs no keychain trick, and survives any future change to how the CLI stores
// its own credential.
//
// # Ownership changes the write-back rule
//
// A rung-1 credential is the CLI's; we never write to it (claude.go's
// refreshedToken is in-memory only) because two writers racing over a rotating
// refresh token can strand the CLI's login. A rung-2 credential is OURS: there
// is no other writer, so a rotated refresh token MUST be persisted or the
// session dies the moment the daemon restarts after a rotation. Creds.FromStore
// carries that provenance.
//
// # Layout and hygiene (R2/R3)
//
//	~/.swarmery/credentials/<account>.json     dir 0700, files 0600
//
// The file is the SAME JSON shape the CLI writes, so parseCreds reads both
// without a second parser. Writes are atomic — a temp file in the same
// directory, fsync'd, then renamed — because a crash mid-write must never
// strand a half-written file or a rotated-away refresh token. Nothing here is
// ever logged: a read failure is simply "no credential for this account".

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const (
	// storeDirEnv overrides the store's base directory. It is the seam tests
	// use to keep every write inside a t.TempDir, in the same spirit as
	// securityBin and keychainCreds — no test may touch the operator's real
	// ~/.swarmery.
	storeDirEnv = "SWARMERY_CREDENTIALS_DIR"
	// storeDirMode/storeFileMode are the hygiene contract: only the owner can
	// traverse the directory or read a credential.
	storeDirMode  = 0o700
	storeFileMode = 0o600
	// storeTempPattern names the same-directory temp file an atomic write goes
	// through. The leading dot keeps a transient failure out of casual listings.
	storeTempPattern = ".credential-*.tmp"
)

// storeCreateTemp is the atomic write's temp-file seam. A package var so a test
// can force the write to fail AFTER the destination exists and prove the
// destination survives it — the property that makes a crash mid-rotation safe.
var storeCreateTemp = os.CreateTemp

// storeBaseDir resolves the store's directory: the SWARMERY_CREDENTIALS_DIR
// override when set, else ~/.swarmery/credentials. Returns "" when neither can
// be resolved, which every caller treats as "there is no store" rather than an
// error — a machine without a resolvable home simply stays on rung 1.
func storeBaseDir() string {
	if dir := strings.TrimSpace(os.Getenv(storeDirEnv)); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".swarmery", "credentials")
}

// storePath is the file holding one account's swarmery-owned credential, or ""
// when there is no store or the account key is not a safe file name.
//
// The key comes from ingest.AccountFor, which derives it from a directory name
// the OPERATOR controls, so it is validated rather than trusted: anything with a
// path separator, a "..", or a leading dot could otherwise escape the store
// directory or shadow a dotfile.
func storePath(account string) string {
	if !safeAccountKey(account) {
		return ""
	}
	base := storeBaseDir()
	if base == "" {
		return ""
	}
	return filepath.Join(base, account+".json")
}

// safeAccountKey reports whether account is usable as a bare file name.
func safeAccountKey(account string) bool {
	if account == "" || account == "." || account == ".." {
		return false
	}
	if strings.HasPrefix(account, ".") {
		return false
	}
	if strings.ContainsAny(account, `/\`) || strings.Contains(account, "..") {
		return false
	}
	return account == filepath.Base(account)
}

// storedCred is the on-disk shape. The credential itself is nested under
// "claudeAiOauth" exactly as the `claude` CLI writes it, so parseCreds handles
// both stores; `disabled` is swarmery's own per-account kill switch, a way to
// park a connected account without deleting its credential.
type storedCred struct {
	Disabled      bool     `json:"disabled,omitempty"`
	ClaudeAiOauth rawCreds `json:"claudeAiOauth"`
}

// storedCreds reads one account's swarmery-owned credential.
//
// nil means "not connected through swarmery" for EVERY reason — no store, no
// file, an unreadable file, corrupt JSON, no access token, or the per-account
// disabled flag. That is deliberate: this is the first link in the resolution
// chain, and a corrupt file must fall through to the config-dir file rather than
// hard-fail the account into an error card it cannot act on.
func storedCreds(account string) *Creds {
	path := storePath(account)
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var sc storedCred
	if err := json.Unmarshal(raw, &sc); err == nil && sc.Disabled {
		return nil
	}
	c := parseCreds(raw)
	if c == nil {
		return nil
	}
	c.FromStore = true
	return c
}

// writeStoredCreds persists one account's credential ATOMICALLY: a temp file in
// the same directory (so the rename cannot cross a filesystem boundary), fsync'd
// so the bytes are durable before the rename publishes them, then renamed over
// the destination. A crash at any point leaves either the previous credential or
// the new one — never a truncated file, and never a file whose refresh token has
// already been rotated away upstream (R2).
func writeStoredCreds(account string, c *Creds) error {
	path := storePath(account)
	if path == "" {
		return errNoStore
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, storeDirMode); err != nil {
		return err
	}
	body, err := json.MarshalIndent(storedCred{ClaudeAiOauth: rawCreds{
		AccessToken:      c.AccessToken,
		RefreshToken:     c.RefreshToken,
		ExpiresAt:        c.ExpiresAt,
		Scopes:           c.Scopes,
		SubscriptionType: c.SubscriptionType,
		RateLimitTier:    c.RateLimitTier,
	}}, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := storeCreateTemp(dir, storeTempPattern)
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Any failure from here on removes the temp file, so a store directory can
	// never accumulate half-written credentials.
	cleanup := func(e error) error {
		tmp.Close()
		os.Remove(tmpName)
		return e
	}
	// Chmod before the content lands: CreateTemp makes 0600 already, but on a
	// permissive umask an implementation change must not silently widen it.
	if err := tmp.Chmod(storeFileMode); err != nil {
		return cleanup(err)
	}
	if _, err := tmp.Write(body); err != nil {
		return cleanup(err)
	}
	if err := tmp.Sync(); err != nil {
		return cleanup(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// deleteStoredCreds removes one account's swarmery-owned credential — the whole
// of what disconnecting means on disk.
//
// It touches THIS store and nothing else. The CLI's own credential file and the
// macOS keychain item are the CLI's (see the ownership note at the top of this
// file); a disconnect that reached into them would take the operator's terminal
// login down from a dashboard button.
//
// An already-absent file is success, not an error: disconnect is idempotent, and
// an account that was never connected is already in the state being asked for.
// The error, when there is one, is returned unwrapped for the caller to classify
// — but it carries the store path, so no caller may put it in a response.
func deleteStoredCreds(account string) error {
	path := storePath(account)
	if path == "" {
		return errNoStore
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
