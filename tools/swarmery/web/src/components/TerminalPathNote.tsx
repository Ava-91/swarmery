// The terminal half of account readiness (SC-9): a connected account governs
// what swarmery DISPATCHES — runs and the dashboard terminal — but a `claude`
// the operator starts by hand reads ~/.claude unless CLAUDE_CONFIG_DIR is set
// in that shell. This note names the CLI that closes the gap. The commands are
// the documented literals from cmd/swarmery/account.go's help text (there is
// no daemon endpoint serving them: which|use|clear|env|exec deliberately never
// contact the daemon, so the strings cannot be fetched, only quoted).

const LINES: readonly { cmd: string; what: string }[] = [
  { cmd: 'swarmery account which', what: 'which account this project runs under, and why' },
  { cmd: 'swarmery account exec -- claude', what: 'run claude under it, one-off' },
  { cmd: 'eval "$(swarmery account env)"', what: 'or export it into the current shell' },
];

export function TerminalPathNote(): JSX.Element {
  return (
    <div className="mt-2 rounded-lg border border-line bg-bg/40 px-2.5 py-2">
      <p className="font-mono text-[10px] leading-relaxed text-ink-dim">
        Dispatched runs and the dashboard terminal now use this account. A terminal you open
        yourself still runs the machine default — from the project root:
      </p>
      <dl className="mt-1.5 space-y-0.5">
        {LINES.map((l) => (
          <div key={l.cmd} className="flex flex-wrap items-baseline gap-x-2">
            <dt className="font-mono text-[10px] whitespace-nowrap text-ink-2">
              <code>{l.cmd}</code>
            </dt>
            <dd className="font-mono text-[9.5px] text-ink-faint">{l.what}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}
