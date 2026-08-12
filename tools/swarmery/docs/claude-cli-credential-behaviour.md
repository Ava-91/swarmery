# Claude CLI credential behaviour — measured

**CLI version:** 2.1.220 (Claude Code), `/opt/homebrew/bin/claude`
**Date measured:** 2026-08-12 (re-run of the 2026-08-06 spike, same CLI version)
**Method:** throwaway config dirs under `mktemp -d`, deleted after measurement. No
token material, account names or operator paths are recorded here — behaviour only.

This note is the evidence base for the account-connect plan (probe classification,
write-once credential handoff, PTY fallback). Re-run these three measurements when the
installed CLI changes before trusting them again.

## 1. No-credential behaviour

```
CLAUDE_CONFIG_DIR=<tmp>/cfg claude -p 'hi'     # <tmp>/cfg exists, has no credential
```

- Exit code: **1**
- Output (on **stdout**, stderr empty): `Not logged in · Please run /login`
- The default account on this machine **was** logged in at the time, and the CLI did
  **not** fall back to it — the prompt was never answered.
- Side effect: the CLI scaffolds the dir (`.claude.json`, `projects/`, `plugins/`, …)
  even on the failed run.

**Verdict:** a non-empty `CLAUDE_CONFIG_DIR` with no credential of its own fails outright
(`Not logged in · Please run /login`, exit 1) and does NOT silently use the default
account — the config dir really is the whole account boundary. Unchanged from 2026-08-06.

## 2. Handoff acceptance

The default account's credential JSON (the `{"claudeAiOauth":{...}}` shape) was copied
into `<tmp>/cfg/.credentials.json` at mode 0600, then the same `claude -p` invocation ran.

- The CLI **authenticated**: exit 0, the prompt was answered, wall time ~3.8s.
- Afterwards `<tmp>/cfg/.credentials.json` **still existed**, byte-identical size.
- The suffixed Keychain item `Claude Code-credentials-<sha256(dir)[0:8]>`
  (derivation: `internal/usage/creds.go` `scopedKeychainService`) did **not** appear.

**Verdict:** the CLI accepts a handed-over `.credentials.json` — a write-once handoff
works. Nuance vs the 2026-08-06 note: "the store deletes `<dir>/.credentials.json` after
a successful Keychain write" did not trigger here because the handed-over token was
unexpired — no refresh, therefore no Keychain write, therefore no deletion and no
suffixed item. The deletion is tied to the CLI's store *writing* (login / token refresh),
not to mere use. The write-once rule stands: after the CLI's first refresh the file is
expected to disappear in favour of the Keychain, so swarmery must never rewrite it.

## 3. Cheapest authenticating invocation

Measured under the authed temp dir and a second, credential-less temp dir:

| Invocation | Authenticates? | Wall time | Exit (authed / no-cred) | Token cost |
|---|---|---|---|---|
| `claude auth status` | **yes** | ~0.35s | 0 / 1 | none (no inference) |
| `claude -p 'ok' --max-turns 1` | yes | ~4.1s | 0 / 1 | one short turn |
| `claude -p ''` | **no** | ~2.2s | 1 / 1 — `Error: Input must be provided…` before any auth check | none |

`claude auth status` prints JSON: `{"loggedIn": true, ...}` exit 0 when the config dir
has a login, `{"loggedIn": false, "authMethod": "none", ...}` exit 1 when it does not.
`claude -p ''` fails on argument validation on **both** dirs, so it cannot distinguish
anything.

**Verdict:** prefer `claude auth status` — sub-second, no token cost, and its exit code
alone separates ready (0) from no-login (1). `claude -p 'ok' --max-turns 1` is the
fallback for a future CLI without the subcommand.
