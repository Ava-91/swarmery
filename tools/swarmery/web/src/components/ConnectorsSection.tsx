// Connectors — the MCP servers Claude Code has configured, read from the
// daemon's `claude mcp list`. Read-only in v1: `claude mcp add/remove` mutates
// the operator's real terminal configuration, and a mis-click there removes a
// server their own CLI depends on, so no add/remove control is offered here.
//
// Self-fetching (one endpoint, one consumer — a shared context would be
// ceremony). Five states: loading, unavailable, error, empty, list. A 503 is
// NOT an error: it means the host cannot serve connectors at all (reader
// unattached, or the claude CLI unfindable from the daemon's PATH), so it
// degrades to a muted card carrying the daemon's own hint instead of a red box.
//
// The footnote is load-bearing, not decoration: this list is the DEFAULT Claude
// account only, and the daemon reads from its own working directory, so
// project-/local-scope servers configured in the operator's repos never appear.
// Silently omitting them would read as a bug.

import { useEffect, useState } from 'react';
import { ConnectorsUnavailableError, fetchConnectors } from '../api';
import type { Connector, ConnectorStatus } from '../api/types';
import { Empty, ErrorBox, Loading } from './ui';

type State =
  | { kind: 'loading' }
  | { kind: 'unavailable'; message: string; hint: string | null }
  | { kind: 'error'; message: string }
  | { kind: 'ready'; connectors: Connector[] };

/** Status → dot tone, mirroring the daemon dot on the Settings page. */
const STATUS_DOT: Record<ConnectorStatus, string> = {
  connected: 'bg-green',
  failed: 'bg-red',
  needs_auth: 'bg-amber',
  pending: 'bg-amber',
  disabled: 'bg-line',
  unknown: 'bg-line',
};

/** Hairline pill for transport / scope / source. */
function Chip({ children, className = '' }: { children: string; className?: string }): JSX.Element {
  return (
    <span
      className={`shrink-0 rounded-full border border-line px-1.5 py-0.5 font-mono text-[9.5px] whitespace-nowrap text-ink-dim ${className}`}
    >
      {children}
    </span>
  );
}

function ConnectorRow({ c }: { c: Connector }): JSX.Element {
  return (
    <div
      className="flex flex-wrap items-center gap-x-2 gap-y-1.5 px-3.5 py-2.5"
      data-status={c.status}
    >
      {/* Status is never colour-only: the dot is decorative, the word is read
          by assistive tech and revealed on hover. */}
      <span className="flex shrink-0 items-center" title={`status: ${c.status}`}>
        <span
          aria-hidden="true"
          className={`inline-block h-[7px] w-[7px] rounded-full ${STATUS_DOT[c.status]}`}
        />
        <span className="sr-only">status: {c.status}</span>
      </span>

      <span className="shrink-0 font-mono text-[12px] text-ink">{c.name}</span>

      {/* The CLI omits the type for some servers (claude.ai config), and the DTO
          carries that through as ''. An em dash, never a blank chip. */}
      <Chip>{c.transport === '' ? '—' : c.transport}</Chip>
      <Chip>{c.scope}</Chip>

      <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-ink-dim" title={c.detail}>
        {c.detail}
      </span>

      {/* Where the row came from. Today always the CLI listing; a future
          file-sourced inventory slots in as another value with no UI change. */}
      <Chip className="ml-auto">{c.source}</Chip>
    </div>
  );
}

export function ConnectorsSection(): JSX.Element {
  const [state, setState] = useState<State>({ kind: 'loading' });
  const [retry, setRetry] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setState({ kind: 'loading' });

    void fetchConnectors()
      .then((resp) => {
        if (!cancelled) setState({ kind: 'ready', connectors: resp.connectors });
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        if (err instanceof ConnectorsUnavailableError) {
          setState({ kind: 'unavailable', message: err.message, hint: err.hint });
          return;
        }
        setState({
          kind: 'error',
          message: err instanceof Error ? err.message : 'could not read mcp config',
        });
      });

    return () => {
      cancelled = true;
    };
  }, [retry]);

  return (
    <>
      {state.kind === 'loading' && <Loading label="reading mcp config…" />}

      {/* Unavailable — the host cannot serve connectors. Muted and dashed, the
          same shape as the auto-approve note above: informational, not broken. */}
      {state.kind === 'unavailable' && (
        <div className="rounded-xl border border-dashed border-line px-3.5 py-4 font-mono text-[11.5px] text-ink-dim">
          <div>{state.message}</div>
          {state.hint !== null && <div className="mt-1.5 text-ink-faint">{state.hint}</div>}
        </div>
      )}

      {state.kind === 'error' && (
        <ErrorBox
          message={state.message}
          onRetry={() => {
            setRetry((n) => n + 1);
          }}
        />
      )}

      {state.kind === 'ready' && state.connectors.length === 0 && (
        <Empty>no mcp servers configured</Empty>
      )}

      {state.kind === 'ready' && state.connectors.length > 0 && (
        <div className="divide-y divide-line rounded-xl border border-line bg-surface">
          {state.connectors.map((c) => (
            <ConnectorRow key={`${c.source}:${c.name}`} c={c} />
          ))}
        </div>
      )}

      {state.kind !== 'loading' && (
        <p className="mt-2 font-mono text-[10px] text-ink-dim">
          read-only · from `claude mcp list` on the default Claude account · project- and local-scope
          servers configured in your repos are not listed (the daemon reads from its own working
          directory)
        </p>
      )}
    </>
  );
}
