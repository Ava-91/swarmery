// The header's live usage chip — `◔ 28%` — and the owner of the Usage modal's
// open state. Both shells (fleet App + project WorkspaceShell) render just
// `<UsageChip />`; the chip carries its own `relative` wrapper because that is
// the positioning context the ≥768px popover variant of UsageModal anchors to.
//
// Data comes from the shared poller (lib/usageData.tsx), never from a fetch of
// its own — the chip and the modal must not be two pollers.
//
// Chrome is byte-identical to the plain button it replaces (and to ThemeToggle's
// neighbouring style) so swapping the label for a percentage cannot shift the
// header's height or horizontal rhythm.

import { useEffect, useState } from 'react';
import type { UsageProvider, UsageWindow } from '../../api/types';
import { useUsage } from '../../lib/usageData';
import { fmtResetsIn } from './format';
import { UsageModal } from './UsageModal';

const NO_AUTH_TIP = 'Claude usage unavailable — run `claude` to log in';

/**
 * Tip for a payload with nothing but not-connected providers. The daemon's own
 * hint headline is preferred when there is one — "Claude login expired" is the
 * fact the operator needs, and it differs per cause (expired, rejected, missing
 * scope, switched off) where the generic line does not.
 */
function noAuthTip(providers: readonly UsageProvider[]): string {
  const hint = providers.find((p) => p.hint !== undefined)?.hint;
  if (hint === undefined) return NO_AUTH_TIP;
  const fix =
    hint.command !== undefined && hint.command !== '' ? `run \`${hint.command}\`` : 'open usage';
  return `${hint.title} — ${fix}`;
}

/**
 * The window the chip speaks for: the session window of the first healthy
 * provider, falling back to that provider's first window (a payload with only a
 * weekly window is still worth showing), then to nothing.
 */
function pickWindow(providers: readonly UsageProvider[]): UsageWindow | null {
  const healthy = providers.find((p) => p.status === 'ok');
  if (healthy === undefined) return null;
  return healthy.windows.find((w) => w.key.startsWith('session')) ?? healthy.windows[0] ?? null;
}

/**
 * Colour keyed to ACTUAL burn, with pace as an independent trigger.
 *
 * `ahead` means burning FASTER than the window elapses — over pace, the warning
 * state — so it is red even at a low percentage. Read UsageWindowRow's pace note
 * before touching this: the vocabulary is not invertible.
 */
function toneFor(w: UsageWindow): string {
  if (w.percentUsed > 90 || w.pace?.status === 'ahead') return 'text-red';
  if (w.percentUsed > 70) return 'text-amber';
  return 'text-ink-2';
}

interface ChipView {
  /** Label after the glyph — a percentage once data exists, else `usage`. */
  text: string;
  tone: string;
  tip: string;
}

function buildView(
  providers: readonly UsageProvider[],
  error: string | null,
  lastUpdated: number | null,
): ChipView {
  // Nothing has ever loaded and the fetch failed — the only genuinely blind
  // state, so the chip greys out. A failure AFTER a success keeps the last known
  // percentage instead (stale but informative) and says so in the tip, matching
  // how the modal keeps stale cards behind a "refresh failed" footer.
  if (lastUpdated === null && error !== null) {
    return { text: 'usage', tone: 'text-ink-dim', tip: `Claude usage unavailable — ${error}` };
  }
  if (lastUpdated === null) {
    return { text: 'usage', tone: 'text-ink-2', tip: 'Subscription usage — loading…' };
  }
  // No credentials: the daemon omits the estimate card entirely when
  // SWARMERY_USAGE_LIMITS is unset, so a logged-out operator sees exactly one
  // no-auth provider. `every` (not `some`) keeps a half-broken payload — one
  // healthy card, one no-auth — reporting the healthy percentage.
  if (providers.length > 0 && providers.every((p) => p.status === 'no-auth')) {
    return { text: 'usage', tone: 'text-ink-2', tip: noAuthTip(providers) };
  }

  const w = pickWindow(providers);
  if (w === null) {
    return { text: 'usage', tone: 'text-ink-2', tip: 'Subscription usage' };
  }

  const reset = w.resetText ?? (w.resetAt !== undefined ? fmtResetsIn(w.resetAt, Date.now()) : '');
  const parts = [`${w.label} — ${String(Math.round(w.percentUsed))}% used`];
  if (reset !== '') parts.push(reset);
  if (w.pace !== undefined) parts.push(w.pace.message);
  if (error !== null) parts.push('refresh failed');

  return {
    text: `${String(Math.round(w.percentUsed))}%`,
    tone: toneFor(w),
    tip: parts.join(' · '),
  };
}

export function UsageChip(): JSX.Element {
  const { providers, error, lastUpdated, setModalOpen } = useUsage();
  const [open, setOpen] = useState(false);

  // Registering the open modal is what bumps the SHARED cadence 120s → 30s; it
  // never starts a second poller. Reference-counted in the provider, so
  // StrictMode's mount/unmount/mount cycle nets out correctly.
  useEffect(() => {
    if (!open) return undefined;
    setModalOpen(true);
    return () => setModalOpen(false);
  }, [open, setModalOpen]);

  const view = buildView(providers, error, lastUpdated);

  return (
    <span className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        aria-haspopup="dialog"
        aria-label="Subscription usage"
        // `data-tip`, never `title`: native tooltips were removed app-wide in
        // favour of the themed TooltipLayer (components/Tooltip.tsx), which also
        // wires aria-describedby — the accessible NAME still comes from
        // aria-label above.
        data-tip={view.tip}
        className={`rounded-lg border border-line bg-surface px-2.5 py-1 font-mono text-[11px] font-semibold tabular-nums transition-colors hover:bg-surface2 ${view.tone}`}
      >
        <span aria-hidden="true">◔</span> {view.text}
      </button>
      <UsageModal open={open} onClose={() => setOpen(false)} />
    </span>
  );
}
