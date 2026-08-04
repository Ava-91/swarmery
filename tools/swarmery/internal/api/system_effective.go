package api

// Effective component scope — what a project ACTUALLY resolves at runtime.
//
// The registry tables are machine-wide: `scope='project'` rows belong to ONE
// project's .claude/, `scope='global' origin='local'` rows are the user's own
// ~/.claude/ (they apply to every project), and `origin='plugin'` rows come from
// the plugin cache and only apply where their pack is ENABLED. A project page
// therefore cannot be served by a plain project_id filter (that hides everything
// the project inherits) nor by the unfiltered registry (that leaks OTHER
// projects' local components).
//
// effectiveScope is the third answer, and the same rule effectiveTemplates
// (system_hub.go) already applies to templates:
//
//	global-local  ∪  plugin rows of enabled packs (core always)  ∪  own rows
//
// Every project-scoped System surface (rosters, counts, insights) resolves the
// scope through this one type so the badge counts equal the visible roster BY
// CONSTRUCTION.

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/projectscan"
)

// effectiveScope is a resolved project scope: its id plus the plugin packs whose
// components it resolves. A nil *effectiveScope means "unscoped" (fleet mode) —
// every predicate method degrades to an empty fragment, so callers never branch.
type effectiveScope struct {
	projectID int64
	// projectSlug is the DB path slug, for payloads that carry slugs rather than
	// ids (plugin-drift findings resolve to a project by path, not by join).
	projectSlug string
	// packs are the plugin names whose rows are effective here, always including
	// "core" (a swarmery consumer resolves core built-ins even when the pack list
	// is unreadable — mirroring effectiveTemplates).
	packs []string
}

// resolveEffectiveScope turns a ?project= / ?projectId= reference (path slug,
// numeric id or kebab name — the shared projectMatchExpr rule) into a scope.
// An empty or unresolvable reference returns (nil, nil): the caller then serves
// the unscoped fleet view, which is the existing "unknown project" behaviour.
func (h *Handler) resolveEffectiveScope(ref string) (*effectiveScope, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, nil
	}
	var id int64
	var path, slug string
	err := h.DB.QueryRow(
		`SELECT id, path, slug FROM projects WHERE `+projectMatchExpr("")+` AND archived = 0`,
		scopeArgs(ref)...).Scan(&id, &path, &slug)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// Overlay-aware, like effectiveTemplates: a pack a declared settings overlay
	// switches on is resolvable in that project's sessions, so it is effective.
	packs := []string{"core"}
	if st, serr := projectscan.ReadPluginState(path, nil, overlaysFor(path)...); serr == nil && st != nil {
		for _, name := range st.Packs {
			if name != "core" {
				packs = append(packs, name)
			}
		}
	}
	return &effectiveScope{projectID: id, projectSlug: slug, packs: packs}, nil
}

// predicate returns the ` AND (…)` fragment selecting the rows effective for the
// scope, plus its bind args. alias is the item table's alias WITH the dot
// ("t."). hasOrigin tells whether the table carries origin/plugin_name — hooks
// do not (they are parsed out of settings.json files, never shipped by a pack),
// so for them "effective" is global ∪ own.
//
// The zero scope (nil receiver) yields an empty fragment: fleet mode is
// unfiltered, exactly as before.
func (e *effectiveScope) predicate(alias string, hasOrigin bool) (string, []any) {
	if e == nil {
		return "", nil
	}
	own := `(` + alias + `scope = 'project' AND ` + alias + `project_id = ?)`
	if !hasOrigin {
		return ` AND (` + alias + `scope = 'global' OR ` + own + `)`, []any{e.projectID}
	}
	holes, args := e.packHoles()
	args = append(args, e.projectID)
	return ` AND ((` + alias + `origin = 'local' AND ` + alias + `scope = 'global')` +
		` OR (` + alias + `origin = 'plugin' AND ` + alias + `plugin_name IN (` + holes + `))` +
		` OR ` + own + `)`, args
}

// packHoles renders the ?-list binding e.packs, for predicates that constrain a
// plugin_name column. Never called on a nil receiver.
func (e *effectiveScope) packHoles() (string, []any) {
	holes := make([]string, len(e.packs))
	args := make([]any, len(e.packs))
	for i, p := range e.packs {
		holes[i] = "?"
		args[i] = p
	}
	return strings.Join(holes, ", "), args
}

// scoped reports whether a concrete project resolved (nil-safe).
func (e *effectiveScope) scoped() bool { return e != nil }

// slug returns the resolved project's DB path slug, "" when unscoped (nil-safe).
func (e *effectiveScope) slug() string {
	if e == nil {
		return ""
	}
	return e.projectSlug
}
