// Agent picker: a <select> of the registry agents a board task may dispatch as,
// shared by NewTaskModal (create) and TaskDrawer (edit). Mirrors PlaybookPicker's
// shape — a fetch hook plus a plain, labelled <select> so the control is
// keyboard-native (WCAG).
//
// The roster comes from GET /api/agents/hub, which does NO scope filtering; the
// server's own validation (resolveAgentName in internal/api/tasks_board.go)
// accepts only a global agent or one scoped to THIS project, so the same
// predicate is applied here — otherwise the picker would offer names the POST
// then rejects with "unknown agent". Same-named rows across scopes fold to one
// option (the project-scoped row wins, as it is the definition that overrides).

import { useEffect, useState } from 'react';
import type { AgentRosterRow } from '../api/types';
import { fetchAgentRoster } from '../api/agentHub';

/** Roster entries selectable for a project, name-sorted and de-duplicated. */
export function useAgentRoster(
  projectId: number | null,
  projectSlug: string | null,
): { agents: AgentRosterRow[]; loading: boolean } {
  const [agents, setAgents] = useState<AgentRosterRow[]>([]);
  const [loading, setLoading] = useState(true);
  useEffect(() => {
    if (projectId === null) {
      setAgents([]);
      setLoading(false);
      return;
    }
    let disposed = false;
    setLoading(true);
    fetchAgentRoster(String(projectId))
      .then((resp) => {
        if (disposed) return;
        setAgents(selectableAgents(resp.agents, projectSlug));
      })
      .catch(() => {
        if (!disposed) setAgents([]);
      })
      .finally(() => {
        if (!disposed) setLoading(false);
      });
    return () => {
      disposed = true;
    };
  }, [projectId, projectSlug]);
  return { agents, loading };
}

/** Pure part of the hook: scope filter + fold-by-name + sort. Exported for tests. */
export function selectableAgents(rows: AgentRosterRow[], projectSlug: string | null): AgentRosterRow[] {
  const byName = new Map<string, AgentRosterRow>();
  for (const a of rows) {
    if (a.scope !== 'global' && a.projectSlug !== projectSlug) continue;
    const seen = byName.get(a.name);
    // A project-scoped definition overrides a global one of the same name.
    if (seen === undefined || (seen.scope === 'global' && a.scope === 'project')) byName.set(a.name, a);
  }
  return [...byName.values()].sort((a, b) => a.name.localeCompare(b.name));
}

/**
 * A <select> bound to an agent name. `value` is the selected name ("" = no
 * agent, a plain run); `onChange` receives the new name ("" when "none" is
 * picked). Renders an unknown stored value as its own option so an agent whose
 * file has since gone does not silently reset the field.
 */
export function AgentSelect({
  agents,
  value,
  onChange,
  disabled = false,
  id,
}: {
  agents: AgentRosterRow[];
  value: string;
  onChange: (name: string) => void;
  disabled?: boolean;
  id?: string;
}): JSX.Element {
  const known = agents.some((a) => a.name === value);
  return (
    <select
      id={id}
      value={value}
      disabled={disabled}
      aria-label="agent"
      onChange={(e) => onChange(e.target.value)}
      className="w-full rounded-[8px] border border-line bg-field px-2 py-1.5 font-mono text-[11px] text-ink outline-none transition-colors hover:border-line-strong focus:border-ink-dim disabled:opacity-50"
    >
      <option value="">none (plain run)</option>
      {value !== '' && !known && <option value={value}>@{value} (unknown)</option>}
      {agents.map((a) => (
        <option key={`${a.name}:${String(a.id)}`} value={a.name}>
          @{a.name} · {a.scope}
        </option>
      ))}
    </select>
  );
}

/** Scope chip + one-line description of the selected agent (null when none). */
export function AgentHint({
  agents,
  value,
}: {
  agents: AgentRosterRow[];
  value: string;
}): JSX.Element | null {
  if (value === '') return null;
  const agent = agents.find((a) => a.name === value);
  if (agent === undefined) return null;
  return (
    <div className="mt-1 flex items-start gap-1.5">
      <span
        className="shrink-0 rounded-full border border-line px-1.5 py-[1px] font-mono text-[9px] text-ink-dim uppercase"
        data-tip={agent.scope === 'global' ? 'available to every project' : 'defined by this project'}
      >
        {agent.scope}
      </span>
      {agent.description !== null && (
        <span className="font-mono text-[10px] leading-snug text-ink-faint">{agent.description}</span>
      )}
    </div>
  );
}
