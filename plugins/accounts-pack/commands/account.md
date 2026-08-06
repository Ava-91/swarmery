---
description: Thin entry point for `/account [list|use <key>|clear|setup-shell [--uninstall]]` — shows which Claude Code account this project runs under and switches it. Every decision lives in the `swarmery account` CLI; no run logic lives here.
allowed-tools:
  - Bash
---

# /account — which account does this project run under?

## Usage

```
/account                              list the accounts and mark the one this project uses
/account use <key>                    bind this project to an account
/account clear                        drop the binding (back to the default account)
/account setup-shell [--uninstall]    install/remove the `claude` shell function in your profile
```

## What it does — and does not do

This is a **thin proxy** over the `swarmery account` CLI. It parses
`$ARGUMENTS`, picks the matching command below, and reports its output. It owns
no logic of its own: what an account is, where its config dir lives, and what
the environment delta is are all answered by the CLI, so this command and the
dashboard can never give different answers.

If a rule about accounts ever appears in this file, it leaked out of the CLI and
belongs back there.

**Requires the `swarmery` CLI on PATH.** It does **not** require the daemon:
`which`, `use`, `clear`, `env` and `exec` read and write the binding file
directly. If `swarmery` is missing, say so and stop — do not guess an account.

## Commands

### `/account` (no arguments)

```bash
swarmery account list
swarmery account which --path "${CLAUDE_PROJECT_DIR:-$PWD}"
```

Report both: the table of installed accounts (key, config dir, default?,
connected?, plan) and the account this project effectively runs under. `source:
binding` means someone chose it; `source: default` means nobody did.

`connected: unknown` is not `no` — it means credential resolution is switched
off (`SWARMERY_USAGE_OAUTH=0`), so the question was never asked.

### `/account use <key>`

```bash
swarmery account use "<key>" --path "${CLAUDE_PROJECT_DIR:-$PWD}"
```

Writes the binding to `.claude/settings.local.json` (machine-local, gitignored —
two people on one repo legitimately use different accounts). The CLI refuses a
key that is not installed on this machine; report that refusal as-is rather than
falling back to another account.

An **already-running session keeps its own account** — the binding decides what
the *next* session starts under. Say so instead of implying a live switch.

### `/account clear`

```bash
swarmery account clear --path "${CLAUDE_PROJECT_DIR:-$PWD}"
```

### `/account setup-shell [--uninstall]`

```bash
"${CLAUDE_PLUGIN_ROOT}/bin/install-shell-function.sh"              # install
"${CLAUDE_PLUGIN_ROOT}/bin/install-shell-function.sh" --uninstall  # remove
"${CLAUDE_PLUGIN_ROOT}/bin/install-shell-function.sh" --status     # check
```

This edits a **login profile** (`~/.zshrc` / `~/.bashrc`), so run it only when
the operator asked for it in this turn — never as a follow-up to `use`, and
never "while we're here". It backs the profile up to `<profile>.bak` before its
first write and is idempotent.

After installing, tell the operator the function applies to **new** shells
(`source ~/.zshrc` for the current one).

## Argument parsing

1. No arguments → the listing above.
2. `use` → requires exactly one following token, the account key. Missing key →
   print usage and stop; never pick an account for the operator.
3. `clear` → takes no further arguments.
4. `setup-shell` → optional `--uninstall` or `--status`. Any other flag → usage
   error, stop.
5. Anything else → usage error, stop.

## Related

- `plugins/accounts-pack/README.md` — the two shell surfaces and what each costs.
- `plugins/accounts-pack/bin/claude-account.sh` — the explicit wrapper, for
  operators who would rather not have a `claude` shell function.
