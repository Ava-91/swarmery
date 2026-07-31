package projectscan

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeSettings writes a .claude/settings.json under dir with body.
func writeSettings(t *testing.T, dir, body string) {
	t.Helper()
	claude := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude, "settings.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPluginState_Managed(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{
		"extraKnownMarketplaces": {"swarmery": {"source": {"source": "github", "repo": "atretyak1985/swarmery"}}},
		"enabledPlugins": {"core@swarmery": true, "iot-pack@swarmery": true, "web-pack@swarmery": false, "other@elsewhere": true}
	}`)

	st, err := ReadPluginState(dir, []string{filepath.Dir(dir)})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if st == nil {
		t.Fatal("want non-nil state")
	}
	if !st.Managed {
		t.Error("want Managed=true")
	}
	if !reflect.DeepEqual(st.Packs, []string{"iot-pack"}) {
		t.Errorf("packs = %v, want [iot-pack] (disabled + non-swarmery excluded)", st.Packs)
	}
	if st.Marketplace != "atretyak1985/swarmery" {
		t.Errorf("marketplace = %q", st.Marketplace)
	}
	if !st.UnderOnboardRoot {
		t.Error("want UnderOnboardRoot=true (project is directly under the root)")
	}
}

func TestPluginState_NotManaged_NoSwarmery(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"enabledPlugins": {"other@elsewhere": true}}`)

	st, err := ReadPluginState(dir, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if st == nil {
		t.Fatal("settings present but no swarmery → non-nil, Managed=false")
	}
	if st.Managed || len(st.Packs) != 0 {
		t.Errorf("want unmanaged empty, got %+v", st)
	}
	if st.UnderOnboardRoot {
		t.Error("nil roots → UnderOnboardRoot must be false")
	}
}

func TestPluginState_NoSettings(t *testing.T) {
	st, err := ReadPluginState(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if st != nil {
		t.Errorf("missing settings.json → nil state, got %+v", st)
	}
}

func TestPluginState_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{not valid json`)

	st, err := ReadPluginState(dir, nil)
	if err != nil {
		t.Fatalf("malformed settings must not error the list: %v", err)
	}
	if st != nil {
		t.Errorf("malformed settings → nil state, got %+v", st)
	}
}

func TestPluginState_OutsideRoot(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"enabledPlugins": {"core@swarmery": true}}`)

	st, err := ReadPluginState(dir, []string{"/some/unrelated/root"})
	if err != nil {
		t.Fatal(err)
	}
	if st.UnderOnboardRoot {
		t.Error("project not under the root → UnderOnboardRoot=false")
	}
}

func TestComponents(t *testing.T) {
	dir := t.TempDir()
	claude := filepath.Join(dir, ".claude")
	mkdir := func(p string) {
		if err := os.MkdirAll(filepath.Join(claude, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p string) {
		if err := os.WriteFile(filepath.Join(claude, p), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mkdir("agents")
	write("agents/reviewer.md")
	write("agents/planner.md")
	write("agents/notes.txt") // ignored: not .md
	mkdir("skills/deploy")    // skill = directory
	mkdir("skills/lint")
	mkdir("commands")
	write("commands/ship.md")
	mkdir("hooks")
	write("hooks/pretooluse.sh")

	c, err := ReadComponents(dir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if c.Counts != (ComponentCounts{Agents: 2, Skills: 2, Commands: 1, Hooks: 1}) {
		t.Errorf("counts = %+v", c.Counts)
	}
	// sorted, extension stripped, source tagged local
	if len(c.Agents) != 2 || c.Agents[0].Name != "planner" || c.Agents[0].Source != "local" {
		t.Errorf("agents = %+v", c.Agents)
	}
	if c.Skills[0].Name != "deploy" {
		t.Errorf("skills = %+v", c.Skills)
	}
}

func TestComponents_MissingDirs(t *testing.T) {
	c, err := ReadComponents(t.TempDir())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// non-nil empty slices so JSON renders [] not null
	if c.Agents == nil || c.Skills == nil || c.Commands == nil || c.Hooks == nil {
		t.Errorf("want empty non-nil slices, got %+v", c)
	}
	if c.Counts != (ComponentCounts{}) {
		t.Errorf("counts = %+v, want all zero", c.Counts)
	}
}

func TestReadEnabledPlugins_AllMarketplaces(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{
		"enabledPlugins": {
			"core@swarmery": true,
			"superpowers@claude-plugins-official": true,
			"web-pack@swarmery": false,
			"malformed-key-without-marketplace": true
		}
	}`)

	ids, err := ReadEnabledPlugins(dir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"core@swarmery", "superpowers@claude-plugins-official"}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}
}

func TestReadEnabledPlugins_MissingAndMalformed(t *testing.T) {
	missing := t.TempDir()
	ids, err := ReadEnabledPlugins(missing)
	if ids != nil || err != nil {
		t.Errorf("missing settings: (%v, %v), want (nil, nil)", ids, err)
	}

	bad := t.TempDir()
	writeSettings(t, bad, `{not json`)
	ids, err = ReadEnabledPlugins(bad)
	if ids != nil || err != nil {
		t.Errorf("malformed settings: (%v, %v), want (nil, nil)", ids, err)
	}
}

// overlay is a test-local shorthand for a declared settings overlay.
func overlay(name string, enabled map[string]bool, marketplaces map[string]string) SettingsOverlay {
	return SettingsOverlay{Name: name, Settings: Settings{EnabledPlugins: enabled, Marketplaces: marketplaces}}
}

func TestPluginState_OverlayEnablesEmptyRepo(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{}`)

	st, err := ReadPluginState(dir, nil, overlay("acme",
		map[string]bool{"core@swarmery": true, "iot-pack@swarmery": true},
		map[string]string{"swarmery": "owner/swarmery"}))
	if err != nil || st == nil {
		t.Fatalf("ReadPluginState = (%v, %v), want a state", st, err)
	}
	if !st.Managed {
		t.Error("want Managed=true from the overlay")
	}
	if !reflect.DeepEqual(st.Packs, []string{"iot-pack"}) {
		t.Errorf("packs = %v, want [iot-pack]", st.Packs)
	}
	if st.Marketplace != "owner/swarmery" {
		t.Errorf("marketplace = %q, want the overlay's", st.Marketplace)
	}
	if !reflect.DeepEqual(st.OverlaySources, []string{"acme"}) {
		t.Errorf("overlaySources = %v, want [acme]", st.OverlaySources)
	}
}

// TestPluginState_OverlayWithoutRepoSettings: no .claude/settings.json at all
// used to mean (nil, nil). With a covering overlay there IS state to report.
func TestPluginState_OverlayWithoutRepoSettings(t *testing.T) {
	st, err := ReadPluginState(t.TempDir(), nil,
		overlay("acme", map[string]bool{"core@swarmery": true}, nil))
	if err != nil || st == nil {
		t.Fatalf("ReadPluginState = (%v, %v), want a state", st, err)
	}
	if !st.Managed || len(st.OverlaySources) != 1 {
		t.Errorf("state = %+v, want Managed=true with one overlay source", st)
	}
}

// TestPluginState_OverlayWinsOnConflict pins precedence: an overlay rides at
// CLI precedence in a real session, so it outranks the repo per key — later
// overlays outrank earlier ones for the same reason.
func TestPluginState_OverlayWinsOnConflict(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{
		"extraKnownMarketplaces": {"swarmery": {"source": {"repo": "repo/one"}}},
		"enabledPlugins": {"core@swarmery": true, "iot-pack@swarmery": true}
	}`)

	st, err := ReadPluginState(dir, nil,
		overlay("first", map[string]bool{"iot-pack@swarmery": false, "web-pack@swarmery": false}, nil),
		overlay("second", map[string]bool{"web-pack@swarmery": true}, map[string]string{"swarmery": "repo/two"}))
	if err != nil || st == nil {
		t.Fatalf("ReadPluginState = (%v, %v), want a state", st, err)
	}
	if !st.Managed {
		t.Error("core came from the repo and no overlay touched it — want Managed=true")
	}
	if !reflect.DeepEqual(st.Packs, []string{"web-pack"}) {
		t.Errorf("packs = %v, want [web-pack] (iot-pack off by overlay, web-pack on by the later one)", st.Packs)
	}
	if st.Marketplace != "repo/two" {
		t.Errorf("marketplace = %q, want repo/two (overlay wins)", st.Marketplace)
	}
	if !reflect.DeepEqual(st.OverlaySources, []string{"first", "second"}) {
		t.Errorf("overlaySources = %v, want [first second]", st.OverlaySources)
	}
}

// TestPluginState_EmptyOverlayIsNotProvenance: an overlay that declares nothing
// changed nothing, so naming it would be provenance for a non-event.
func TestPluginState_EmptyOverlayIsNotProvenance(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"enabledPlugins": {"core@swarmery": true}}`)

	st, err := ReadPluginState(dir, nil, overlay("empty", nil, nil))
	if err != nil || st == nil {
		t.Fatalf("ReadPluginState = (%v, %v), want a state", st, err)
	}
	if len(st.OverlaySources) != 0 {
		t.Errorf("overlaySources = %v, want none", st.OverlaySources)
	}
	if !st.Managed {
		t.Error("the repo's own state must survive an empty overlay")
	}
}

func TestReadSettings_ReportsFailure(t *testing.T) {
	if _, err := ReadSettings(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Error("ReadSettings on a missing file must report the error (the overlay reader logs it once)")
	}
	bad := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(bad, []byte(`{nope`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSettings(bad); err == nil {
		t.Error("ReadSettings on malformed JSON must report the error")
	}
}
