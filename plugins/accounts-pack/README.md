# accounts-pack

Bind a project to one of several Claude Code accounts and run every session in
it under that account — from the dashboard, from `/account`, and from your own
terminal. Opt-in: most people have one account and need none of this.

## What an "account" is here

One Claude Code config dir. The CLI keeps everything about a login — including
the credential — under `CLAUDE_CONFIG_DIR`, so pointing a process at another
config dir *is* switching the account. The default account is the env-LESS one:
it lives in `~/.claude`, which is where the CLI looks when `CLAUDE_CONFIG_DIR`
is unset, so a project bound to it produces an empty environment delta and
behaves exactly as it did before this pack existed.

swarmery never writes credential material. It creates directories and points
processes at them; the `claude` CLI performs each account's own login.

## Enable per project

```jsonc
"enabledPlugins": { "accounts-pack@swarmery": true }
```

Enabling the pack **does not touch your shell profile**. The only thing that
edits it is running `bin/install-shell-function.sh` yourself.

## Requires the swarmery CLI

Every decision lives in `swarmery account`, so the shell surfaces, the hooks and
the daemon cannot disagree about which account a project uses:

```
swarmery account list                              key, config dir, default?, connected?, plan
swarmery account which [--path <dir>]              the effective account, and whether it came
                                                   from a binding or from the default
swarmery account use <key> [--path <dir>]          bind a project
swarmery account clear [--path <dir>]              unbind it
swarmery account env [--path <dir>]                zero or one line: CLAUDE_CONFIG_DIR=<dir>
swarmery account exec [--path <dir>] -- <cmd ...>  run a command under the project's account
```

`which`, `use`, `clear`, `env` and `exec` never contact the daemon. Your
terminal has to keep working with swarmery stopped.

The pack degrades honestly rather than silently: without the CLI on `PATH`,
`claude-account.sh` prints one warning and runs the default account, and the
shell function falls through to plain `claude`.

## The two terminal surfaces

Pick one. They do the same thing and differ only in how much they take over.

### 1. The explicit wrapper — `bin/claude-account.sh`

```bash
claude-account.sh --resume
```

One line of shell, nothing installed, nothing shadowed: it execs
`swarmery account exec --path "${CLAUDE_PROJECT_DIR:-$PWD}" -- claude "$@"`.
The exit code is the child's. Symlink it onto your `PATH` under whatever name
you like.

### 2. The shell function — `bin/install-shell-function.sh`

```bash
bin/install-shell-function.sh              # install into ~/.zshrc or ~/.bashrc
bin/install-shell-function.sh --status
bin/install-shell-function.sh --uninstall
bin/install-shell-function.sh --profile ~/.config/shell/rc   # explicit target
```

Installs a `claude` function so that typing plain `claude` follows the binding
of the directory you are standing in. What it guarantees:

- the profile is edited **only** when you run this script — never on enable;
- the original is copied to `<profile>.bak` before the first write;
- it is idempotent — running it twice produces no second block and no diff;
- `--uninstall` removes exactly its own marker block and nothing else;
- unbalanced markers (a hand-edited profile) **abort** the run instead of
  guessing;
- the function calls `command claude`, never `claude`, so it cannot recurse;
- the fallback is silent — no CLI, no binding, or a failed lookup all fall
  through to plain `claude` with no output. This runs on every invocation, so a
  warning here would be noise forever.

Already-open shells keep the old definition until they are restarted
(`source ~/.zshrc`, or `unset -f claude` after uninstalling).

## `/account`

`commands/account.md` — list the accounts, show the effective one, switch it,
install or remove the shell function. Its body is `swarmery account …` calls; it
holds no logic of its own.

## Hooks

`hooks/hooks.json` wires `SessionStart` → `hooks/warn-wrong-account.sh`, which
warns when a session is running under an account other than the project's
binding. **That script is currently a skeleton**: it drains stdin and exits 0.
It is wired now and filled in a later phase, so the wiring can be reviewed and
proven harmless on its own. Like every hook here it is fail-open — a hook that
can fail is a hook that can block a session.

## Known edges

- **The binding is read from the project root only.** It lives in
  `<project>/.claude/settings.local.json` and is not searched for in parent
  directories, so the shell function follows the binding when your shell sits at
  the project root. Deeper down you get the default account; `claude-account.sh`
  avoids this by preferring `CLAUDE_PROJECT_DIR`.
- **A running session keeps its account.** A binding decides what the *next*
  session starts under; nothing re-homes a live one.
- **An empty delta inherits.** A project bound to the default account adds no
  variable, so a `CLAUDE_CONFIG_DIR` you exported by hand in that shell still
  applies — the same rule every other swarmery spawner follows.
- **The binding file is machine-local** (`settings.local.json`, gitignored):
  two people on one repo can legitimately use different accounts.
