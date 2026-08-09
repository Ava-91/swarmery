// The account the CURRENT project scope actually runs under — the per-project
// binding resolved by GET /api/projects/{id}/account (internal/api/accounts.go).
//
// Scope, not route: both shells (fleet App + project WorkspaceShell) share one
// ScopeContext, and the workspace layout pushes its :slug into it, so reading
// `scopeProject` here gives the same answer in either header. No scope (all
// projects) has no single active account, so the hook answers null and the
// usage surfaces render exactly as before.
//
// The binding is a tiny local settings-file read on the daemon side; it is
// re-fetched when the scope changes and each time the modal opens, so a binding
// changed on the Settings page is picked up without a page reload.

import { useEffect, useState } from 'react';
import { fetchProjectAccount } from '../api';
import { useScope } from './scope';

export interface ActiveAccount {
  /** The account the scoped project runs under (binding, or the default). */
  account: string;
  /** 'binding' = explicitly pinned in the project's settings; 'default' = no
   *  binding, the project implicitly runs under the default account. */
  source: 'binding' | 'default';
  /** Display name of the scoped project the account speaks for. */
  project: string;
}

/**
 * The scoped project's effective account, or null when unscoped / the lookup
 * failed (a failed lookup must degrade to the unmarked modal, never block it).
 * `open` re-triggers the fetch on the modal's open edge so the label cannot go
 * stale while the popover is in use.
 */
export function useActiveUsageAccount(open: boolean): ActiveAccount | null {
  const { scopeProject } = useScope();
  const [active, setActive] = useState<ActiveAccount | null>(null);

  const projectId = scopeProject?.id ?? null;
  const projectName = scopeProject?.name ?? scopeProject?.slug ?? '';

  useEffect(() => {
    if (projectId === null) {
      setActive(null);
      return undefined;
    }
    let alive = true;
    fetchProjectAccount(projectId)
      .then((b) => {
        if (alive) setActive({ account: b.effective, source: b.source, project: projectName });
      })
      .catch(() => {
        if (alive) setActive(null);
      });
    return () => {
      alive = false;
    };
  }, [projectId, projectName, open]);

  return active;
}
