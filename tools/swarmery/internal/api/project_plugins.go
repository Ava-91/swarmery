package api

// phase: projects — GET /api/projects/{id}/plugins merges the swarmery
// marketplace catalog (the clone under ~/.claude/plugins/marketplaces/swarmery,
// read via internal/marketplace) with the project's enabledPlugins state
// (projectscan.ReadPluginState). Read-only and unfenced; the canWrite flag
// tells the UI whether the PUT fence (step 03, same file) would admit a write.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/marketplace"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/onboard"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/plugindrift"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/projectscan"
)

// pluginMarketplace is the only marketplace this surface manages — matches
// projectscan's marketplaceSuffix ("@swarmery") view of enabledPlugins.
const pluginMarketplace = "swarmery"

// overlayStatusDetail explains a row that is enabled by a declared settings
// overlay: it is live in real sessions, but the drift detector never looked at
// it (see internal/plugindrift/ticker.go loadProjects for why it stays
// repo-scoped), so its status is "unknown" rather than a green "ok".
const overlayStatusDetail = "enabled by a settings overlay — drift is only scanned from this repo's settings.json"

// pluginCatalogDir is attached once at startup (or per-test); empty ⇒ resolve
// ~/.claude at request time. Mirrors AttachOnboard (onboard.go:41).
var pluginCatalogDir string

// AttachPluginCatalog points the project-plugins endpoints at the directory
// holding plugins/marketplaces/ (production: ~/.claude; tests: a temp dir).
func AttachPluginCatalog(claudeDir string) { pluginCatalogDir = claudeDir }

func catalogDir() (string, error) {
	if pluginCatalogDir != "" {
		return pluginCatalogDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

type projectPluginDTO struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	// Locked marks plugins this surface refuses to toggle: core's lifecycle is
	// attach/detach (hooks + statusline + project.json travel with it).
	Locked bool `json:"locked"`
	// Status is the drift verdict from the plugin_* findings:
	// ok | missing | behind | orphaned | unknown ("unknown" = the plugin is
	// disabled here, so no finding is expected either way).
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// projectPluginDrift is the winning finding for one plugin in one project.
type projectPluginDrift struct{ status, detail string }

// driftStatus maps active plugin_* findings for one project onto the per-row
// status. Rule precedence is severity order: a missing plugin outranks a
// version-behind one, which outranks an orphaned cache dir.
func driftStatus(db *sql.DB, projectPath string) (map[string]projectPluginDrift, error) {
	rows, err := db.Query(
		`SELECT target, rule, message FROM config_lint_findings
		  WHERE resolved_at IS NULL AND rule LIKE 'plugin\_%' ESCAPE '\'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]projectPluginDrift{}
	for rows.Next() {
		var target, rule, message string
		if err := rows.Scan(&target, &rule, &message); err != nil {
			return nil, err
		}
		id, path, ok := plugindrift.ParseTarget(target)
		if !ok || path != projectPath {
			continue
		}
		name, _, _ := strings.Cut(id, "@")
		st, ok := statusForRule(rule)
		if !ok {
			continue // plugin_note / plugin_detector_unavailable are not row statuses
		}
		if cur, exists := out[name]; exists && rank(cur.status) >= rank(st) {
			continue
		}
		out[name] = projectPluginDrift{status: st, detail: message}
	}
	return out, rows.Err()
}

func statusForRule(rule string) (string, bool) {
	switch rule {
	case plugindrift.RuleEnabledNotInstalled:
		return "missing", true
	case plugindrift.RuleVersionBehind:
		return "behind", true
	case plugindrift.RuleCacheOrphaned:
		return "orphaned", true
	}
	return "", false
}

func rank(status string) int {
	switch status {
	case "missing":
		return 3
	case "orphaned":
		return 2
	case "behind":
		return 1
	}
	return 0
}

type projectPluginsResponse struct {
	MarketplaceVersion string `json:"marketplaceVersion"`
	// MarketplaceName lets the client build the "<name>@<marketplace>" id the
	// repair endpoint takes, instead of hard-coding the marketplace in React.
	MarketplaceName string             `json:"marketplaceName"`
	CanWrite        bool               `json:"canWrite"`
	Plugins         []projectPluginDTO `json:"plugins"`
	// OverlaySources names the declared settings overlays that contributed to
	// the enabled state below (internal/settingsoverlay). Omitted when the repo's
	// own settings.json is the whole story — its presence is the reader's only
	// clue that this project's plugin set does not live in its repo.
	OverlaySources []string `json:"overlaySources,omitempty"`
}

// projectPlugins handles GET /api/projects/{id}/plugins.
func (h *Handler) projectPlugins(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, `{"error":"invalid project id"}`, http.StatusBadRequest)
		return
	}
	var path string
	err = h.DB.QueryRow(`SELECT path FROM projects WHERE id = ?`, id).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, `{"error":"project not found"}`, http.StatusNotFound)
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}

	cdir, err := catalogDir()
	if err != nil {
		writeErr(w, err)
		return
	}
	cat, err := marketplace.Read(cdir, pluginMarketplace)
	if errors.Is(err, fs.ErrNotExist) {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{
			"error": "swarmery marketplace is not installed on this machine — run a Claude Code marketplace update",
		})
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}

	// Enabled state: Managed covers core, Packs the domain packs. A nil state
	// (telemetry-only project, unreadable settings) renders everything off.
	// roots=nil: UnderOnboardRoot is unused here — canWrite is derived
	// separately below via resolveUnderRoots.
	//
	// Declared settings overlays are folded in: for a project whose plugin set
	// is injected by a launcher at CLI precedence, the repo alone would report
	// every row disabled while every session runs them.
	overlays := overlaysFor(path)
	enabledCore, enabledPacks, overlaySources := false, []string{}, []string(nil)
	if st, serr := projectscan.ReadPluginState(path, nil, overlays...); serr == nil && st != nil {
		enabledCore = st.Managed
		enabledPacks = st.Packs
		overlaySources = st.OverlaySources
	}
	// Drift is scanned from the repo's own settings only
	// (internal/plugindrift/ticker.go loadProjects), so a plugin that is enabled
	// ONLY by an overlay has no finding either way — and "no finding" must not
	// render as a checked "ok" for it, for the same reason a disabled row is
	// "unknown". Repo-enabled plugins keep their real status even when an
	// overlay names them too, hence the subtraction rather than a plain lookup.
	fromOverlay, repoEnabled := overlayPacks(overlays), map[string]bool{}
	if ids, ierr := projectscan.ReadEnabledPlugins(path); ierr == nil {
		for _, id := range ids {
			if name, mkt, _ := strings.Cut(id, "@"); mkt == pluginMarketplace {
				repoEnabled[name] = true
			}
		}
	}

	// canWrite mirrors the attach/detach fence (attach.go:42-87): roots must be
	// configured AND the project path must resolve under one of them.
	canWrite := false
	if len(onboardCfg.Roots) > 0 {
		if _, ferr := resolveUnderRoots(path, onboardCfg.Roots); ferr == nil {
			canWrite = true
		}
	}

	drift, derr := driftStatus(h.DB, path)
	if derr != nil {
		writeErr(w, derr)
		return
	}

	resp := projectPluginsResponse{
		MarketplaceVersion: cat.Version,
		MarketplaceName:    pluginMarketplace,
		CanWrite:           canWrite,
		Plugins:            []projectPluginDTO{},
		OverlaySources:     overlaySources,
	}
	seen := map[string]bool{}
	for _, p := range cat.Plugins {
		seen[p.Name] = true
		enabled := (p.Name == "core" && enabledCore) || slices.Contains(enabledPacks, p.Name)
		// A disabled plugin is "unknown", not "ok": no finding is expected for it
		// either way, so claiming health would be an assertion nothing checked.
		status, detail := "ok", ""
		switch {
		case !enabled:
			status = "unknown"
		case fromOverlay[p.Name] && !repoEnabled[p.Name]:
			status, detail = "unknown", overlayStatusDetail
		default:
			if d, ok := drift[p.Name]; ok {
				status, detail = d.status, d.detail
			}
		}
		resp.Plugins = append(resp.Plugins, projectPluginDTO{
			Name: p.Name, Description: p.Description,
			Enabled: enabled, Locked: p.Name == "core",
			Status: status, Detail: detail,
		})
	}
	// Enabled-but-unknown packs (stale clone) must stay visible. This used to
	// smuggle its explanation into Description; it now folds into the same
	// status model as every other row.
	for _, name := range enabledPacks {
		if !seen[name] {
			detail := "enabled here, but missing from the local marketplace clone — refresh marketplaces"
			if d, ok := drift[name]; ok {
				detail = d.detail
			}
			resp.Plugins = append(resp.Plugins, projectPluginDTO{
				Name: name, Enabled: true, Status: "missing", Detail: detail,
			})
		}
	}
	writeJSON(w, resp, nil)
}

// pluginRepairer runs the claude CLI for repair actions; attached once at
// startup, stubbed in tests, nil when the binary could not be resolved.
var pluginRepairer plugindrift.Runner

// AttachPluginRepairer points the repair endpoint at a CLI runner.
func AttachPluginRepairer(r plugindrift.Runner) { pluginRepairer = r }

// isSymlinkedClaudeDir reports whether projectPath/.claude is itself a symlink
// — the multi-repo consumer overlay pattern (a shared agents/ repo with each
// consuming repo's .claude symlinked into it; see CLAUDE.md's Self-hosting
// section and EXTENDING.md). Lstat (not Stat) so the symlink itself is
// inspected rather than followed; any error (missing/unreadable) reports false
// so the normal repair path still runs and surfaces its own error.
func isSymlinkedClaudeDir(projectPath string) bool {
	fi, err := os.Lstat(filepath.Join(projectPath, ".claude"))
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}

// pluginsClaudeDir anchors the user-level settings.json that the user-scope
// repair fallback has to revert. Injectable for tests.
var pluginsClaudeDir = defaultMemoryClaudeDir()

type repairPluginResponse struct {
	ID     string `json:"id"`
	Action string `json:"action"` // install | update
	// Scope is which scope the CLI was actually asked for: "project" normally,
	// "user" for the symlinked-overlay fallback. The UI shows it because the
	// two are not equivalent — see repairViaUserScope.
	Scope   string `json:"scope"`
	Output  string `json:"output"`
	Status  string `json:"status"` // recomputed after the run
	Restart bool   `json:"restart"`
	// Warning is set when the repair succeeded but left the machine in a state
	// the operator must know about (currently: the global enable could not be
	// reverted). Empty on a clean run.
	Warning string `json:"warning,omitempty"`
}

// repairViaUserScope is the repair path for a project whose .claude is a
// symlinked overlay, where `--scope project` cannot run at all.
//
// It installs at USER scope, which resolves the drift (plugindrift.resolveFor
// counts a Scope=="user" entry as available to every project), then reverts the
// enabledPlugins key the CLI sets in the user settings.json as a side effect —
// otherwise repairing one consumer would switch the pack on for every project
// on the machine. See internal/onboard/globalenable.go for why the revert keeps
// the install drift-resolving.
//
// The revert runs even when the CLI failed: a partial install can still have
// written the key.
func repairViaUserScope(ctx context.Context, pluginID, action string) (repairPluginResponse, int) {
	resp := repairPluginResponse{ID: pluginID, Action: action, Scope: "user", Restart: true}

	snap, cerr := onboard.CaptureGlobalEnable(pluginsClaudeDir, pluginID)
	if cerr != nil {
		// Refuse rather than risk an irreversible global enable.
		resp.Restart = false
		resp.Output = "refused: " + cerr.Error() + " — a user-scope install would enable " +
			pluginID + " for every project on this machine, and swarmery could not " +
			"guarantee reverting that. Fix the user settings.json and retry."
		return resp, http.StatusConflict
	}

	out, rerr := pluginRepairer.Run(ctx, "", "plugin", action, pluginID)
	resp.Output = string(out)

	if resErr := snap.Restore(); resErr != nil {
		log.Printf("repair: user-scope %s of %s left the global enable in place: %v", action, pluginID, resErr)
		resp.Warning = "installed, but the global enable in " + pluginsClaudeDir +
			"/settings.json could not be reverted (" + resErr.Error() + ") — " + pluginID +
			" is now enabled for every project until you remove that key by hand"
	}
	if rerr != nil {
		resp.Output = strings.TrimSpace(resp.Output + "\n" + rerr.Error())
		return resp, http.StatusBadGateway
	}
	return resp, http.StatusOK
}

// repairProjectPlugin handles POST /api/projects/{id}/plugins/{name}/repair.
// Fenced exactly like the PUT below: requireLocalOrigin at the route, plus the
// project path resolving under a configured onboard root.
//
// The action is derived server-side from the current drift status, so a client
// cannot ask for an install where an update is what is needed.
func (h *Handler) repairProjectPlugin(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, `{"error":"invalid project id"}`, http.StatusBadRequest)
		return
	}
	var path string
	err = h.DB.QueryRow(`SELECT path FROM projects WHERE id = ?`, id).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, `{"error":"project not found"}`, http.StatusNotFound)
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	if len(onboardCfg.Roots) == 0 {
		writeJSONStatus(w, http.StatusForbidden, map[string]string{
			"error": "read-only — daemon started without SWARMERY_ONBOARD_ROOTS",
		})
		return
	}
	if _, ferr := resolveUnderRoots(path, onboardCfg.Roots); ferr != nil {
		writeJSONStatus(w, http.StatusForbidden, map[string]string{
			"error": "project is outside the configured onboard roots",
		})
		return
	}
	if pluginRepairer == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]string{
			"error": "repair unavailable — the claude CLI was not resolved at startup",
		})
		return
	}

	pluginID := r.PathValue("name")
	if !strings.Contains(pluginID, "@") {
		http.Error(w, `{"error":"plugin must be given as name@marketplace"}`, http.StatusBadRequest)
		return
	}
	name, _, _ := strings.Cut(pluginID, "@")

	drift, derr := driftStatus(h.DB, path)
	if derr != nil {
		writeErr(w, derr)
		return
	}
	action := "install"
	if d, ok := drift[name]; ok && d.status == "behind" {
		action = "update"
	}

	// Multi-repo consumer overlay (CLAUDE.md / EXTENDING.md): <project>/.claude
	// is itself a symlink into a shared agents/ repo. The claude CLI refuses to
	// write project-scope settings through a symlinked .claude directory
	// (SymlinkWriteRefusedError), so `--scope project` can never succeed there;
	// fall back to a user-scope install that resolves the drift without writing
	// through the symlink at all.
	if isSymlinkedClaudeDir(path) {
		resp, code := repairViaUserScope(r.Context(), pluginID, action)
		if code == http.StatusOK {
			resp.Status = recomputedDriftStatus(h.DB, path, name)
		}
		writeJSONStatus(w, code, resp)
		return
	}

	out, rerr := pluginRepairer.Run(r.Context(), path, "plugin", action, pluginID, "--scope", "project")
	resp := repairPluginResponse{ID: pluginID, Action: action, Scope: "project", Output: string(out), Restart: true}
	if rerr != nil {
		resp.Output = strings.TrimSpace(resp.Output + "\n" + rerr.Error())
		writeJSONStatus(w, http.StatusBadGateway, resp)
		return
	}
	resp.Status = recomputedDriftStatus(h.DB, path, name)
	writeJSON(w, resp, nil)
}

// recomputedDriftStatus re-reads the drift status after a repair.
//
// It forces a drift pass first. The findings table is otherwise only refreshed
// by the 5-minute ticker, so the response — and the list the UI refetches right
// after it — would still carry the PRE-repair status, leaving the row unchanged
// and the repair button in place. A successful repair that renders as "nothing
// happened" is indistinguishable from a broken one.
//
// Synchronous, unlike the session-start hook's fire-and-forget refresh
// (prockill.go): that path has a 2s budget and nothing to render, whereas here
// the freshly scanned status IS the response. One pass costs a single
// `claude plugin list --json` (~300ms), paid only on an explicit button press.
//
// A read error leaves the status empty rather than asserting "ok".
func recomputedDriftStatus(db *sql.DB, path, name string) string {
	if driftRefresher != nil {
		driftRefresher()
	}
	again, err := driftStatus(db, path)
	if err != nil {
		return ""
	}
	if d, ok := again[name]; ok {
		return d.status
	}
	return "ok"
}

type putPluginRequest struct {
	Enabled bool `json:"enabled"`
}

type putPluginResponse struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Changed bool   `json:"changed"`
	Backup  string `json:"backup,omitempty"`
}

// putProjectPlugin handles PUT /api/projects/{id}/plugins/{name}. Fenced like
// attach: requireLocalOrigin at the route, SWARMERY_ONBOARD_ROOTS here,
// resolveUnderRoots before the write.
func (h *Handler) putProjectPlugin(w http.ResponseWriter, r *http.Request) {
	if len(onboardCfg.Roots) == 0 {
		writeJSONStatus(w, http.StatusForbidden, map[string]string{
			"error": "plugin toggles are disabled — start the daemon with SWARMERY_ONBOARD_ROOTS set to the allowed parent directories",
		})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, `{"error":"invalid project id"}`, http.StatusBadRequest)
		return
	}
	name := r.PathValue("name")
	// onboard.TogglePlugin has its own ErrCoreLocked guard; this check exists to
	// answer 400 before any I/O — neither is redundant, keep both.
	if name == "core" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "core is managed via attach/detach"})
		return
	}
	var req putPluginRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}

	var path string
	err = h.DB.QueryRow(`SELECT path FROM projects WHERE id = ?`, id).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, `{"error":"project not found"}`, http.StatusNotFound)
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	target, err := resolveUnderRoots(path, onboardCfg.Roots)
	if err != nil {
		writeJSONStatus(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}

	// Enabling requires the pack to exist in the catalog; disabling does not
	// (a stale clone must not trap an enabled pack in the on state).
	if req.Enabled {
		cdir, cerr := catalogDir()
		if cerr != nil {
			writeErr(w, cerr)
			return
		}
		cat, cerr := marketplace.Read(cdir, pluginMarketplace)
		if cerr != nil {
			writeErr(w, cerr)
			return
		}
		known := false
		for _, p := range cat.Plugins {
			if p.Name == name {
				known = true
				break
			}
		}
		if !known {
			writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "unknown plugin: " + name})
			return
		}
	}

	res, err := onboard.TogglePlugin(target, name, req.Enabled)
	switch {
	case errors.Is(err, onboard.ErrCoreLocked):
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	case errors.Is(err, onboard.ErrNoSettings), errors.Is(err, onboard.ErrBadSettings):
		writeJSONStatus(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	case err != nil:
		writeErr(w, err)
		return
	}
	// Auto-provision: only on a real enable (a no-op re-enable or any disable
	// must not kick off install/generate). Best-effort — enqueueProvision never
	// blocks or fails the toggle response.
	if res.Changed && req.Enabled {
		h.enqueueProvision(id, target, name)
	}
	writeJSON(w, putPluginResponse{Name: name, Enabled: req.Enabled, Changed: res.Changed, Backup: res.Backup}, nil)
}
