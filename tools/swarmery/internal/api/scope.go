package api

import "net/http"

// projectScopePredicate is the single source of truth for matching the global
// ?project=<slug|name|id> scope against a joined projects table (aliased p).
// It is appended to a WHERE clause and binds scopeArgCount args: the raw
// project query value repeated. Matches, in order: the path-derived slug
// (legacy deep links), the numeric id, and the kebab-cased display name — the
// pretty slug the SPA puts in URLs ("My App" → "my-app"; must mirror
// slugifyName in web/src/lib/projectSlug.ts). Query sites that build SQL by
// string concatenation use the const + scopeArgs directly; the rest go
// through scopeFilter.
var projectScopePredicate = ` AND ` + projectMatchExpr("p.")

// projectMatchExpr builds the slug|id|name match for a projects table under
// the given column alias prefix ("p." or "" for un-aliased lookups). Binds
// three placeholders — always pair with scopeArgs.
func projectMatchExpr(alias string) string {
	return `(` + alias + `slug = ? OR CAST(` + alias + `id AS TEXT) = ? OR lower(replace(COALESCE(` + alias + `name, ''), ' ', '-')) = lower(?))`
}

// scopeArgs binds projectMatchExpr's placeholders for one scope value.
func scopeArgs(project string) []any {
	return []any{project, project, project}
}

// scopeFilter resolves ?project=<slug|name|id> into a SQL predicate appended
// to a query that joins projects p — the same match rule everywhere a project
// scope applies (/api/sessions, /api/stats/*, /api/system/*, analytics).
// Empty when unscoped.
func scopeFilter(r *http.Request) (string, []any) {
	project := r.URL.Query().Get("project")
	if project == "" {
		return "", nil
	}
	return projectScopePredicate, scopeArgs(project)
}
