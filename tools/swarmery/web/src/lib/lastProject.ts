// Last-visited project slug (mode toggle): the Projects segment of the header
// ModeToggle reopens the last project the user was in. Persisted in localStorage
// under `swarmery.lastProject`, written when a project workspace resolves its
// slug (ProjectContext) and read by the ModeToggle. Guarded try/catch — private
// mode / disabled storage degrades to "no last project" (→ the Projects list),
// the same idiom as lib/scope.tsx.

const KEY = 'swarmery.lastProject';

/** The last-opened project slug, or null when none is stored / storage is off. */
export function loadLastProject(): string | null {
  try {
    const v = window.localStorage.getItem(KEY);
    return v !== null && v !== '' ? v : null;
  } catch {
    return null;
  }
}

/** Remember the last-opened project slug (no-op for an empty slug). */
export function saveLastProject(slug: string): void {
  try {
    if (slug !== '') window.localStorage.setItem(KEY, slug);
  } catch {
    // storage disabled — in-memory navigation still works for this session
  }
}
