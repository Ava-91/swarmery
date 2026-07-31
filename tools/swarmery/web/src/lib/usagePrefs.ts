// Per-operator preferences for the Usage modal — display mode (Used vs
// Remaining) and the set of collapsed quota windows — over one localStorage
// key. Same shape as lib/notifications.ts: a typed read/write pair, every
// storage access wrapped in try/catch so private-browsing mode (where
// localStorage getters THROW rather than return null) degrades to defaults
// instead of taking the header down with it.
//
// GLOBAL, not per-project: quota is an account-level fact, so a hidden window
// stays hidden regardless of which project the operator is looking at.
// lib/lastProject.ts is this app's only precedent for project-scoped storage
// and it does not apply here — the reference implementation's project-scoped
// `getScopedItem` is deliberately NOT ported.
//
// NOT PORTED from the reference implementation, both cost without benefit here:
//   · resizable-popover size persistence (`kb-usage-modal-size`) — this modal is
//     a fixed 380px popover / max-w-md sheet, so there is no size to remember;
//   · provider drag-order persistence (`kb-usage-provider-order`) — at most two
//     providers render, in server order (see UsageProviderCard's own note).

import type { UsageProvider } from '../api/types';

const KEY = 'swarmery.usage.prefs';

export type UsageMode = 'used' | 'remaining';

export interface UsagePrefs {
  mode: UsageMode;
  /**
   * Hidden window keys per CARD IDENTITY (`${account}:${name}` — see
   * providerIdentity), keyed on the server-supplied `window.key` — stable
   * across refreshes, unlike the label (which is localised/renamed) or the
   * array index (which shifts when the daemon adds a per-model window). That
   * choice is what lets this module stay short instead of carrying the
   * reference implementation's label-versus-index migration shims
   * (`getWindowIdentity` / `matchesHiddenWindowEntry`).
   */
  hidden: Record<string, string[]>;
}

/** The account key the daemon reports for the stock ~/.claude subscription. */
const DEFAULT_ACCOUNT = 'default';

/**
 * A card's identity across the UI. The provider NAME alone is not unique once
 * the daemon reads more than one account: every account contributes a card
 * called "Claude", so hiding a window on one would hide it on all of them.
 */
export function providerIdentity(p: Pick<UsageProvider, 'account' | 'name'>): string {
  return `${p.account}:${p.name}`;
}

export const DEFAULT_USAGE_PREFS: UsagePrefs = { mode: 'used', hidden: {} };

function isMode(v: unknown): v is UsageMode {
  return v === 'used' || v === 'remaining';
}

/**
 * Accept only `Record<string, string[]>`; anything else degrades to `{}`.
 *
 * Keys stored before the account dimension existed are bare provider names
 * ("Claude"). They were written on a single-account machine, so they mean the
 * default account and are migrated to `default:Claude` on read — an operator's
 * collapsed windows must not silently reappear on upgrade. Migrated and native
 * keys are unioned in case both are present.
 */
function parseHidden(v: unknown): Record<string, string[]> {
  if (typeof v !== 'object' || v === null || Array.isArray(v)) return {};
  const out: Record<string, string[]> = {};
  for (const [stored, keys] of Object.entries(v as Record<string, unknown>)) {
    if (!Array.isArray(keys)) continue;
    const clean = keys.filter((k): k is string => typeof k === 'string');
    if (clean.length === 0) continue;
    const id = stored.includes(':') ? stored : `${DEFAULT_ACCOUNT}:${stored}`;
    const merged = new Set([...(out[id] ?? []), ...clean]);
    out[id] = [...merged];
  }
  return out;
}

/**
 * Stored prefs, or the defaults for absent/blocked/malformed storage.
 *
 * Never throws: a hand-edited `swarmery.usage.prefs` of `"{{{"` (JSON.parse
 * throws), of `"null"`, or of `"[1,2]"` all land on the defaults.
 */
export function readUsagePrefs(): UsagePrefs {
  try {
    const raw = localStorage.getItem(KEY);
    if (raw === null) return DEFAULT_USAGE_PREFS;
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
      return DEFAULT_USAGE_PREFS;
    }
    const rec = parsed as Record<string, unknown>;
    return {
      mode: isMode(rec.mode) ? rec.mode : DEFAULT_USAGE_PREFS.mode,
      hidden: parseHidden(rec.hidden),
    };
  } catch {
    return DEFAULT_USAGE_PREFS; // storage blocked or JSON malformed
  }
}

export function writeUsagePrefs(prefs: UsagePrefs): void {
  try {
    localStorage.setItem(KEY, JSON.stringify(prefs));
  } catch {
    // storage blocked/full — prefs stay in-memory for this tab
  }
}

/**
 * Drop hidden keys that no longer match a live window, so a window the daemon
 * stopped reporting cannot linger in storage and inflate `show hidden (N)`.
 *
 * Cards absent from the payload are left ALONE: a card missing because it is
 * momentarily erroring — or because its ACCOUNT is not in this payload (the
 * operator unset SWARMERY_PROJECTS_ROOTS, or a config dir is temporarily gone)
 * — must not silently lose the operator's choices. Only a card that IS present
 * gets its key list intersected with reality.
 *
 * `providers` is every card across every account, matched on `${account}:${name}`.
 *
 * Returns the same object identity when nothing was pruned, so callers can use
 * it as a cheap "did anything change?" test and skip a redundant write.
 */
export function pruneHiddenPrefs(
  hidden: Record<string, string[]>,
  providers: readonly UsageProvider[],
): Record<string, string[]> {
  let changed = false;
  const out: Record<string, string[]> = {};
  for (const [id, keys] of Object.entries(hidden)) {
    const provider = providers.find((p) => providerIdentity(p) === id);
    if (provider === undefined) {
      out[id] = keys; // card not in this payload — preserve as-is
      continue;
    }
    const live = keys.filter((k) => provider.windows.some((w) => w.key === k));
    if (live.length !== keys.length) changed = true;
    if (live.length > 0) out[id] = live;
  }
  return changed ? out : hidden;
}
