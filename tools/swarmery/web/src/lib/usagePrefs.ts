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
   * Hidden window keys per provider NAME, keyed on the server-supplied
   * `window.key` — stable across refreshes, unlike the label (which is
   * localised/renamed) or the array index (which shifts when the daemon adds a
   * per-model window). That choice is what lets this module stay ~60 lines
   * instead of carrying the reference implementation's label-versus-index
   * migration shims (`getWindowIdentity` / `matchesHiddenWindowEntry`).
   */
  hidden: Record<string, string[]>;
}

export const DEFAULT_USAGE_PREFS: UsagePrefs = { mode: 'used', hidden: {} };

function isMode(v: unknown): v is UsageMode {
  return v === 'used' || v === 'remaining';
}

/** Accept only `Record<string, string[]>`; anything else degrades to `{}`. */
function parseHidden(v: unknown): Record<string, string[]> {
  if (typeof v !== 'object' || v === null || Array.isArray(v)) return {};
  const out: Record<string, string[]> = {};
  for (const [provider, keys] of Object.entries(v as Record<string, unknown>)) {
    if (!Array.isArray(keys)) continue;
    const clean = keys.filter((k): k is string => typeof k === 'string');
    if (clean.length > 0) out[provider] = clean;
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
 * Providers absent from the payload are left ALONE: a provider missing because
 * it is momentarily erroring must not silently lose the operator's choices.
 * Only a provider that IS present gets its key list intersected with reality.
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
  for (const [name, keys] of Object.entries(hidden)) {
    const provider = providers.find((p) => p.name === name);
    if (provider === undefined) {
      out[name] = keys; // provider not in this payload — preserve as-is
      continue;
    }
    const live = keys.filter((k) => provider.windows.some((w) => w.key === k));
    if (live.length !== keys.length) changed = true;
    if (live.length > 0) out[name] = live;
  }
  return changed ? out : hidden;
}
