// Typed client for the swarmery REST API.
// All response types live in ./api/types.ts (frozen contract) — do not
// declare API types here.

import type {
  AdviseStats,
  AgentEvidence,
  AnalyticsDimension,
  AnalyticsMetric,
  ApprovalRule,
  AttachResponse,
  BoardColumn,
  BoardTask,
  BreakdownResp,
  AddConnectorInput,
  Connector,
  ConnectorsResponse,
  DetachResponse,
  DispatchStatus,
  AutonomyResp,
  DocDetail,
  DocMeta,
  DurationsResp,
  Epic,
  ErrorsResp,
  FileSessionsResponse,
  FunnelResp,
  HealthResponse,
  MatrixResp,
  DuplicatePlaybookResponse,
  MemoryFileContent,
  MemoryListResp,
  OnboardConfig,
  PlaybookRollup,
  ProductivityResp,
  UsageLoginStart,
  UsageResp,
  OnboardRequest,
  OnboardResponse,
  Playbook,
  PermissionEscalation,
  PhaseDiagnosis,
  PermissionPresetInput,
  PermissionPresetView,
  PermissionRequest,
  PermissionRequestStatus,
  PlanDoc,
  PlanRunMode,
  PlanningStart,
  PlanningStatus,
  ProjectDetail,
  RunConflictCode,
  ProjectMeta,
  ProjectMetaPatch,
  ProjectOverviewResp,
  ProjectPluginsResponse,
  PluginRepairResponse,
  ProjectPluginToggleResponse,
  ProjectsHealthResponse,
  ProjectsResponse,
  ProposalsResp,
  Recommendation,
  RecommendationsResp,
  RecommendationStatus,
  RetroAgentsResp,
  RetroFrictionResp,
  RetroLessonsResp,
  RetroTasksResp,
  Routine,
  RoutineInput,
  RoutineRun,
  SearchResponse,
  ContextHogsReport,
  SessionDetailResponse,
  SessionHandoffResponse,
  SessionOutcome,
  SessionsResponse,
  StatsOverview,
  StatsToday,
  TaskDetail,
  SkillsResp,
  TasksResponse,
  TimeseriesResp,
  ToolsResp,
  ToolsResponse,
} from './api/types';
import { mockApi } from './mock/data';

/** Offline mock mode — fixture data + fake WS (VITE_MOCK=1). */
export const MOCK: boolean = import.meta.env.VITE_MOCK === '1';

async function get<T>(path: string): Promise<T> {
  const res = await fetch(path);
  if (!res.ok) {
    throw new Error(`GET ${path}: ${res.status}`);
  }
  return (await res.json()) as T;
}

export interface SessionFilters {
  /** Project slug or id (server matches either). */
  project?: string;
  status?: string;
}

export function fetchProjects(includeArchived = false): Promise<ProjectsResponse> {
  if (MOCK) return mockApi.projects();
  return get(`/api/projects${includeArchived ? '?include=archived' : ''}`);
}

/** GET /api/projects/{id} — enriched project + local components + stats. */
export function fetchProject(id: number | string): Promise<ProjectDetail> {
  if (MOCK) return mockApi.project(id);
  return get(`/api/projects/${encodeURIComponent(id)}`);
}

/** GET /api/projects/{id}/overview — Canvas v2 editorial aggregate (rightNow / thisWeek / attention). */
export function fetchProjectOverview(id: number | string): Promise<ProjectOverviewResp> {
  return get(`/api/projects/${encodeURIComponent(id)}/overview`);
}

/** DELETE /api/projects/{id} — soft-archive (remove from the default list). */
export async function archiveProject(id: number): Promise<void> {
  if (MOCK) return; // no-op in mock mode
  const res = await fetch(`/api/projects/${String(id)}`, { method: 'DELETE' });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `archive failed: ${String(res.status)}`);
  }
}

/** POST /api/projects/{id}/restore — un-archive. */
export async function restoreProject(id: number): Promise<void> {
  if (MOCK) return; // no-op in mock mode
  const res = await fetch(`/api/projects/${String(id)}/restore`, { method: 'POST' });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `restore failed: ${String(res.status)}`);
  }
}

/** POST /api/projects/{id}/architecture/rebuild — force-regenerate the
 * architecture map via the provision pipeline (single-flight; 202 returns the
 * in-flight job when one is already running). */
export async function rebuildArchitectureMap(id: number): Promise<void> {
  if (MOCK) return; // no-op in mock mode
  const res = await fetch(`/api/projects/${String(id)}/architecture/rebuild`, { method: 'POST' });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `rebuild failed: ${String(res.status)}`);
  }
}

/** GET /api/projects/health — per-project week-over-week health rows. */
export function fetchProjectsHealth(): Promise<ProjectsHealthResponse> {
  if (MOCK) return mockApi.projectsHealth();
  return get('/api/projects/health');
}

/** PATCH /api/projects/{id} — update pinned / tags (dashboard-only meta). */
export async function patchProject(id: number, patch: ProjectMetaPatch): Promise<ProjectMeta> {
  if (MOCK) return { pinned: patch.pinned ?? false, tags: patch.tags ?? [] };
  const res = await fetch(`/api/projects/${String(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `update failed: ${String(res.status)}`);
  }
  return (await res.json()) as ProjectMeta;
}

/**
 * POST /api/projects/{id}/detach — remove the swarmery-owned entries from the
 * project's .claude/settings.json. dryRun=true returns the plan without writing
 * (rendered as a preview before the real call); full=true also removes the
 * other onboarding artifacts (project.json, statusline scripts). 403 when the
 * endpoint is disabled (no SWARMERY_ONBOARD_ROOTS) or the path is outside the
 * allow-list.
 */
export async function detachProject(
  id: number,
  dryRun: boolean,
  full: boolean,
): Promise<DetachResponse> {
  if (MOCK) {
    return {
      detached: true,
      dryRun,
      steps: ['- enabledPlugins.core@swarmery', '- extraKnownMarketplaces.swarmery'],
      ...(dryRun ? {} : { backup: '.claude/settings.json.bak' }),
    };
  }
  const params = new URLSearchParams();
  if (dryRun) params.set('dryRun', '1');
  if (full) params.set('full', '1');
  const qs = params.size > 0 ? `?${params.toString()}` : '';
  const res = await fetch(`/api/projects/${String(id)}/detach${qs}`, {
    method: 'POST',
  });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `detach failed: ${String(res.status)}`);
  }
  return (await res.json()) as DetachResponse;
}

/**
 * POST /api/projects/{id}/attach — re-enable swarmery for a detached project:
 * merge the swarmery entries back into settings.json, restore project.json
 * from its .bak, reinstall hooks. dryRun=true returns the plan without writing.
 * 403 when the endpoint is disabled (no SWARMERY_ONBOARD_ROOTS / workspace
 * root) or the path is outside the allow-list.
 */
export async function attachProject(id: number, dryRun: boolean): Promise<AttachResponse> {
  if (MOCK) {
    return {
      attached: true,
      dryRun,
      steps: ['+ enabledPlugins.core@swarmery', '+ .claude/project.json restored from project.json.bak'],
      ...(dryRun ? {} : { backup: '.claude/settings.json.bak' }),
    };
  }
  const qs = dryRun ? '?dryRun=1' : '';
  const res = await fetch(`/api/projects/${String(id)}/attach${qs}`, {
    method: 'POST',
  });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `attach failed: ${String(res.status)}`);
  }
  return (await res.json()) as AttachResponse;
}

/** GET /api/projects/{id}/plugins — marketplace catalog × enabledPlugins. */
export function fetchProjectPlugins(id: number | string): Promise<ProjectPluginsResponse> {
  if (MOCK) return mockApi.projectPlugins();
  return get(`/api/projects/${encodeURIComponent(id)}/plugins`);
}

/**
 * PUT /api/projects/{id}/plugins/{name} — flip a pack in the project's
 * .claude/settings.json (merge-only, .bak backup on the daemon side). Takes
 * effect in the NEXT Claude Code session. 403 when the daemon write fence is
 * closed; 409 when settings.json is missing/malformed — surfaced inline.
 */
export async function toggleProjectPlugin(
  id: number,
  name: string,
  enabled: boolean,
): Promise<ProjectPluginToggleResponse> {
  if (MOCK) return { name, enabled, changed: true, backup: '.claude/settings.json.bak' };
  const res = await fetch(`/api/projects/${String(id)}/plugins/${encodeURIComponent(name)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ enabled }),
  });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `toggle failed: ${String(res.status)}`);
  }
  return (await res.json()) as ProjectPluginToggleResponse;
}

/**
 * POST /api/projects/{id}/plugins/{name}/repair — runs `claude plugin
 * install|update <id> --scope project` on the daemon side. The action is chosen
 * by the daemon from the current drift status, so the client cannot ask for an
 * install where an update is what is needed. Takes effect in the NEXT Claude
 * Code session, which is why the response always sets restart.
 *
 * Projects whose .claude is a symlinked overlay cannot take a project-scope
 * write at all; the daemon installs at user scope instead and says so in
 * `scope`. Check `warning` on success — it is set when that fallback could not
 * revert the global enable it caused.
 */
export async function repairProjectPlugin(
  id: number,
  pluginId: string,
): Promise<PluginRepairResponse> {
  if (MOCK)
    return {
      id: pluginId,
      action: 'install',
      scope: 'project',
      output: 'mock',
      status: 'ok',
      restart: true,
    };
  const res = await fetch(
    `/api/projects/${String(id)}/plugins/${encodeURIComponent(pluginId)}/repair`,
    { method: 'POST' },
  );
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string; output?: string };
    throw new Error(data.error ?? data.output ?? `repair failed: ${String(res.status)}`);
  }
  return (await res.json()) as PluginRepairResponse;
}

/** GET /api/projects/onboard/config — defaults + enabled state for the modal. */
export function fetchOnboardConfig(): Promise<OnboardConfig> {
  return get('/api/projects/onboard/config');
}

/**
 * POST /api/projects/onboard — bootstrap a new consumer project (.claude/
 * settings.json + project.json + workspace namespace). The endpoint is fenced
 * to an allow-list and returns 403 when disabled; non-2xx throws the server's
 * error text so the form can surface it inline. An empty workspaceRoot falls
 * back to the server default.
 */
export async function onboardProject(
  slug: string,
  path: string,
  packs: string[],
  workspaceRoot?: string,
): Promise<OnboardResponse> {
  const body: OnboardRequest = { slug, path, packs };
  if (workspaceRoot !== undefined && workspaceRoot !== '') body.workspaceRoot = workspaceRoot;
  const res = await fetch('/api/projects/onboard', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `onboard failed: ${String(res.status)}`);
  }
  return (await res.json()) as OnboardResponse;
}

export interface SessionPageOpts {
  /** Server default 100, max 500. */
  limit?: number;
  /** nextCursor of the previous page. */
  cursor?: string;
}

export function fetchSessions(
  filters: SessionFilters = {},
  page: SessionPageOpts = {},
): Promise<SessionsResponse> {
  if (MOCK) return mockApi.sessions(filters);
  const qs = new URLSearchParams();
  if (filters.project !== undefined) qs.set('project', filters.project);
  if (filters.status !== undefined) qs.set('status', filters.status);
  if (page.limit !== undefined) qs.set('limit', String(page.limit));
  if (page.cursor !== undefined) qs.set('cursor', page.cursor);
  const query = qs.toString();
  return get(`/api/sessions${query === '' ? '' : `?${query}`}`);
}

export function fetchSession(id: number | string): Promise<SessionDetailResponse> {
  if (MOCK) return mockApi.session(id);
  return get(`/api/sessions/${encodeURIComponent(id)}`);
}

export function fetchSessionHandoff(id: number | string): Promise<SessionHandoffResponse> {
  if (MOCK) return mockApi.sessionHandoff(id);
  return get(`/api/sessions/${encodeURIComponent(id)}/handoff`);
}

export function fetchSessionContextHogs(id: number | string): Promise<ContextHogsReport> {
  if (MOCK) return mockApi.sessionContextHogs(id);
  return get(`/api/sessions/${encodeURIComponent(id)}/context-hogs`);
}

export function fetchStatsToday(): Promise<StatsToday> {
  if (MOCK) return mockApi.statsToday();
  return get('/api/stats/today');
}

/** Day-scoped overview stats + trailing series (parity contract). */
export function fetchStatsOverview(day: string, project?: string): Promise<StatsOverview> {
  if (MOCK) return mockApi.statsOverview(day);
  const qs = new URLSearchParams({ day });
  if (project !== undefined) qs.set('project', project);
  return get(`/api/stats/overview?${qs.toString()}`);
}

export function fetchHealth(): Promise<HealthResponse> {
  if (MOCK) return mockApi.health();
  return get('/api/health');
}

// --- analytics ----------------------------------------------------------------

/** Optional local-day range; the server defaults to the last 14 days. */
export interface AnalyticsRange {
  from?: string;
  to?: string;
  /** Global project scope — slug or id (server matches either). */
  project?: string;
}

function rangeQuery(range: AnalyticsRange, extra: Record<string, string>): string {
  const qs = new URLSearchParams(extra);
  if (range.from !== undefined) qs.set('from', range.from);
  if (range.to !== undefined) qs.set('to', range.to);
  if (range.project !== undefined) qs.set('project', range.project);
  return qs.toString();
}

/** Daily series for the main chart (one series per group member). */
export function fetchTimeseries(
  metric: AnalyticsMetric,
  group: AnalyticsDimension,
  range: AnalyticsRange = {},
): Promise<TimeseriesResp> {
  if (MOCK) return mockApi.timeseries(metric, group, range);
  return get(`/api/stats/timeseries?${rangeQuery(range, { metric, group })}`);
}

/** Ranked totals for the current pivot dimension. */
export function fetchBreakdown(
  by: AnalyticsDimension,
  range: AnalyticsRange = {},
): Promise<BreakdownResp> {
  if (MOCK) return mockApi.breakdown(by, range);
  return get(`/api/stats/breakdown?${rangeQuery(range, { by })}`);
}

/** Agents|skills × projects cross-tab (metric=runs, or cost for agents). */
export function fetchMatrix(
  rows: 'agent' | 'skill',
  metric: 'runs' | 'cost' = 'runs',
  range: AnalyticsRange = {},
): Promise<MatrixResp> {
  if (MOCK) return mockApi.matrix(rows, metric, range);
  return get(`/api/stats/matrix?${rangeQuery(range, { rows, cols: 'project', metric })}`);
}

/** Per-tool call/error/denied counts + duration stats (analytics uplift).
 * `agent` optionally narrows every row + column to one attributed agent. */
export function fetchToolStats(range: AnalyticsRange = {}, agent?: string): Promise<ToolsResp> {
  if (MOCK) return mockApi.toolStats(range, agent);
  return get(`/api/stats/tools?${rangeQuery(range, agent ? { agent } : {})}`);
}

/** Per-skill invocation/error/denied counts + duration stats (analytics uplift).
 * `agent` optionally narrows every row + column to one attributed agent. */
export function fetchSkillStats(range: AnalyticsRange = {}, agent?: string): Promise<SkillsResp> {
  if (MOCK) return mockApi.skillStats(range, agent);
  return get(`/api/stats/skills?${rangeQuery(range, agent ? { agent } : {})}`);
}

/** Session-duration + approval-wait aggregates (analytics uplift). */
export function fetchDurations(range: AnalyticsRange = {}): Promise<DurationsResp> {
  if (MOCK) return mockApi.durations(range);
  return get(`/api/stats/durations?${rangeQuery(range, {})}`);
}

/** Error events grouped by normalized message key (analytics uplift). */
export function fetchErrorGroups(range: AnalyticsRange = {}): Promise<ErrorsResp> {
  if (MOCK) return mockApi.errorGroups(range);
  return get(`/api/stats/errors?${rangeQuery(range, {})}`);
}

// --- analytics uplift (fusion phase 14) ---------------------------------------

/** Autonomy ratio — tool-calls per human intervention (fusion phase 14). */
export function fetchAutonomy(range: AnalyticsRange = {}): Promise<AutonomyResp> {
  if (MOCK) return mockApi.autonomy(range);
  return get(`/api/stats/autonomy?${rangeQuery(range, {})}`);
}

/** Productivity — commits/LOC/languages/durations + hours-saved ESTIMATE. */
export function fetchProductivity(range: AnalyticsRange = {}): Promise<ProductivityResp> {
  if (MOCK) return mockApi.productivity(range);
  return get(`/api/stats/productivity?${rangeQuery(range, {})}`);
}

/** SDLC funnel snapshot over the board columns (fusion phase 14). */
export function fetchFunnel(range: AnalyticsRange = {}): Promise<FunnelResp> {
  if (MOCK) return mockApi.funnel(range);
  return get(`/api/stats/funnel?${rangeQuery(range, {})}`);
}

/** Per-playbook rollup — empty list pre-Phase-13 (fusion phase 14). */
export function fetchPlaybookStats(range: AnalyticsRange = {}): Promise<PlaybookRollup[]> {
  if (MOCK) return mockApi.playbookStats(range);
  return get(`/api/stats/playbooks?${rangeQuery(range, {})}`);
}

/** Subscription-usage providers: the operator's LIVE Claude quota windows,
 * plus a telemetry-estimate card when SWARMERY_USAGE_LIMITS is set. Unscoped —
 * quota is global. `fresh` bypasses the daemon's 30s cache (the Refresh btn). */
export function fetchUsage(fresh = false): Promise<UsageResp> {
  if (MOCK) return mockApi.usage();
  return get(`/api/usage${fresh ? '?fresh=1' : ''}`);
}

/**
 * POST /api/usage/accounts/{account}/login/start — begin connecting an account
 * swarmery cannot read a credential for. Returns the URL to open in a browser;
 * the PKCE verifier and CSRF state stay in the daemon.
 */
export async function startUsageLogin(account: string): Promise<UsageLoginStart> {
  if (MOCK) throw new Error('connecting an account is not available in mock mode');
  const res = await fetch(`/api/usage/accounts/${encodeURIComponent(account)}/login/start`, {
    method: 'POST',
  });
  if (!res.ok) throw new Error(await errBody(res, 'could not start the connection'));
  return (await res.json()) as UsageLoginStart;
}

/**
 * POST /api/usage/accounts/{account}/login/complete — finish the connection with
 * the "code#state" value the callback page shows. The daemon exchanges it and
 * stores the credential; nothing is echoed back.
 */
export async function completeUsageLogin(account: string, code: string): Promise<void> {
  if (MOCK) throw new Error('connecting an account is not available in mock mode');
  const res = await fetch(`/api/usage/accounts/${encodeURIComponent(account)}/login/complete`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ code }),
  });
  if (!res.ok) throw new Error(await errBody(res, 'could not complete the connection'));
}

/**
 * DELETE /api/usage/accounts/{account}/login — disconnect an account.
 *
 * Removes the credential swarmery's own store holds for it, and nothing else:
 * the `claude` CLI's credential file and the macOS keychain item are untouched,
 * and nothing is revoked upstream at Anthropic. Idempotent — an account that is
 * already disconnected answers 200 too.
 */
export async function disconnectUsageAccount(account: string): Promise<void> {
  if (MOCK) throw new Error('disconnecting an account is not available in mock mode');
  const res = await fetch(`/api/usage/accounts/${encodeURIComponent(account)}/login`, {
    method: 'DELETE',
  });
  if (!res.ok) throw new Error(await errBody(res, 'could not disconnect the account'));
}

// --- retro loop (per-agent scorecards + friction board) -----------------------

/** Per-agent health scorecards + previous-window comparison (retro loop). */
export function fetchRetroAgents(range: AnalyticsRange = {}): Promise<RetroAgentsResp> {
  if (MOCK) return mockApi.retroAgents();
  return get(`/api/retro/agents?${rangeQuery(range, {})}`);
}

/** Friction board: denied tools, top error groups, approval waits (retro loop). */
export function fetchRetroFriction(range: AnalyticsRange = {}): Promise<RetroFrictionResp> {
  if (MOCK) return mockApi.retroFriction();
  return get(`/api/retro/friction?${rangeQuery(range, {})}`);
}

/** Lessons-learned feed parsed from 09-retrospective.md docs (retro phase 2). */
export function fetchRetroLessons(range: AnalyticsRange = {}): Promise<RetroLessonsResp> {
  if (MOCK) return mockApi.retroLessons();
  return get(`/api/retro/lessons?${rangeQuery(range, {})}`);
}

/** Estimation accuracy + loop/delegation counts per task (retro phase 2). */
export function fetchRetroTasks(range: AnalyticsRange = {}): Promise<RetroTasksResp> {
  if (MOCK) return mockApi.retroTasks();
  return get(`/api/retro/tasks?${rangeQuery(range, {})}`);
}

/**
 * Advisor recommendations (retro phase 3). `status` is a CSV filter or 'all';
 * the server defaults to the actionable set (proposed,accepted,adopted).
 */
export function fetchRecommendations(status?: string): Promise<RecommendationsResp> {
  if (MOCK) return mockApi.retroRecommendations();
  const qs = status !== undefined ? `?status=${encodeURIComponent(status)}` : '';
  return get(`/api/retro/recommendations${qs}`);
}

/**
 * PATCH /api/retro/recommendations/{id} — accept or dismiss one proposal.
 * Illegal transitions come back 422 with an {error} body.
 */
export async function patchRecommendation(
  id: number,
  status: Extract<RecommendationStatus, 'accepted' | 'dismissed'>,
): Promise<Recommendation> {
  if (MOCK) return mockApi.patchRecommendation(id, status);
  const res = await fetch(`/api/retro/recommendations/${String(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ status }),
  });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `patch recommendation failed: ${String(res.status)}`);
  }
  return (await res.json()) as Recommendation;
}

/** POST /api/retro/advise — run the advisor rule engine now ("Analyze now"). */
export async function runAdvise(): Promise<AdviseStats> {
  if (MOCK) return mockApi.advise();
  const res = await fetch('/api/retro/advise', { method: 'POST' });
  if (!res.ok) throw new Error(`advise failed: ${String(res.status)}`);
  return (await res.json()) as AdviseStats;
}

// --- fusion phase 12 — project-scoped insights + memory ----------------------

/**
 * GET /api/retro/recommendations?projectId= — the advisor recs attributable to
 * one project (post-filtered on evidence session). `project` is a slug or id.
 */
export function fetchProjectRecommendations(
  project: string | number,
  status?: string,
): Promise<RecommendationsResp> {
  if (MOCK) return mockApi.projectRecommendations(project);
  const qs = new URLSearchParams({ projectId: String(project) });
  if (status !== undefined) qs.set('status', status);
  return get(`/api/retro/recommendations?${qs.toString()}`);
}

/**
 * POST /api/retro/advise?projectId= — run the advisor now for the project's
 * Insights card. The engine still runs fleet-wide (cross-project rates); the
 * projectId is accepted for API symmetry and the READ side does the narrowing.
 */
export async function runProjectAdvise(project: string | number): Promise<AdviseStats> {
  if (MOCK) return mockApi.advise();
  const qs = new URLSearchParams({ projectId: String(project) });
  const res = await fetch(`/api/retro/advise?${qs.toString()}`, { method: 'POST' });
  if (!res.ok) throw new Error(`advise failed: ${String(res.status)}`);
  return (await res.json()) as AdviseStats;
}

/** GET /api/projects/{id}/memory — the project's memory files across 3 roots. */
export function fetchMemoryList(project: string | number): Promise<MemoryListResp> {
  if (MOCK) return mockApi.memoryList(project);
  return get(`/api/projects/${encodeURIComponent(String(project))}/memory`);
}

/** GET /api/projects/{id}/memory/file?path= — one memory file's content+hash. */
export function fetchMemoryFile(
  project: string | number,
  path: string,
): Promise<MemoryFileContent> {
  if (MOCK) return mockApi.memoryFile(project, path);
  const qs = new URLSearchParams({ path });
  return get(`/api/projects/${encodeURIComponent(String(project))}/memory/file?${qs.toString()}`);
}

/**
 * PUT /api/projects/{id}/memory/file?path= — versioned write. A 409 (base_hash
 * drifted from disk) throws with the disk-side message so the editor can prompt
 * a reload; 403 means the readonly kill-switch is on.
 */
export async function putMemoryFile(
  project: string | number,
  path: string,
  content: string,
  baseHash: string,
): Promise<MemoryFileContent> {
  if (MOCK) return mockApi.putMemoryFile(project, path, content);
  const qs = new URLSearchParams({ path });
  const res = await fetch(
    `/api/projects/${encodeURIComponent(String(project))}/memory/file?${qs.toString()}`,
    {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ content, base_hash: baseHash }),
    },
  );
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `save failed: ${String(res.status)}`);
  }
  return (await res.json()) as MemoryFileContent;
}

// --- self-improvement phase 4 — agent change proposals -----------------------

/** Extracts an {error} message from a failed response body, else a default. */
async function errBody(res: Response, fallback: string): Promise<string> {
  const data = (await res.json().catch(() => ({}))) as { error?: string };
  return data.error ?? `${fallback}: ${String(res.status)}`;
}

/** GET /api/retro/proposals — newest first; optional CSV status filter. */
export function fetchProposals(status?: string): Promise<ProposalsResp> {
  if (MOCK) return mockApi.proposals();
  const qs = status !== undefined ? `?status=${encodeURIComponent(status)}` : '';
  return get(`/api/retro/proposals${qs}`);
}

/**
 * GET /api/retro/agents/{agent}/evidence — read-only preview of the evidence
 * bundle the rewriter would feed the model. A built-in agent answers
 * in_registry:false with no bundle.
 */
export function fetchAgentEvidence(agent: string): Promise<AgentEvidence> {
  return get(`/api/retro/agents/${encodeURIComponent(agent)}/evidence`);
}

/**
 * POST /api/retro/agents/{agent}/improve — generate a proposal for one agent.
 * 202 while the model runs; 404 unknown agent; 409 an open proposal exists.
 */
export async function improveAgent(agent: string): Promise<void> {
  const res = await fetch(`/api/retro/agents/${encodeURIComponent(agent)}/improve`, {
    method: 'POST',
  });
  if (!res.ok) throw new Error(await errBody(res, 'improve agent failed'));
}

/**
 * PATCH /api/retro/proposals/{id} — approve or reject one proposal. Approving
 * fires the apply/PR pipeline async; illegal transitions come back 422.
 */
export async function patchProposal(
  id: number,
  status: 'approved' | 'rejected',
): Promise<void> {
  const res = await fetch(`/api/retro/proposals/${String(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ status }),
  });
  if (!res.ok) throw new Error(await errBody(res, 'decide proposal failed'));
}

/** POST /api/retro/proposals/{id}/retry — re-run generation for a failed row. */
export async function retryProposal(id: number): Promise<void> {
  const res = await fetch(`/api/retro/proposals/${String(id)}/retry`, { method: 'POST' });
  if (!res.ok) throw new Error(await errBody(res, 'retry proposal failed'));
}

/** POST /api/retro/proposals/{id}/apply — manual re-run of a stuck approved row. */
export async function applyProposal(id: number): Promise<void> {
  const res = await fetch(`/api/retro/proposals/${String(id)}/apply`, { method: 'POST' });
  if (!res.ok) throw new Error(await errBody(res, 'apply proposal failed'));
}

export function fetchDocs(): Promise<DocMeta[]> {
  if (MOCK) return mockApi.docs();
  return get('/api/docs');
}

export function fetchDoc(slug: string): Promise<DocDetail> {
  if (MOCK) return mockApi.doc(slug);
  return get(`/api/docs/${encodeURIComponent(slug)}`);
}

// --- phase 3.5: workspaces ----------------------------------------------------

/** Recently active workspace tasks (default window: 14 days). */
export function fetchTasks(days = 14): Promise<TasksResponse> {
  if (MOCK) return mockApi.tasks();
  return get(`/api/tasks?days=${String(days)}`);
}

/** One workspace task: card metadata + linked sessions + Σ cost. */
export function fetchTask(id: number | string): Promise<TaskDetail> {
  if (MOCK) return mockApi.task(id);
  return get(`/api/tasks/${encodeURIComponent(id)}`);
}

// --- fusion phase 1/3: task board + dispatcher (frozen contract in api/types) ---

/**
 * GET /api/board/tasks?projectId=&boardColumn= — dispatchable board rows
 * (source='queue'), newest first. Both filters optional; the board scopes by
 * projectId, the Archived column lazy-loads with boardColumn='archived'.
 */
export function fetchBoardTasks(projectId?: number, boardColumn?: BoardColumn): Promise<BoardTask[]> {
  if (MOCK) return mockApi.boardTasks(projectId, boardColumn);
  const qs = new URLSearchParams();
  if (projectId !== undefined) qs.set('projectId', String(projectId));
  if (boardColumn !== undefined) qs.set('boardColumn', boardColumn);
  const q = qs.toString();
  return get(`/api/board/tasks${q === '' ? '' : `?${q}`}`);
}

/** Body of POST /api/board/tasks (matches createBoardTask in tasks_board.go). */
export interface CreateBoardTaskInput {
  projectId: number;
  title: string;
  prompt: string;
  priority?: string;
  model?: string;
  /** Selected execution recipe name (fusion phase 13); omit/empty = default. */
  playbook?: string;
  fileScope?: string[];
  dependencies?: string[];
  boardColumn?: BoardColumn;
}

/** POST /api/board/tasks → 201 BoardTask. QuickEntry sends {title,prompt=title}. */
export async function createBoardTask(input: CreateBoardTaskInput): Promise<BoardTask> {
  if (MOCK) return mockApi.createBoardTask(input);
  const res = await fetch('/api/board/tasks', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `create task failed: ${String(res.status)}`);
  }
  return (await res.json()) as BoardTask;
}

/**
 * User-editable subset of a board task (the fields patchBoardTask accepts —
 * boardColumn/title/prompt/priority/model/fileScope/dependencies/paused/
 * userPaused). Dispatcher-owned fields are NOT settable here.
 */
export interface PatchBoardTaskInput {
  boardColumn?: BoardColumn;
  title?: string;
  prompt?: string;
  priority?: string;
  model?: string | null;
  /** Selected recipe name; "" clears the selection back to the default. */
  playbook?: string | null;
  fileScope?: string[];
  dependencies?: string[];
  paused?: boolean;
  userPaused?: boolean;
}

/** PATCH /api/board/tasks/{id} → updated BoardTask (id = numeric row id). */
export async function patchBoardTask(id: number, patch: PatchBoardTaskInput): Promise<BoardTask> {
  if (MOCK) return mockApi.patchBoardTask(id, patch);
  const res = await fetch(`/api/board/tasks/${String(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `patch task failed: ${String(res.status)}`);
  }
  return (await res.json()) as BoardTask;
}

/** GET /api/dispatch — dispatcher status snapshot (503 when not attached). */
export function fetchDispatchStatus(): Promise<DispatchStatus> {
  if (MOCK) return mockApi.dispatch();
  return get('/api/dispatch');
}

// --- fusion phase 13: playbooks (selectable workflows) ------------------------

/**
 * GET /api/playbooks?projectId= — the playbooks visible to a project (built-ins
 * overlaid by the project's own .claude/playbooks files), sorted by name. Omit
 * projectId for built-ins only.
 */
export function fetchPlaybooks(projectId?: number): Promise<Playbook[]> {
  if (MOCK) return mockApi.playbooks(projectId);
  const qs = projectId !== undefined ? `?projectId=${String(projectId)}` : '';
  return get(`/api/playbooks${qs}`);
}

/**
 * POST /api/projects/{id}/playbooks/{name}/duplicate — copy a built-in's
 * markdown into the project so its prompts become editable. Non-2xx (404 unknown
 * built-in/project, 409 the project file already exists, 503 not attached)
 * throws the server's {error} text for inline display.
 */
export async function duplicatePlaybook(
  projectId: number,
  name: string,
): Promise<DuplicatePlaybookResponse> {
  if (MOCK) return mockApi.duplicatePlaybook(projectId, name);
  const res = await fetch(
    `/api/projects/${String(projectId)}/playbooks/${encodeURIComponent(name)}/duplicate`,
    { method: 'POST' },
  );
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `duplicate playbook failed: ${String(res.status)}`);
  }
  return (await res.json()) as DuplicatePlaybookResponse;
}

// --- fusion phase 8: planning mode --------------------------------------------

/** GET /api/projects/{id}/planning — the planner status for a project. */
export function fetchPlanning(projectId: number): Promise<PlanningStatus> {
  if (MOCK) return mockApi.planning(projectId);
  return get(`/api/projects/${String(projectId)}/planning`);
}

/**
 * POST /api/projects/{id}/planning {idea} — spawn a headless planner run.
 * Returns 202 with the pre-generated session uuid. Non-2xx (400 empty idea,
 * 404 unknown project, 409 a run is already active, 503 not attached) throws the
 * server's {error} text for inline display.
 */
export async function startPlanning(projectId: number, idea: string): Promise<PlanningStart> {
  if (MOCK) return mockApi.startPlanning(projectId, idea);
  const res = await fetch(`/api/projects/${String(projectId)}/planning`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ idea }),
  });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `start planning failed: ${String(res.status)}`);
  }
  return (await res.json()) as PlanningStart;
}

/** POST /api/projects/{id}/planning/cancel — abort the in-flight planner run. */
export async function cancelPlanning(projectId: number): Promise<void> {
  if (MOCK) return; // no-op in mock mode
  const res = await fetch(`/api/projects/${String(projectId)}/planning/cancel`, { method: 'POST' });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `cancel planning failed: ${String(res.status)}`);
  }
}

/**
 * POST /api/projects/{id}/planning/answer — consume the current wizard
 * question. Structured mode sends the question id + selected option ids
 * (otherText fills the "Other" option); raw-fallback mode sends questionId ""
 * with empty selectedOptionIds and the whole free-text reply as otherText.
 * Non-2xx (400 empty selection, 404 no wizard, 409 not awaiting / wrong
 * question / resume in flight) throws the server's {error} text.
 */
export async function answerPlanning(
  projectId: number,
  body: { questionId: string; selectedOptionIds: string[]; otherText?: string },
): Promise<{ status: string }> {
  if (MOCK) return mockApi.answerPlanning(projectId, body);
  const res = await fetch(`/api/projects/${String(projectId)}/planning/answer`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `answer planning failed: ${String(res.status)}`);
  }
  return (await res.json()) as { status: string };
}

/**
 * POST /api/projects/{id}/planning/refine {instructions} — free-form
 * course-correction: the plan updates and the next questions follow the
 * operator's direction. Same error matrix as answer.
 */
export async function refinePlanning(
  projectId: number,
  instructions: string,
): Promise<{ status: string }> {
  if (MOCK) return mockApi.refinePlanning(projectId, instructions);
  const res = await fetch(`/api/projects/${String(projectId)}/planning/refine`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ instructions }),
  });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `refine planning failed: ${String(res.status)}`);
  }
  return (await res.json()) as { status: string };
}

/**
 * POST /api/projects/{id}/planning/proceed — end the interview and trigger
 * plan writing (PHASE B). 404/409 as answer.
 */
export async function proceedPlanning(projectId: number): Promise<{ status: string }> {
  if (MOCK) return mockApi.proceedPlanning(projectId);
  const res = await fetch(`/api/projects/${String(projectId)}/planning/proceed`, {
    method: 'POST',
  });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `proceed planning failed: ${String(res.status)}`);
  }
  return (await res.json()) as { status: string };
}

/** POST /api/dispatch/pause — global or per-project pause toggle. */
export async function pauseDispatch(
  scope: 'global' | 'project',
  paused: boolean,
  projectId?: number,
): Promise<void> {
  if (MOCK) return; // no-op in mock mode
  const body: { scope: string; paused: boolean; projectId?: number } = { scope, paused };
  if (scope === 'project' && projectId !== undefined) body.projectId = projectId;
  const res = await fetch('/api/dispatch/pause', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `pause failed: ${String(res.status)}`);
  }
}

// --- phase 2 — approvals (docs/hooks-protocol.md; DTO frozen in api/types.ts) ---

/**
 * `resolved` is a meta-filter covering every terminal status — assumed
 * server-side (see web/CONTRACT-REQUESTS.md); the UI only ever asks for
 * `pending` and `resolved`.
 */
export type ApprovalStatusFilter = PermissionRequestStatus | 'resolved';

/**
 * `answer` resolves an AskUserQuestion with per-question answers; `terminal`
 * is the no-decision handoff to the native terminal selector (E12d/E12e —
 * a plain approve would resolve the questions unanswered).
 */
export type ApprovalAction = 'approve' | 'deny' | 'answer' | 'terminal';

/** {action:"answer"} answers: string, or an array of labels for multiSelect. */
export type ApprovalAnswers = Record<string, string | string[]>;

export function fetchApprovals(status?: ApprovalStatusFilter): Promise<PermissionRequest[]> {
  if (MOCK) return mockApi.approvals(status);
  const qs = status !== undefined ? `?status=${encodeURIComponent(status)}` : '';
  return get(`/api/approvals${qs}`);
}

/**
 * POST /api/approvals/{id} → 200 with the updated PermissionRequest.
 * Non-2xx (e.g. 409 when the row raced to a terminal state via the terminal
 * dialog or expiry) throws — callers silently refetch; the WS
 * permission_resolved is the authoritative reconciliation either way.
 */
export async function resolveApproval(
  id: number,
  action: ApprovalAction,
  reason?: string,
  answers?: ApprovalAnswers,
): Promise<PermissionRequest> {
  if (MOCK) return mockApi.resolveApproval(id, action, reason, answers);
  const body: { action: ApprovalAction; reason?: string; answers?: ApprovalAnswers } = { action };
  if (reason !== undefined && reason !== '') body.reason = reason;
  if (answers !== undefined) body.answers = answers;
  const res = await fetch(`/api/approvals/${String(id)}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    throw new Error(`POST /api/approvals/${String(id)}: ${String(res.status)}`);
  }
  return (await res.json()) as PermissionRequest;
}

// --- control-plane v2 — auto-approve rules ------------------------------------

export interface ApprovalRuleInput {
  projectId: number | null;
  toolPattern: string;
  note?: string;
}

export function fetchApprovalRules(): Promise<ApprovalRule[]> {
  if (MOCK) return Promise.resolve([]);
  return get('/api/approval-rules');
}

export async function createApprovalRule(input: ApprovalRuleInput): Promise<ApprovalRule> {
  const res = await fetch('/api/approval-rules', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `create rule failed: ${String(res.status)}`);
  }
  return (await res.json()) as ApprovalRule;
}

export async function toggleApprovalRule(id: number, enabled: boolean): Promise<ApprovalRule> {
  const res = await fetch(`/api/approval-rules/${String(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ enabled }),
  });
  if (!res.ok) throw new Error(`toggle rule failed: ${String(res.status)}`);
  return (await res.json()) as ApprovalRule;
}

export async function deleteApprovalRule(id: number): Promise<void> {
  const res = await fetch(`/api/approval-rules/${String(id)}`, { method: 'DELETE' });
  if (!res.ok) throw new Error(`delete rule failed: ${String(res.status)}`);
}

// --- fusion phase 11 — permission presets ------------------------------------

/**
 * Thrown by putPermissionPreset on a 428: the change escalates privileges and
 * needs explicit confirmation. Carries the escalation list so the caller can
 * render a confirm dialog, then retry with `confirm: true`.
 */
export class EscalationRequiredError extends Error {
  readonly escalations: string[];
  readonly reason: string;
  constructor(payload: PermissionEscalation) {
    super(payload.error);
    this.name = 'EscalationRequiredError';
    this.escalations = payload.escalations;
    this.reason = payload.reason;
  }
}

export function fetchPermissionPreset(projectId: number | string): Promise<PermissionPresetView> {
  return get(`/api/projects/${String(projectId)}/permission-preset`);
}

/**
 * PUT /api/projects/{id}/permission-preset. On a 428 (privileged change without
 * confirm) throws {@link EscalationRequiredError}; other non-2xx throw a plain
 * Error. Returns the recompiled effective policy view on success.
 */
export async function putPermissionPreset(
  projectId: number | string,
  input: PermissionPresetInput,
): Promise<PermissionPresetView> {
  const res = await fetch(`/api/projects/${String(projectId)}/permission-preset`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  if (res.status === 428) {
    const payload = (await res.json().catch(() => ({
      error: 'confirmation required',
      reason: '',
      escalations: [],
    }))) as PermissionEscalation;
    throw new EscalationRequiredError(payload);
  }
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `set preset failed: ${String(res.status)}`);
  }
  return (await res.json()) as PermissionPresetView;
}

/** POST /api/sessions/{id}/kill — send SIGTERM (force=false) or SIGKILL (force=true). */
export async function killSession(id: number, force = false): Promise<void> {
  if (MOCK) return; // no-op in mock mode
  const res = await fetch(`/api/sessions/${String(id)}/kill`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ force }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({})) as { error?: string };
    throw new Error(data.error ?? `kill failed: ${String(res.status)}`);
  }
}

/** POST /api/sessions/{id}/stop — graceful SIGTERM; the session is recorded
 * as 'completed' (not 'killed'). Works even when no PID is known. */
export async function stopSession(id: number): Promise<void> {
  if (MOCK) return; // no-op in mock mode
  const res = await fetch(`/api/sessions/${String(id)}/stop`, { method: 'POST' });
  if (!res.ok) {
    const data = await res.json().catch(() => ({})) as { error?: string };
    throw new Error(data.error ?? `stop failed: ${String(res.status)}`);
  }
}

/**
 * POST /api/sessions/{id}/message — resume an idle/completed conversation
 * headlessly (`claude -r <uuid> -p <text>`). Returns 202 immediately; the
 * resulting user + assistant turns arrive on the open detail via the WS bus
 * once the ingest watcher tails the transcript. Non-2xx (409 for a live
 * session or for a session whose working directory is gone — a run's worktree
 * is removed when the run ends — and 503 when the claude binary is missing)
 * throws with the server's error text so the composer can surface it inline.
 */
export async function sendSessionMessage(id: number, text: string): Promise<void> {
  if (MOCK) return; // no-op in mock mode
  const res = await fetch(`/api/sessions/${String(id)}/message`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ text }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({})) as { error?: string };
    throw new Error(data.error ?? `send failed: ${String(res.status)}`);
  }
}

/**
 * POST /api/sessions/{id}/message/cancel — abort the in-flight headless resume
 * run (kills the child claude process). 409 when nothing is in flight.
 */
export async function cancelSessionMessage(id: number): Promise<void> {
  if (MOCK) return; // no-op in mock mode
  const res = await fetch(`/api/sessions/${String(id)}/message/cancel`, { method: 'POST' });
  if (!res.ok) {
    const data = await res.json().catch(() => ({})) as { error?: string };
    throw new Error(data.error ?? `cancel failed: ${String(res.status)}`);
  }
}

/**
 * PATCH /api/sessions/{id} — set or clear (null) the manual session outcome.
 */
export async function patchSessionOutcome(
  id: number,
  outcome: SessionOutcome | null,
): Promise<void> {
  if (MOCK) return; // no-op in mock mode
  const res = await fetch(`/api/sessions/${String(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ outcome }),
  });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `outcome failed: ${String(res.status)}`);
  }
}

/**
 * PATCH /api/sessions/{id} — rename a session. A blank/null title clears the
 * override and reverts to the ingested ai-title.
 */
export async function renameSession(id: number, title: string | null): Promise<void> {
  if (MOCK) return; // no-op in mock mode
  const res = await fetch(`/api/sessions/${String(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ title }),
  });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `rename failed: ${String(res.status)}`);
  }
}

// --- tool dashboards (serena LSP dashboard + graphify viz) ----------------------

/** GET /api/tools — sidebar feed for daemon-managed tool dashboards. */
export function fetchTools(): Promise<ToolsResponse> {
  if (MOCK) return mockApi.tools();
  return get('/api/tools');
}

/**
 * POST /api/projects/{id}/serena/start — launch the project's serena dashboard
 * process. Non-2xx (403 fence closed, 404 no lsp-pack, 409 already running,
 * 503 binary missing) throws the server's {error} text for inline display.
 */
export async function serenaStart(id: number): Promise<void> {
  if (MOCK) return; // no-op in mock mode
  const res = await fetch(`/api/projects/${String(id)}/serena/start`, { method: 'POST' });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `serena start failed: ${String(res.status)}`);
  }
}

/** POST /api/projects/{id}/serena/stop — stop the dashboard process. */
export async function serenaStop(id: number): Promise<void> {
  if (MOCK) return; // no-op in mock mode
  const res = await fetch(`/api/projects/${String(id)}/serena/stop`, { method: 'POST' });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `serena stop failed: ${String(res.status)}`);
  }
}

// --- connectors (MCP servers) --------------------------------------------------

export function fetchConnectors(): Promise<ConnectorsResponse> {
  if (MOCK) return mockApi.connectors();
  return get<ConnectorsResponse>('/api/connectors');
}

/** Add a stdio/http/sse MCP server; the daemon returns the refreshed list. */
export async function addConnector(input: AddConnectorInput): Promise<Connector[]> {
  if (MOCK) return (await mockApi.connectors()).connectors;
  const res = await fetch('/api/connectors', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `add connector failed: ${String(res.status)}`);
  }
  const body = (await res.json()) as ConnectorsResponse;
  return body.connectors;
}

/** Remove a server by name (optionally scoped); returns the refreshed list. */
export async function removeConnector(name: string, scope?: string): Promise<Connector[]> {
  if (MOCK) return (await mockApi.connectors()).connectors;
  const qs = scope !== undefined && scope !== '' ? `?scope=${encodeURIComponent(scope)}` : '';
  const res = await fetch(`/api/connectors/${encodeURIComponent(name)}${qs}`, { method: 'DELETE' });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `remove connector failed: ${String(res.status)}`);
  }
  const body = (await res.json()) as ConnectorsResponse;
  return body.connectors;
}

// --- global search (Cmd+K palette) ---------------------------------------------

/**
 * GET /api/search — grouped global search (sessions / turns / files /
 * projects). Mock mode returns empty groups so the palette still renders its
 * static Navigation section offline.
 */
export function fetchSearch(q: string, project?: string, limit = 20): Promise<SearchResponse> {
  if (MOCK) {
    return Promise.resolve({ query: q, sessions: [], turns: [], files: [], projects: [] });
  }
  const qs = new URLSearchParams({ q, limit: String(limit) });
  if (project !== undefined && project !== '') qs.set('project', project);
  return get(`/api/search?${qs.toString()}`);
}

/** GET /api/files/sessions — sessions that touched files matching `path`. */
export function fetchFileSessions(path: string, project?: string): Promise<FileSessionsResponse> {
  if (MOCK) return Promise.resolve({ path, sessions: [] });
  const qs = new URLSearchParams({ path });
  if (project !== undefined && project !== '') qs.set('project', project);
  return get(`/api/files/sessions?${qs.toString()}`);
}

// ── Routines (fusion phase 7) ───────────────────────────────────────────────

/** GET /api/routines — all routines (optionally project-scoped), newest first. */
export function fetchRoutines(projectId?: number): Promise<Routine[]> {
  if (MOCK) return mockApi.routines();
  const qs = projectId ? `?projectId=${String(projectId)}` : '';
  return get(`/api/routines${qs}`);
}

/** GET /api/routines/{id}/runs — run history (newest first). */
export function fetchRoutineRuns(id: string): Promise<RoutineRun[]> {
  if (MOCK) return mockApi.routineRuns(id);
  return get(`/api/routines/${encodeURIComponent(id)}/runs`);
}

/** POST /api/routines — create. Returns the routine (with webhookToken when
 * webhook:true was requested). */
export async function createRoutine(input: RoutineInput): Promise<Routine> {
  if (MOCK) return mockApi.createRoutine(input);
  const res = await fetch('/api/routines', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `create routine failed: ${String(res.status)}`);
  }
  return (await res.json()) as Routine;
}

/** PATCH /api/routines/{id} — partial update. */
export async function patchRoutine(id: string, input: Partial<RoutineInput>): Promise<Routine> {
  if (MOCK) return mockApi.patchRoutine(id, input);
  const res = await fetch(`/api/routines/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `update routine failed: ${String(res.status)}`);
  }
  return (await res.json()) as Routine;
}

/** DELETE /api/routines/{id}. */
export async function deleteRoutine(id: string): Promise<void> {
  if (MOCK) return;
  const res = await fetch(`/api/routines/${encodeURIComponent(id)}`, { method: 'DELETE' });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `delete routine failed: ${String(res.status)}`);
  }
}

/** POST /api/routines/{id}/run — manual trigger. Returns whether a run started
 * (false when the routine is already running / the global cap is full). */
export async function runRoutine(id: string): Promise<{ status: string }> {
  if (MOCK) return { status: 'started' };
  const res = await fetch(`/api/routines/${encodeURIComponent(id)}/run`, { method: 'POST' });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `run routine failed: ${String(res.status)}`);
  }
  return (await res.json()) as { status: string };
}

// ── Epics (fusion phase 10) ─────────────────────────────────────────────────

/** GET /api/epics?projectId= — epics (workspace plans) with phases + rollups. */
export function fetchEpics(projectId?: number): Promise<Epic[]> {
  if (MOCK) return mockApi.epics(projectId);
  const qs = projectId !== undefined ? `?projectId=${String(projectId)}` : '';
  return get(`/api/epics${qs}`);
}

export type EpicLifecycleAction = 'pause' | 'resume' | 'archive' | 'restore';

/**
 * POST /api/epics/{taskId}/lifecycle {action} → 200 {status}. File-backed on
 * the daemon side (README status rewrite / working↔archive zone move); 409 on
 * an invalid transition, 404 when the task has no plan dir.
 */
export async function epicLifecycle(
  taskId: number,
  action: EpicLifecycleAction,
): Promise<{ status: Epic['status'] }> {
  if (MOCK) {
    const next: Record<EpicLifecycleAction, Epic['status']> = {
      pause: 'paused',
      resume: 'active',
      archive: 'archived',
      restore: 'active',
    };
    return { status: next[action] };
  }
  const res = await fetch(`/api/epics/${String(taskId)}/lifecycle`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ action }),
  });
  if (!res.ok) {
    const body = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(body.error ?? `lifecycle ${action} failed (${String(res.status)})`);
  }
  return (await res.json()) as { status: Epic['status'] };
}

/**
 * A run rejection that carries structured escape-hatch data. The
 * branch-holds-commits 409 names the branch (and how far ahead it is) so the UI
 * can offer "delete it and retry" instead of asking the user to parse prose.
 *
 * Both run surfaces throw this: the phase run and the whole-plan run answer with
 * the same four fields, so one type covers both. `base` names the branch
 * commitsAhead was measured against — without it "2 commits ahead" is not a fact
 * the user can act on, since the same branch can be 2 ahead of `dev` and 0 ahead
 * of a feature branch that already carries them. Absent when the daemon could not
 * name a base (no git seam, detached HEAD) — never guessed.
 */
export type PhaseRunBranchError = Error & {
  /**
   * The 409's stable discriminator. Callers switch on THIS, never on which
   * fields happen to be present: a body-shape sniff mis-classifies every future
   * case that carries the same field. Undefined only for a non-409 failure (or a
   * body the daemon could not encode), so a consumer must treat "no code" as
   * "unknown rejection", not as any particular case.
   */
  code?: RunConflictCode | undefined;
  branch?: string | undefined;
  commitsAhead?: number | undefined;
  base?: string | undefined;
};

/**
 * runConflictError builds the enriched error every run/branch endpoint throws.
 *
 * The branch facts ride along only for the case that actually measured them
 * (`branch-dirty`), keyed off `code` rather than off `body.branch !== undefined`
 * — the sniff this helper exists to replace. Every other 409 still gets its
 * `code`, so a caller can distinguish "checked out elsewhere" from "is HEAD"
 * from "outside swarm/" without reading the prose.
 */
function runConflictError(
  body: {
    error?: string;
    code?: RunConflictCode;
    branch?: string;
    commitsAhead?: number;
    base?: string;
  },
  fallback: string,
): PhaseRunBranchError {
  const err: PhaseRunBranchError = new Error(body.error ?? fallback);
  if (body.code !== undefined) err.code = body.code;
  if (body.code === 'branch-dirty') {
    err.branch = body.branch;
    err.commitsAhead = body.commitsAhead;
    err.base = body.base;
  }
  return err;
}

/**
 * POST /api/epics/{taskId}/phases/{phaseId}/run — execute one plan phase
 * headlessly in an isolated worktree (no board task). 202 {status, sessionUuid};
 * 409 carries the gate reason (already running / unmet deps / no doc) in the
 * error body — surfaced verbatim for the toast.
 */
export async function runEpicPhase(
  taskId: number,
  phaseId: number,
): Promise<{ status: string; sessionUuid: string }> {
  if (MOCK) return { status: 'running', sessionUuid: 'mock-run-uuid' };
  const res = await fetch(`/api/epics/${String(taskId)}/phases/${String(phaseId)}/run`, {
    method: 'POST',
  });
  if (!res.ok) {
    // Every 409 carries a `code`; the branch-holds-commits one additionally
    // carries structured escape-hatch data (`branch`, `commitsAhead`, `base`) the
    // caller turns into a "delete the branch" affordance. Both ride along on the
    // Error rather than being flattened into the message, which would force the
    // UI to parse prose.
    const body = (await res.json().catch(() => ({}))) as {
      error?: string;
      code?: RunConflictCode;
      branch?: string;
      commitsAhead?: number;
      base?: string;
    };
    throw runConflictError(body, `phase run failed (${String(res.status)})`);
  }
  return (await res.json()) as { status: string; sessionUuid: string };
}

/**
 * GET /api/epics/{taskId}/phases/{phaseId}/diagnosis — why a phase run did (or
 * did not) achieve anything: the derived outcome, the criteria delta, the
 * deterministic blockers and the executor's last word. READ-ONLY, so it stays
 * available even while a plan run owns the docs.
 */
export function fetchPhaseDiagnosis(taskId: number, phaseId: number): Promise<PhaseDiagnosis> {
  if (MOCK) return mockApi.phaseDiagnosis(taskId, phaseId);
  return get(`/api/epics/${String(taskId)}/phases/${String(phaseId)}/diagnosis`);
}

/**
 * DELETE /api/epics/{taskId}/phases/{phaseId}/branch — reclaim the previous
 * run's branch so the phase can be retried. 200 {deleted, branch}; 409 while a
 * run is active or the branch is checked out.
 */
export async function deletePhaseRunBranch(
  taskId: number,
  phaseId: number,
): Promise<{ deleted: boolean; branch: string }> {
  if (MOCK) return { deleted: true, branch: 'swarm/phase-mock' };
  const res = await fetch(`/api/epics/${String(taskId)}/phases/${String(phaseId)}/branch`, {
    method: 'DELETE',
  });
  if (!res.ok) {
    // Same enriched error the run endpoints throw: a delete refusal is a state
    // the user can resolve (checked out somewhere, is HEAD, a run owns it), and
    // `code` is what lets the caller say which one without matching on prose.
    const body = (await res.json().catch(() => ({}))) as {
      error?: string;
      code?: RunConflictCode;
    };
    throw runConflictError(body, `branch delete failed (${String(res.status)})`);
  }
  return (await res.json()) as { deleted: boolean; branch: string };
}

/**
 * DELETE /api/epics/{taskId}/orphan-branch?branch= — delete a swarm/phase-<id>
 * branch whose id matches no phase row (work stranded under a previous id
 * generation). 200 {deleted, branch}; 409 for a branch outside the
 * swarm/phase-<id> namespace or one that belongs to a live phase row.
 *
 * A SIBLING of deletePhaseRunBranch, not a parameterisation of it: that route
 * derives the branch from the phase id and must stay incapable of naming an
 * arbitrary one. An orphan has no row to derive from, hence the explicit name.
 */
export async function deleteOrphanBranch(
  taskId: number,
  branch: string,
): Promise<{ deleted: boolean; branch: string }> {
  if (MOCK) return { deleted: true, branch };
  const res = await fetch(
    `/api/epics/${String(taskId)}/orphan-branch?branch=${encodeURIComponent(branch)}`,
    { method: 'DELETE' },
  );
  if (!res.ok) {
    const body = (await res.json().catch(() => ({}))) as {
      error?: string;
      code?: RunConflictCode;
    };
    throw runConflictError(body, `orphan branch delete failed (${String(res.status)})`);
  }
  return (await res.json()) as { deleted: boolean; branch: string };
}

/** POST /api/epics/{taskId}/phases/{phaseId}/run/cancel — 202 / 409 when idle. */
export async function cancelEpicPhaseRun(
  taskId: number,
  phaseId: number,
): Promise<{ status: string }> {
  if (MOCK) return { status: 'cancelling' };
  const res = await fetch(`/api/epics/${String(taskId)}/phases/${String(phaseId)}/run/cancel`, {
    method: 'POST',
  });
  if (!res.ok) {
    const body = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(body.error ?? `phase run cancel failed (${String(res.status)})`);
  }
  return (await res.json()) as { status: string };
}

/**
 * POST /api/epics/{taskId}/run — hand the WHOLE plan to one agent: a headless
 * session in an isolated worktree that drives core's run-plan skill. 202
 * {status, sessionUuid, agent, mode}; 409 carries the gate reason (already
 * running / a phase run holds the docs / plan not active / already complete) in
 * the error body — surfaced verbatim.
 *
 * The branch-holds-commits 409 is the one rejection that is not just a message:
 * it carries `branch` / `commitsAhead` / `base`, so it throws the same enriched
 * PhaseRunBranchError the phase run throws. Flattening it to `new Error(message)`
 * is what left the operator staring at a raw string with no way to act.
 */
export async function runEpicPlan(
  taskId: number,
  opts: { agent?: string; mode?: PlanRunMode } = {},
): Promise<{ status: string; sessionUuid: string; agent: string; mode: PlanRunMode }> {
  if (MOCK)
    return {
      status: 'running',
      sessionUuid: 'mock-plan-run-uuid',
      agent: opts.agent ?? 'tech-lead',
      mode: opts.mode ?? 'auto',
    };
  const res = await fetch(`/api/epics/${String(taskId)}/run`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ agent: opts.agent ?? '', mode: opts.mode ?? 'auto' }),
  });
  if (!res.ok) {
    const body = (await res.json().catch(() => ({}))) as {
      error?: string;
      code?: RunConflictCode;
      branch?: string;
      commitsAhead?: number;
      base?: string;
    };
    throw runConflictError(body, `plan run failed (${String(res.status)})`);
  }
  return (await res.json()) as {
    status: string;
    sessionUuid: string;
    agent: string;
    mode: PlanRunMode;
  };
}

/**
 * DELETE /api/epics/{taskId}/branch — destroy the plan's run branch INCLUDING
 * its commits, the explicit decision behind the branch-dirty 409. 200
 * {deleted, branch} where `deleted` reports whether the branch was actually
 * there (the delete is idempotent, so a no-op must not be claimed as a
 * deletion); 409 while a run owns it, it is checked out, or it is outside the
 * swarm/ namespace the daemon may delete.
 */
export async function deletePlanRunBranch(
  taskId: number,
): Promise<{ deleted: boolean; branch: string }> {
  if (MOCK) return { deleted: true, branch: 'swarm/plan-mock' };
  const res = await fetch(`/api/epics/${String(taskId)}/branch`, { method: 'DELETE' });
  if (!res.ok) {
    // Same enriched error the phase-scoped delete throws — the plan and phase
    // surfaces answer the same condition with the same `code` (runconflict.go),
    // so one client-side shape covers both.
    const body = (await res.json().catch(() => ({}))) as {
      error?: string;
      code?: RunConflictCode;
    };
    throw runConflictError(body, `branch delete failed (${String(res.status)})`);
  }
  return (await res.json()) as { deleted: boolean; branch: string };
}

/** POST /api/epics/{taskId}/run/cancel — 202 / 409 when idle. */
export async function cancelEpicPlanRun(taskId: number): Promise<{ status: string }> {
  if (MOCK) return { status: 'cancelling' };
  const res = await fetch(`/api/epics/${String(taskId)}/run/cancel`, { method: 'POST' });
  if (!res.ok) {
    const body = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(body.error ?? `plan run cancel failed (${String(res.status)})`);
  }
  return (await res.json()) as { status: string };
}

/** GET /api/epics/{taskId}/docs?path= — read a plan doc (path-confined). */
export function fetchPlanDoc(taskId: number, path: string): Promise<PlanDoc> {
  if (MOCK) return mockApi.planDoc(taskId, path);
  return get(`/api/epics/${String(taskId)}/docs?path=${encodeURIComponent(path)}`);
}

/** PUT /api/epics/{taskId}/docs?path= {content} — overwrite a plan doc (backup). */
export async function savePlanDoc(taskId: number, path: string, content: string): Promise<PlanDoc> {
  if (MOCK) return { path, content, backup: '.backups/mock/doc.md' };
  const res = await fetch(`/api/epics/${String(taskId)}/docs?path=${encodeURIComponent(path)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ content }),
  });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `save doc failed: ${String(res.status)}`);
  }
  return (await res.json()) as PlanDoc;
}

/**
 * PATCH /api/epics/{taskId}/docs?path= {line, done} — flip one checkbox by
 * 0-based line index (the exact `- [ ]`↔`- [x]` line).
 */
export async function togglePlanCheckbox(
  taskId: number,
  path: string,
  line: number,
  done: boolean,
): Promise<PlanDoc> {
  if (MOCK) return mockApi.togglePlanCheckbox(taskId, path, line, done);
  const res = await fetch(`/api/epics/${String(taskId)}/docs?path=${encodeURIComponent(path)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ line, done }),
  });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error ?? `toggle checkbox failed: ${String(res.status)}`);
  }
  return (await res.json()) as PlanDoc;
}

// --- verification contour (phase 2) ------------------------------------------

/** Per-agent first-pass success rate from trajectory_scores. */
export interface FirstPassRow {
  agent: string;
  sessions: number;
  firstPass: number;
  rate: number;
  /** Distinct anti-pattern kinds detected for this agent (for Retro chips). */
  kinds: string[];
}

export function fetchFirstPassRates(): Promise<FirstPassRow[]> {
  return get('/api/analytics/first-pass');
}

// --- verification contour (phase 2) — LLM-judge trajectory judgments ----------

/** Advisory LLM-judge verdict for one session × agent × model. Scores 1–5 (higher = better). */
export interface TrajectoryJudgment {
  agent: string;
  model: string;
  judgedAt: string;
  endResult: number;
  instructionCompliance: number;
  pitfalls: number;
  toolCalls: number;
  overall: number;
  review: string;
}

/** GET /api/analytics/trajectory-judgments?session=<id> — verdicts for one session.
 * Returns [] when no judgments have been recorded yet. */
export function fetchTrajectoryJudgments(sessionId: number): Promise<TrajectoryJudgment[]> {
  return get(`/api/analytics/trajectory-judgments?session=${String(sessionId)}`);
}
