// Daemon health polling (GET /api/health, every 60s), shared by the app shell
// header ("● daemon healthy") and the icon-rail footer ("v0.2.0+41157a8 · :7777").
// `unreachable` flips true when the fetch fails so the UI can go red.

import { useEffect, useState } from 'react';
import type { HealthResponse } from '../api/types';
import { fetchHealth } from '../api';

const POLL_MS = 60_000;

/**
 * Build identity for the header: the release semver plus the commit the daemon
 * was actually built from, so a rebuild is visible without a semver bump.
 *
 *   "0.2.0-15-g41157a8"       → "v0.2.0+41157a8"
 *   "0.2.0-15-g41157a8-dirty" → "v0.2.0+41157a8*"   (* = uncommitted worktree)
 *   "0.2.0+41157a8"           → "v0.2.0+41157a8"    (Go VCS-stamp fallback)
 *   "0.2.0"                   → "v0.2.0"            (built exactly on the tag)
 *
 * Falls back to `version` when talking to a daemon older than the build field.
 */
export function versionLabel(health: Pick<HealthResponse, 'version' | 'build'>): string {
  const raw = (health.build ?? health.version).trim();
  const dirty = raw.endsWith('-dirty');
  const clean = dirty ? raw.slice(0, -'-dirty'.length) : raw;
  const semver = /^\d+\.\d+\.\d+/.exec(clean)?.[0] ?? clean;
  const commit = /[-+]g?([0-9a-f]{7,40})$/.exec(clean)?.[1];
  return `v${semver}${commit ? `+${commit}` : ''}${dirty ? '*' : ''}`;
}

/** Full, untruncated build string for the `title` tooltip behind the label. */
export function versionTitle(health: Pick<HealthResponse, 'version' | 'build'>): string {
  return `swarmery ${health.build ?? health.version}`;
}

export interface HealthState {
  health: HealthResponse | null;
  unreachable: boolean;
}

export function useHealth(): HealthState {
  const [health, setHealth] = useState<HealthResponse | null>(null);
  const [unreachable, setUnreachable] = useState(false);

  useEffect(() => {
    let disposed = false;
    const poll = (): void => {
      fetchHealth()
        .then((h) => {
          if (disposed) return;
          setHealth(h);
          setUnreachable(false);
        })
        .catch(() => {
          if (!disposed) setUnreachable(true);
        });
    };
    poll();
    const timer = setInterval(poll, POLL_MS);
    return () => {
      disposed = true;
      clearInterval(timer);
    };
  }, []);

  return { health, unreachable };
}
