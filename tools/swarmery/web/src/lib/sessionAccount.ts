// Per-subscription account dimension (migration 0047). One machine can run
// several Claude Code subscriptions side by side (CLAUDE_CONFIG_DIR) and the
// daemon stamps every session with the config dir its transcript came from.
//
// One display rule, applied everywhere: the DEFAULT account is INVISIBLE. Most
// machines run exactly one subscription, and a badge reading "default" on every
// row of every list is noise. So nothing renders for it — a single-account user
// sees no account UI at all, and the moment a second subscription appears its
// rows (and only its rows) start carrying a badge.

import type { Session } from '../api/types';

/** Server key and display key of the stock ~/.claude subscription. The daemon
 * stores '' for rows it could not attribute (ingested before the column
 * existed, or minted by the hooks channel, which has no config dir to derive
 * from) and treats '' and 'default' as the same account — so does this. */
export const DEFAULT_ACCOUNT = 'default';

/** Grouping/filter key of a session — never '': un-stamped rows join the
 * default account rather than forming a nameless bucket of their own. */
export function accountKey(session: Session): string {
  const raw = session.account;
  return raw === undefined || raw === '' ? DEFAULT_ACCOUNT : raw;
}

/** The account worth SHOWING for a session, or null when it is the default. */
export function accountLabel(session: Session): string | null {
  const key = accountKey(session);
  return key === DEFAULT_ACCOUNT ? null : key;
}

/** Distinct account keys across a loaded list, default first then alphabetical
 * — the order the filter control renders them in. A result of length ≤ 1 means
 * the list spans one subscription and the control must not render at all. */
export function accountKeys(sessions: readonly Session[]): string[] {
  const keys = [...new Set(sessions.map(accountKey))].sort((a, b) => {
    if (a === DEFAULT_ACCOUNT) return -1;
    if (b === DEFAULT_ACCOUNT) return 1;
    return a.localeCompare(b);
  });
  return keys;
}
