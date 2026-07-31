// One provider card in the Usage modal: glyph + name + plan chip + status
// badge, an error block for a provider that failed, and its window rows.
//
// A provider whose fetch failed degrades to THIS card (no rows) rather than
// taking the whole modal down — that is why UsageProvider carries a per-provider
// `status`/`error` instead of one top-level error. A card that failed because
// the operator has not connected the provider yet renders the daemon's setup
// hint (UsageSetupHint) instead of a red error line.
//
// One account contributes one live Claude card (plus, on the default account,
// the optional telemetry-estimate card). The account label is rendered only when
// the payload has more than one account, so the single-subscription card is
// unchanged.
//
// NOT PORTED from the reference implementation: provider drag-reorder (and its
// persisted order). Cards render in server order — accounts in root order, and
// within an account the live card before the estimate — which is already the
// order that means something.

import type { UsageProvider } from '../../api/types';
import { UsageConnect } from './UsageConnect';
import { UsageSetupHint } from './UsageSetupHint';
import { UsageWindowRow } from './UsageWindowRow';

/**
 * Whether this card can offer the daemon's own OAuth connection (UsageConnect).
 *
 * Only a LIVE-quota card that is waiting on a login: the telemetry-estimate card
 * has no credential to connect, an `ok` card is already connected, and a card
 * that is opted out (SWARMERY_USAGE_OAUTH=0) or missing a scope needs a
 * different fix than a fresh authorization. Anything else keeps the card exactly
 * as it rendered before this existed.
 */
function canConnect(p: UsageProvider): boolean {
  return p.source === 'oauth' && p.status === 'no-auth' && p.hint?.kind === 'login';
}

/**
 * The badge distinguishes the three states the operator can act on differently:
 * red `error` = the provider is broken and there is nothing to do but retry;
 * amber `setup needed` = the operator can fix it right now (the hint says how);
 * neutral `disabled`/`not connected` = switched off, or off with no guidance.
 */
function StatusBadge({ p }: { p: UsageProvider }): JSX.Element | null {
  if (p.status === 'ok') return null;

  let label = 'not connected';
  let tone = 'bg-field text-ink-dim';
  if (p.status === 'error') {
    label = 'error';
    tone = 'bg-red/10 text-red';
  } else if (p.hint?.kind === 'opted-out') {
    label = 'disabled';
  } else if (p.hint !== undefined) {
    label = 'setup needed';
    tone = 'bg-amber/10 text-amber';
  }

  return (
    <span
      className={`shrink-0 rounded-full px-1.5 py-0.5 font-mono text-[9px] tracking-[0.12em] whitespace-nowrap uppercase ${tone}`}
    >
      {label}
    </span>
  );
}

export function UsageProviderCard({
  p,
  showAccount,
  mode,
  hidden,
  onToggleWindow,
  onShowAllHidden,
  now,
}: {
  p: UsageProvider;
  /**
   * Render the account this card belongs to. Set only when the payload carries
   * more than one account: on a single-subscription machine the account is
   * ambient information, and showing it would add a chip to every card for no
   * added meaning.
   */
  showAccount: boolean;
  mode: 'used' | 'remaining';
  /** Window keys the operator collapsed, across all providers. */
  hidden: string[];
  onToggleWindow: (key: string) => void;
  onShowAllHidden: () => void;
  now: number;
}): JSX.Element {
  const hiddenCount = p.windows.filter((w) => hidden.includes(w.key)).length;

  return (
    <div
      className="rounded-xl border border-line bg-surface2/40 px-2.5 py-2.5"
      data-provider={p.name}
      data-account={p.account}
      data-status={p.status}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="flex min-w-0 items-center gap-1.5">
          <span aria-hidden="true" className="shrink-0 font-mono text-[11px] text-brand">
            ◆
          </span>
          <span className="min-w-0 truncate font-mono text-[12px] font-semibold text-ink">
            {p.name}
          </span>
          {showAccount && (
            <span
              className="shrink-0 rounded-full bg-field px-1.5 py-0.5 font-mono text-[9.5px] whitespace-nowrap text-ink-dim"
              data-tip="subscription account"
            >
              {p.account}
            </span>
          )}
          {p.plan !== undefined && (
            <span className="shrink-0 rounded-full border border-line px-1.5 py-0.5 font-mono text-[9.5px] whitespace-nowrap text-ink-dim">
              {p.plan}
            </span>
          )}
        </span>
        <span className="flex shrink-0 items-center gap-1.5">
          {hiddenCount > 0 && (
            <button
              type="button"
              onClick={onShowAllHidden}
              className="rounded-[6px] border border-line px-1.5 py-0.5 font-mono text-[9.5px] whitespace-nowrap text-ink-dim transition-colors hover:bg-surface2 hover:text-ink focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-brand"
            >
              show hidden ({hiddenCount})
            </button>
          )}
          <StatusBadge p={p} />
        </span>
      </div>

      {/* A hint means "not connected yet", not "broken": it supersedes the raw
          error line, which is the same fact in a form the operator cannot act
          on. The red block is kept for genuine provider failures — a 429, a
          non-200, an unparseable payload. */}
      {p.hint !== undefined ? (
        <UsageSetupHint hint={p.hint} />
      ) : (
        p.error !== undefined && (
          <div className="mt-2 border-l-2 border-red bg-red/8 px-2 py-1.5 font-mono text-[10.5px] leading-snug break-words text-red">
            {p.error}
          </div>
        )
      )}

      {/* The one-click alternative to the hint's CLI command, and the ONLY route
          for an account whose credential the daemon cannot read at all — the
          normal state of a non-default account on macOS. */}
      {canConnect(p) && <UsageConnect account={p.account} />}

      {p.windows.length > 0 ? (
        <div className="mt-2 flex flex-col gap-2">
          {p.windows.map((w) => (
            <UsageWindowRow
              key={w.key}
              w={w}
              mode={mode}
              hidden={hidden.includes(w.key)}
              onToggleHidden={() => onToggleWindow(w.key)}
              now={now}
            />
          ))}
        </div>
      ) : (
        p.status === 'ok' && (
          <div className="mt-2 font-mono text-[10.5px] text-ink-dim">No usage data available</div>
        )
      )}
    </div>
  );
}
