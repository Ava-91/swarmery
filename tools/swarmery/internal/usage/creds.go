package usage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// Sentinel outcomes of LoadCreds. Neither is a failure the caller should log
// as an error: ErrNoCreds means "the operator has not logged in with the
// `claude` CLI", ErrDisabled means "the operator opted out".
var (
	// ErrNoCreds means no Claude credential was found in any source.
	ErrNoCreds = errors.New("usage: no Claude credentials found")
	// ErrDisabled means SWARMERY_USAGE_OAUTH=0 — the credential read is
	// switched off and nothing on disk or in the keychain was touched.
	ErrDisabled = errors.New("usage: OAuth usage lookup disabled (SWARMERY_USAGE_OAUTH=0)")
)

const (
	// oauthOptOutEnv disables the credential read entirely when set to "0".
	oauthOptOutEnv = "SWARMERY_USAGE_OAUTH"
	// configDirEnv mirrors the `claude` CLI's own credential-location override.
	configDirEnv = "CLAUDE_CONFIG_DIR"
	// credentialsFile is the CLI's credential file name in every source dir.
	credentialsFile = ".credentials.json"
	// keychainService is the macOS login-keychain item the CLI writes.
	keychainService = "Claude Code-credentials"
	// keychainTimeout bounds the `security` invocation so a keychain prompt
	// can never wedge a dashboard poll.
	keychainTimeout = 5 * time.Second
)

// Creds is the operator's Claude OAuth credential. It never leaves this
// package: it is not persisted, not logged, and not serialized into any API
// response. See the package doc's policy note.
type Creds struct {
	AccessToken      string
	RefreshToken     string
	ExpiresAt        int64 // unix millis; 0 = unknown
	Scopes           []string
	SubscriptionType string
	RateLimitTier    string
}

// String redacts. This is the whole point: an accidental %v, %s or %+v on a
// Creds (or *Creds) anywhere in the tree prints a constant, never a token.
func (c Creds) String() string { return "usage.Creds{redacted}" }

// securityBin is the macOS keychain reader binary. A package var only so tests
// can point it at a stub script and never touch the real login keychain.
var securityBin = "security"

// keychainCreds is a seam over readKeychainCreds so tests can guarantee no
// `security` invocation (and therefore no keychain prompt) ever happens.
var keychainCreds = readKeychainCreds

// LoadCreds resolves the operator's Claude OAuth credential, first hit wins:
//
//  1. $CLAUDE_CONFIG_DIR/.credentials.json (when CLAUDE_CONFIG_DIR is set)
//  2. ~/.claude/.credentials.json
//  3. ~/.config/claude/.credentials.json
//  4. macOS only: `security find-generic-password -s "Claude Code-credentials" -w`
//
// Every individual failure is silent — a missing or unreadable source is just
// the next candidate. Exhausting all sources returns ErrNoCreds; opting out
// with SWARMERY_USAGE_OAUTH=0 returns ErrDisabled before any source is touched.
func LoadCreds(ctx context.Context) (*Creds, error) {
	if os.Getenv(oauthOptOutEnv) == "0" {
		return nil, ErrDisabled
	}
	for _, path := range credentialPaths() {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if c := parseCreds(raw); c != nil {
			return c, nil
		}
	}
	if runtime.GOOS == "darwin" {
		if c := keychainCreds(ctx); c != nil {
			return c, nil
		}
	}
	return nil, ErrNoCreds
}

// credentialPaths lists the file sources in resolution order. Sources whose
// base directory cannot be resolved are simply omitted.
func credentialPaths() []string {
	var paths []string
	if dir := os.Getenv(configDirEnv); dir != "" {
		paths = append(paths, filepath.Join(dir, credentialsFile))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return paths
	}
	return append(paths,
		filepath.Join(home, ".claude", credentialsFile),
		filepath.Join(home, ".config", "claude", credentialsFile),
	)
}

// CredentialSources lists, in resolution order, the sources LoadCreds consults,
// in a form fit for an operator-facing setup hint ("looked in …"). It reports
// LOCATIONS only and never reads or reveals credential content.
func CredentialSources() []string {
	srcs := credentialPaths()
	if runtime.GOOS == "darwin" {
		srcs = append(srcs, "macOS Keychain: "+keychainService)
	}
	return srcs
}

// readKeychainCreds reads the CLI's credential item out of the macOS login
// keychain. The 5s timeout keeps a keychain prompt from wedging a poll. Callers
// are responsible for the runtime.GOOS guard (LoadCreds does it) so this stays
// directly testable against a stub binary.
func readKeychainCreds(ctx context.Context) *Creds {
	ctx, cancel := context.WithTimeout(ctx, keychainTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, securityBin, keychainArgs()...).Output()
	if err != nil {
		return nil
	}
	return parseCreds(bytes.TrimSpace(out))
}

// keychainArgs is the `security` invocation used to read the credential item.
func keychainArgs() []string {
	return []string{"find-generic-password", "-s", keychainService, "-w"}
}

// rawCreds is the credential JSON as the `claude` CLI writes it (camelCase).
type rawCreds struct {
	AccessToken      string   `json:"accessToken"`
	RefreshToken     string   `json:"refreshToken"`
	ExpiresAt        int64    `json:"expiresAt"`
	Scopes           []string `json:"scopes"`
	SubscriptionType string   `json:"subscriptionType"`
	RateLimitTier    string   `json:"rateLimitTier"`
}

func (r rawCreds) toCreds() *Creds {
	return &Creds{
		AccessToken:      r.AccessToken,
		RefreshToken:     r.RefreshToken,
		ExpiresAt:        r.ExpiresAt,
		Scopes:           r.Scopes,
		SubscriptionType: r.SubscriptionType,
		RateLimitTier:    r.RateLimitTier,
	}
}

// parseCreds accepts both shapes the CLI has shipped: the credential nested
// under "claudeAiOauth", and a bare credential object at the root (Fusion's
// `creds?.claudeAiOauth || creds`, usage.ts:974). Anything without an access
// token is treated as absent, not as an error.
func parseCreds(raw []byte) *Creds {
	var wrapper struct {
		ClaudeAiOauth *rawCreds `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(raw, &wrapper); err == nil &&
		wrapper.ClaudeAiOauth != nil && wrapper.ClaudeAiOauth.AccessToken != "" {
		return wrapper.ClaudeAiOauth.toCreds()
	}
	var bare rawCreds
	if err := json.Unmarshal(raw, &bare); err == nil && bare.AccessToken != "" {
		return bare.toCreds()
	}
	return nil
}
