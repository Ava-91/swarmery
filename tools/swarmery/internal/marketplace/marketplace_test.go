package marketplace

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func writeManifest(t *testing.T, claudeDir, name, body string) {
	t.Helper()
	dir := filepath.Join(claudeDir, "plugins", "marketplaces", name, ".claude-plugin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "marketplace.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadHappyPath(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "swarmery", `{
		"name": "swarmery",
		"metadata": {"version": "1.13.0"},
		"plugins": [
			{"name": "core", "source": "./plugins/core", "description": "the core"},
			{"name": "lsp-pack", "source": "./plugins/lsp-pack", "description": "Serena LSP"}
		]
	}`)
	cat, err := Read(dir, "swarmery")
	if err != nil {
		t.Fatal(err)
	}
	if cat.Version != "1.13.0" {
		t.Errorf("version = %q, want 1.13.0", cat.Version)
	}
	if len(cat.Plugins) != 2 || cat.Plugins[0].Name != "core" || cat.Plugins[1].Name != "lsp-pack" {
		t.Errorf("plugins = %+v", cat.Plugins)
	}
	if cat.Plugins[1].Description != "Serena LSP" {
		t.Errorf("description = %q", cat.Plugins[1].Description)
	}
}

func TestReadMissingClone(t *testing.T) {
	_, err := Read(t.TempDir(), "swarmery")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want fs.ErrNotExist", err)
	}
}

func TestReadMalformedManifest(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "swarmery", `{not json`)
	if _, err := Read(dir, "swarmery"); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want parse error", err)
	}
}

// writePluginManifest writes <clone>/<source>/.claude-plugin/plugin.json.
func writePluginManifest(t *testing.T, claudeDir, name, source, body string) {
	t.Helper()
	dir := filepath.Join(claudeDir, "plugins", "marketplaces", name, filepath.Clean(source), ".claude-plugin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPluginVersion(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "testmkt", `{
		"name": "testmkt",
		"metadata": {"version": "2.4.0"},
		"plugins": [
			{"name": "core", "source": "./plugins/core", "description": "the core"},
			{"name": "ghost", "source": "./plugins/ghost", "description": "source dir absent"},
			{"name": "sourceless", "description": "no source field"}
		]
	}`)
	writePluginManifest(t, dir, "testmkt", "./plugins/core", `{"name": "core", "version": "2.4.0"}`)
	writePluginManifest(t, dir, "testmkt", "./plugins/broken", `{not json`)

	cat, err := Read(dir, "testmkt")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Plugin{}
	for _, p := range cat.Plugins {
		byName[p.Name] = p
	}
	if got := byName["core"].Source; got != "./plugins/core" {
		t.Errorf("core source = %q, want ./plugins/core", got)
	}
	if got := PluginVersion(dir, "testmkt", byName["core"]); got != "2.4.0" {
		t.Errorf("PluginVersion(core) = %q, want 2.4.0", got)
	}
	if got := PluginVersion(dir, "testmkt", byName["ghost"]); got != "" {
		t.Errorf("PluginVersion(ghost) = %q, want \"\" (absent source dir)", got)
	}
	if got := PluginVersion(dir, "testmkt", byName["sourceless"]); got != "" {
		t.Errorf("PluginVersion(sourceless) = %q, want \"\"", got)
	}
	if got := PluginVersion(dir, "testmkt", Plugin{Name: "broken", Source: "./plugins/broken"}); got != "" {
		t.Errorf("PluginVersion(broken) = %q, want \"\" (malformed manifest)", got)
	}
}

// ---------------------------------------------------------------------------
// known_marketplaces.json resolution. Everything above this line exercises the
// legacy layout with NO registry present — the exact shape a github-marketplace
// user's machine hands catalogRoot's fallback, which is why those tests staying
// unmodified is the compatibility proof for this feature.
// ---------------------------------------------------------------------------

// writeRegistry writes <claudeDir>/plugins/known_marketplaces.json verbatim, so
// a test can hand it a malformed body as easily as a valid one.
func writeRegistry(t *testing.T, claudeDir, body string) {
	t.Helper()
	dir := filepath.Join(claudeDir, "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, registryFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// jsonStr renders a filesystem path as a JSON string literal so temp-dir paths
// are escaped by encoding/json rather than by hand-concatenation.
func jsonStr(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// directoryRegistry builds a two-entry registry: the marketplace under test as a
// directory source rooted at root, plus an unrelated github entry, so the tests
// prove catalogRoot selects by key rather than taking whatever it finds first.
func directoryRegistry(t *testing.T, name, root string) string {
	t.Helper()
	return `{
		"nabu-org": {"source": {"source": "github", "repo": "nabu-org/packs"},
			"installLocation": "/nonexistent/nabu-org"},
		` + jsonStr(t, name) + `: {"source": {"source": "directory", "path": ` + jsonStr(t, root) + `},
			"installLocation": ` + jsonStr(t, root) + `}
	}`
}

// writeRootManifest writes <root>/.claude-plugin/marketplace.json — the layout of
// a directory-source marketplace, which lives outside claudeDir entirely.
func writeRootManifest(t *testing.T, root, body string) {
	t.Helper()
	dir := filepath.Join(root, ".claude-plugin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "marketplace.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeRootPluginManifest writes <root>/<source>/.claude-plugin/plugin.json.
func writeRootPluginManifest(t *testing.T, root, source, body string) {
	t.Helper()
	dir := filepath.Join(root, filepath.Clean(source), ".claude-plugin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const twoPackManifest = `{
	"name": "swarmery",
	"metadata": {"version": "1.13.0"},
	"plugins": [
		{"name": "core", "source": "./plugins/core", "description": "the core"},
		{"name": "lsp-pack", "source": "./plugins/lsp-pack", "description": "Serena LSP"}
	]
}`

// (b) A github entry's installLocation IS the legacy clone path, so routing a
// github user through the registry must land on exactly the same catalog.
func TestReadRegistryGithubEntryMatchesLegacyPath(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "swarmery", twoPackManifest)
	legacy := filepath.Join(dir, "plugins", "marketplaces", "swarmery")
	writeRegistry(t, dir, `{"swarmery": {"source": {"source": "github", "repo": "o/r"},
		"installLocation": `+jsonStr(t, legacy)+`}}`)

	cat, err := Read(dir, "swarmery")
	if err != nil {
		t.Fatal(err)
	}
	if cat.Version != "1.13.0" || len(cat.Plugins) != 2 || cat.Plugins[0].Name != "core" {
		t.Errorf("catalog = %+v, want the same catalog the legacy-only path returns", cat)
	}
	if got := catalogRoot(dir, "swarmery"); got != legacy {
		t.Errorf("catalogRoot = %q, want the legacy path %q", got, legacy)
	}
}

// (c) A directory entry roots the catalog outside claudeDir entirely, with
// nothing under plugins/marketplaces — the reported-404 machine's shape.
func TestReadRegistryDirectoryEntryResolvesOutsideClaudeDir(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	writeRootManifest(t, root, twoPackManifest)
	writeRootPluginManifest(t, root, "./plugins/core", `{"name": "core", "version": "1.13.0"}`)
	writeRegistry(t, dir, directoryRegistry(t, "swarmery", root))

	cat, err := Read(dir, "swarmery")
	if err != nil {
		t.Fatalf("Read = %v, want the directory-source catalog", err)
	}
	if cat.Version != "1.13.0" || len(cat.Plugins) != 2 {
		t.Errorf("catalog = %+v, want 2 plugins at version 1.13.0", cat)
	}
	if got := PluginVersion(dir, "swarmery", cat.Plugins[0]); got != "1.13.0" {
		t.Errorf("PluginVersion(core) = %q, want 1.13.0 (resolved under the checkout root)", got)
	}
	// Nothing was ever created under the legacy layout.
	if _, err := os.Stat(filepath.Join(dir, "plugins", "marketplaces")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stat plugins/marketplaces = %v, want the fixture to leave it absent", err)
	}
}

// (d) A corrupt registry must not take the legacy path down with it.
func TestReadRegistryCorruptFallsBackToLegacy(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "swarmery", twoPackManifest)
	writeRegistry(t, dir, `{not json`)

	cat, err := Read(dir, "swarmery")
	if err != nil {
		t.Fatalf("Read = %v, want the legacy catalog", err)
	}
	if cat.Version != "1.13.0" {
		t.Errorf("version = %q, want 1.13.0", cat.Version)
	}
}

// (e) A registry that simply does not mention this marketplace is not evidence
// against the legacy clone.
func TestReadRegistryWithoutEntryFallsBackToLegacy(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "swarmery", twoPackManifest)
	writeRegistry(t, dir, `{"nabu-org": {"source": {"source": "github", "repo": "nabu-org/packs"},
		"installLocation": "/nonexistent/nabu-org"}}`)

	cat, err := Read(dir, "swarmery")
	if err != nil {
		t.Fatalf("Read = %v, want the legacy catalog", err)
	}
	if cat.Version != "1.13.0" {
		t.Errorf("version = %q, want 1.13.0", cat.Version)
	}
}

// (f) The stale-entry guard. A registry pointing at a deleted checkout must
// surface fs.ErrNotExist EVEN THOUGH a legacy clone is sitting right there —
// falling back would serve provably stale catalog data from a previous add.
func TestReadRegistryStaleEntryDoesNotFallBackToStaleClone(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "swarmery", twoPackManifest) // the leftover clone
	gone := filepath.Join(t.TempDir(), "checkout-that-was-deleted")
	writeRegistry(t, dir, directoryRegistry(t, "swarmery", gone))

	if _, err := Read(dir, "swarmery"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want fs.ErrNotExist (never the leftover clone)", err)
	}
}

// (g) A relative installLocation would resolve against the daemon's CWD.
func TestReadRegistryRelativeInstallLocationFallsBackToLegacy(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "swarmery", twoPackManifest)
	writeRegistry(t, dir, `{"swarmery": {"source": {"source": "directory", "path": "relative/path"},
		"installLocation": "relative/path"}}`)

	cat, err := Read(dir, "swarmery")
	if err != nil {
		t.Fatalf("Read = %v, want the legacy catalog", err)
	}
	if cat.Version != "1.13.0" {
		t.Errorf("version = %q, want 1.13.0", cat.Version)
	}
	if got, want := catalogRoot(dir, "swarmery"), filepath.Join(dir, "plugins", "marketplaces", "swarmery"); got != want {
		t.Errorf("catalogRoot = %q, want %q", got, want)
	}
}

// (h) Any read error on the registry — here it is a directory, not a file — is
// treated as "no registry".
func TestReadRegistryUnreadableFallsBackToLegacy(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "swarmery", twoPackManifest)
	if err := os.MkdirAll(filepath.Join(dir, "plugins", registryFile), 0o755); err != nil {
		t.Fatal(err)
	}

	cat, err := Read(dir, "swarmery")
	if err != nil {
		t.Fatalf("Read = %v, want the legacy catalog", err)
	}
	if cat.Version != "1.13.0" {
		t.Errorf("version = %q, want 1.13.0", cat.Version)
	}
}

// Catalog.Root is what lets a caller outside this package reach a pack's own
// files at filepath.Join(Root, plugin.Source). Both resolution branches are
// asserted, because a Root that silently fell back to the legacy path on a
// directory-source machine would send those callers looking in a directory that
// does not exist — the same class of bug as the reported catalog 404.
func TestReadExposesCatalogRootLegacyPath(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "swarmery", twoPackManifest)

	cat, err := Read(dir, "swarmery")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "plugins", "marketplaces", "swarmery")
	if cat.Root != want {
		t.Errorf("Root = %q, want the legacy clone path %q", cat.Root, want)
	}
	// The point of exposing Root: a pack's files resolve under it.
	if got := filepath.Join(cat.Root, cat.Plugins[0].Source); got != filepath.Join(want, "plugins", "core") {
		t.Errorf("pack dir = %q, want it under the clone root", got)
	}
}

func TestReadExposesCatalogRootFromRegistry(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	writeRootManifest(t, root, twoPackManifest)
	writeRegistry(t, dir, directoryRegistry(t, "swarmery", root))

	cat, err := Read(dir, "swarmery")
	if err != nil {
		t.Fatal(err)
	}
	if cat.Root != root {
		t.Errorf("Root = %q, want installLocation %q from known_marketplaces.json", cat.Root, root)
	}
	// Nothing under the legacy layout — Root came from the registry, not a guess.
	if _, err := os.Stat(filepath.Join(dir, "plugins", "marketplaces")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stat plugins/marketplaces = %v, want it absent", err)
	}
}

// (i) A directory-source manifest lives in a tree the operator edits, so a
// hand-written `..` source must not walk PluginVersion out of the root — proved
// by planting a readable plugin.json exactly where the escape would land.
func TestPluginVersionRejectsParentTraversal(t *testing.T) {
	dir := t.TempDir()
	base := t.TempDir()
	root := filepath.Join(base, "x", "y")
	writeRootManifest(t, root, twoPackManifest)
	writeRegistry(t, dir, directoryRegistry(t, "swarmery", root))
	// filepath.Join(root, "../../etc") == <base>/etc — reachable without the guard.
	writeRootPluginManifest(t, base, "./etc", `{"name": "escaped", "version": "9.9.9"}`)

	if got := PluginVersion(dir, "swarmery", Plugin{Name: "escaped", Source: "../../etc"}); got != "" {
		t.Errorf("PluginVersion(../../etc) = %q, want \"\" (traversal refused)", got)
	}
}
