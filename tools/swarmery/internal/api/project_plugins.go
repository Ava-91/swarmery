package api

// phase: projects — GET /api/projects/{id}/plugins merges the swarmery
// marketplace catalog (the clone under ~/.claude/plugins/marketplaces/swarmery,
// read via internal/marketplace) with the project's enabledPlugins state
// (projectscan.ReadPluginState). Read-only and unfenced; the canWrite flag
// tells the UI whether the PUT fence (step 03, same file) would admit a write.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
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
	enabledCore, enabledPacks := false, []string{}
	if st, serr := projectscan.ReadPluginState(path, nil); serr == nil && st != nil {
		enabledCore = st.Managed
		enabledPacks = st.Packs
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
	}
	seen := map[string]bool{}
	for _, p := range cat.Plugins {
		seen[p.Name] = true
		enabled := (p.Name == "core" && enabledCore) || slices.Contains(enabledPacks, p.Name)
		// A disabled plugin is "unknown", not "ok": no finding is expected for it
		// either way, so claiming health would be an assertion nothing checked.
		status, detail := "ok", ""
		if !enabled {
			status = "unknown"
		} else if d, ok := drift[p.Name]; ok {
			status, detail = d.status, d.detail
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

type repairPluginResponse struct {
	ID      string `json:"id"`
	Action  string `json:"action"` // install | update
	Output  string `json:"output"`
	Status  string `json:"status"` // recomputed after the run
	Restart bool   `json:"restart"`
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

	out, rerr := pluginRepairer.Run(r.Context(), path, "plugin", action, pluginID, "--scope", "project")
	resp := repairPluginResponse{ID: pluginID, Action: action, Output: string(out), Restart: true}
	if rerr != nil {
		resp.Output = strings.TrimSpace(resp.Output + "\n" + rerr.Error())
		writeJSONStatus(w, http.StatusBadGateway, resp)
		return
	}
	// The recomputed status reads the findings table, which the ticker refreshes
	// every 5 minutes — so right after a repair it usually still reports the
	// pre-repair status. That is honest: the plugin genuinely is not loaded
	// until Claude Code restarts, which is what Restart tells the UI to say.
	if again, aerr := driftStatus(h.DB, path); aerr == nil {
		if d, ok := again[name]; ok {
			resp.Status = d.status
		} else {
			resp.Status = "ok"
		}
	}
	writeJSON(w, resp, nil)
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
