package settingsoverlay

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile writes body to path, creating parents.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// descriptorFor writes a one-overlay descriptor and returns its path.
func descriptorFor(t *testing.T, name, settingsPath string, roots ...string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "overlays.json")
	body := `{"overlays":[{"name":"` + name + `","settingsPath":"` + settingsPath + `","roots":[`
	for i, r := range roots {
		if i > 0 {
			body += ","
		}
		body += `"` + r + `"`
	}
	body += `]}]}`
	writeFile(t, path, body)
	return path
}

func TestForCoversProjectUnderRoot(t *testing.T) {
	root := t.TempDir()
	settings := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, settings, `{"enabledPlugins":{"core@swarmery":true,"iot-pack@swarmery":true}}`)

	r := New(descriptorFor(t, "acme", settings, root))
	got := r.For(filepath.Join(root, "repo"))
	if len(got) != 1 {
		t.Fatalf("For() = %d overlays, want 1", len(got))
	}
	if got[0].Name != "acme" {
		t.Errorf("name = %q, want acme", got[0].Name)
	}
	if !got[0].Settings.EnabledPlugins["core@swarmery"] {
		t.Errorf("settings not parsed: %+v", got[0].Settings)
	}
	// The root itself is covered, not just descendants.
	if len(r.For(root)) != 1 {
		t.Error("the root directory itself must be covered")
	}
}

func TestForSkipsProjectOutsideRoots(t *testing.T) {
	root := t.TempDir()
	settings := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, settings, `{"enabledPlugins":{"core@swarmery":true}}`)

	r := New(descriptorFor(t, "acme", settings, root))
	// A sibling whose path merely shares a prefix STRING with the root must not
	// match — "/tmp/x-other" is not under "/tmp/x".
	if got := r.For(root + "-other"); len(got) != 0 {
		t.Errorf("For(sibling) = %d overlays, want 0", len(got))
	}
	if got := r.For(t.TempDir()); len(got) != 0 {
		t.Errorf("For(unrelated) = %d overlays, want 0", len(got))
	}
}

func TestForDegradesOnBadDescriptor(t *testing.T) {
	root := t.TempDir()
	cases := map[string]func() string{
		"missing": func() string { return filepath.Join(t.TempDir(), "nope.json") },
		"malformed": func() string {
			p := filepath.Join(t.TempDir(), "overlays.json")
			writeFile(t, p, `{"overlays": [ this is not json`)
			return p
		},
		"empty settingsPath": func() string { return descriptorFor(t, "acme", "", root) },
		"no roots":           func() string { return descriptorFor(t, "acme", "/nowhere/settings.json") },
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			if got := New(mk()).For(filepath.Join(root, "repo")); len(got) != 0 {
				t.Errorf("For() = %d overlays, want 0 (repo-only)", len(got))
			}
		})
	}
}

func TestForDroppedWhenSettingsPathDangles(t *testing.T) {
	root := t.TempDir()
	gone := filepath.Join(t.TempDir(), "moved-away", "settings.json")
	r := New(descriptorFor(t, "acme", gone, root))
	if got := r.For(filepath.Join(root, "repo")); len(got) != 0 {
		t.Fatalf("For() = %d overlays, want 0 for a dangling settingsPath", len(got))
	}
	// The warning must be memoised: a second call reports nothing new.
	r.For(filepath.Join(root, "repo"))
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.logged) != 1 {
		t.Errorf("logged %d distinct problems, want 1 (memoised)", len(r.logged))
	}
}

func TestNilReaderIsRepoOnly(t *testing.T) {
	var r *Reader
	if got := r.For("/anywhere"); got != nil {
		t.Errorf("nil Reader For() = %+v, want nil", got)
	}
	if r.Path() != "" {
		t.Errorf("nil Reader Path() = %q, want empty", r.Path())
	}
}

func TestReloadPicksUpDescriptorEdits(t *testing.T) {
	root := t.TempDir()
	settings := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, settings, `{"enabledPlugins":{"core@swarmery":true}}`)
	path := filepath.Join(t.TempDir(), "overlays.json")
	writeFile(t, path, `{"overlays":[]}`)

	r := New(path)
	if got := r.For(root); len(got) != 0 {
		t.Fatalf("precondition: For() = %d overlays, want 0", len(got))
	}
	// Rewrite with a real overlay. Size differs, so the (mtime,size) memo has to
	// notice even on a filesystem with coarse timestamps.
	writeFile(t, path, `{"overlays":[{"name":"acme","settingsPath":"`+settings+`","roots":["`+root+`"]}]}`)
	if got := r.For(root); len(got) != 1 {
		t.Errorf("For() after edit = %d overlays, want 1", len(got))
	}
}

func TestUnnamedOverlayStillHasProvenance(t *testing.T) {
	root := t.TempDir()
	settings := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, settings, `{"enabledPlugins":{"core@swarmery":true}}`)

	got := New(descriptorFor(t, "", settings, root)).For(root)
	if len(got) != 1 || got[0].Name != "overlay-1" {
		t.Errorf("For() = %+v, want one overlay named overlay-1", got)
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory on this machine")
	}
	if got := expandHome("~/x/y.json"); got != filepath.Join(home, "x", "y.json") {
		t.Errorf("expandHome(~/x/y.json) = %q", got)
	}
	if got := expandHome("~"); got != home {
		t.Errorf("expandHome(~) = %q, want %q", got, home)
	}
	// Not a home reference: left alone (this is a descriptor, not a shell).
	for _, in := range []string{"/abs/path", "~someone/x", "relative/x"} {
		if got := expandHome(in); got != in {
			t.Errorf("expandHome(%q) = %q, want unchanged", in, got)
		}
	}
}

func TestDefaultPathIsUnderHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory on this machine")
	}
	if want := filepath.Join(home, ".swarmery", "overlays.json"); DefaultPath() != want {
		t.Errorf("DefaultPath() = %q, want %q", DefaultPath(), want)
	}
	// New("") must fall back to it rather than watching nothing.
	if New("").Path() != DefaultPath() {
		t.Errorf("New(\"\").Path() = %q, want the default", New("").Path())
	}
}
