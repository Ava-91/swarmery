// Global project scope (ScopeContext): one selected project slug (or null =
// all projects) shared by every page as its DEFAULT project filter — the
// GitHub-org-switcher pattern. The selection persists in localStorage and is
// reflected as ?scope=<slug> on the URL when it changes; on first load a URL
// param wins over the stored value so deep links work. NOTE: /system's
// component-scope query param was renamed to ?level= so the global ?scope=
// owns the name everywhere.

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';
import { useSearchParams } from 'react-router-dom';
import type { Project } from '../api/types';
import { fetchProjects } from '../api';
import { findProject } from './projectSlug';

const STORAGE_KEY = 'swarmery.scope';

interface ScopeValue {
  /** Selected project slug (pretty name slug or legacy path slug), or null =
   * all projects. Pass it verbatim to ?project= APIs — the server matches
   * slug, id, and kebab name alike. */
  scope: string | null;
  setScope: (slug: string | null) => void;
  /** Non-archived projects, fetched once here and shared by every consumer
   * (header switcher, command palette, …) — display names live on these. */
  projects: Project[];
  /** Clean display name for the current scope (never the raw path slug). */
  scopeName: string | null;
  /** Resolved project row for the scope, or null when unscoped / unknown.
   * Client-side row filters must compare against scopeProject.slug (the DB
   * path slug rows carry), never against the raw scope value. */
  scopeProject: Project | null;
}

const ScopeContext = createContext<ScopeValue>({
  scope: null,
  setScope: () => undefined,
  projects: [],
  scopeName: null,
  scopeProject: null,
});

function storedScope(): string | null {
  try {
    const v = window.localStorage.getItem(STORAGE_KEY);
    return v !== null && v !== '' ? v : null;
  } catch {
    return null; // storage disabled (private mode) → session-only scope
  }
}

export function ScopeProvider({ children }: { children: ReactNode }): JSX.Element {
  const [searchParams, setSearchParams] = useSearchParams();
  // URL wins over localStorage on first load (?scope= deep links).
  const [scope, setScopeState] = useState<string | null>(
    () => searchParams.get('scope') ?? storedScope(),
  );

  const setScope = useCallback(
    (slug: string | null): void => {
      setScopeState(slug);
      try {
        if (slug === null) window.localStorage.removeItem(STORAGE_KEY);
        else window.localStorage.setItem(STORAGE_KEY, slug);
      } catch {
        // storage disabled — the in-memory scope still applies this session
      }
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          if (slug === null) next.delete('scope');
          else next.set('scope', slug);
          return next;
        },
        { replace: true },
      );
    },
    [setSearchParams],
  );

  // Back/forward navigation that changes ?scope= re-syncs the context.
  const urlScope = searchParams.get('scope');
  useEffect(() => {
    if (urlScope !== null && urlScope !== scope) setScopeState(urlScope);
  }, [urlScope, scope]);

  const [projects, setProjects] = useState<Project[]>([]);
  useEffect(() => {
    fetchProjects()
      .then(setProjects)
      .catch(() => setProjects([])); // consumers degrade to slug labels
  }, []);

  const value = useMemo(() => {
    const selected = findProject(projects, scope);
    const scopeName = scope === null ? null : (selected?.name ?? scope);
    return { scope, setScope, projects, scopeName, scopeProject: selected };
  }, [scope, setScope, projects]);
  return <ScopeContext.Provider value={value}>{children}</ScopeContext.Provider>;
}

export function useScope(): ScopeValue {
  return useContext(ScopeContext);
}

/**
 * Resolve any project reference — the pretty name slug carried by `scope` and
 * by /p/:slug URLs, a legacy DB path slug, or a numeric id — to the NUMERIC id
 * as a string, for use as a `?projectId=` / `?project=` query value.
 *
 * Why the id and not the slug the caller already holds: the server's scope
 * matchers are NOT uniform. Most (`projectMatchExpr`, internal/api/scope.go)
 * accept slug OR id OR kebab-cased name, so the pretty slug works. But
 * `effectiveTemplates` (internal/api/system_hub.go) matches the DB path slug or
 * the numeric id only — no name clause — and a miss there is a hard error, not
 * a fallback to unscoped. Passing `swarmery` for project `-Volumes-Work-swarmery`
 * therefore 500s `{"error":"unknown template project"}` on every /system and
 * /system-hub call. The numeric id is the one form EVERY matcher accepts.
 *
 * Returns null while the project list is still loading or the key resolves to
 * nothing; callers then omit the param and show the unscoped view, which is the
 * server's own "unknown project" behaviour anyway.
 */
export function useProjectIdParam(key: string | null | undefined): string | null {
  const { projects } = useScope();
  if (key == null || key === '') return null;
  const found = findProject(projects, key);
  return found === null ? null : String(found.id);
}

/**
 * useProjectIdParam plus the one bit it cannot express: whether the id is null
 * because there is NO scope, or merely because the project list has not landed
 * yet. Any caller whose unscoped request returns a DIFFERENT (wider) result set
 * must use this and skip fetching while `pending` — otherwise the first,
 * unscoped response can resolve last and clobber the scoped one, which is how a
 * project page ends up rendering the whole fleet.
 */
export function useProjectScope(key: string | null | undefined): {
  id: string | null;
  pending: boolean;
} {
  const { projects } = useScope();
  if (key == null || key === '') return { id: null, pending: false };
  // The list is fetched once in ScopeProvider; empty means "not yet" (a machine
  // with zero projects cannot be showing a project-scoped page).
  if (projects.length === 0) return { id: null, pending: true };
  const found = findProject(projects, key);
  return { id: found === null ? null : String(found.id), pending: false };
}
