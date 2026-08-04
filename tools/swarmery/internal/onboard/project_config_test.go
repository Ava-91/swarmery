package onboard

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── WriteProjectConfig ───────────────────────────────────────────────────────
//
// These cases are deliberately destructive in shape: this is the one function
// in the daemon that rewrites a file the operator owns and hand-edits, and the
// failure that matters is not "the write did not happen" but "the write
// happened and took something else with it". Hence byte comparisons against a
// literal expected file rather than reflect.DeepEqual over decoded maps — a map
// comparison cannot see the reordering or the reformatting that would actually
// hurt.

// realOverlay is the shape a live .claude/project.json has: ten top-level keys,
// mixed scalars/objects/arrays, 2-space indentation, trailing newline. Modelled
// on overlays/example/project.json and on a real multi-repo overlay.
const realOverlay = `{
  "$schema": "../_schema/project.schema.json",
  "name": "example",
  "displayName": "Example Project",
  "codePath": "/path/to/your/project",
  "mainApp": "web-app",
  "apps": [
    "web-app",
    "marketing-site"
  ],
  "repos": [
    "apps/web-app",
    "infrastructure"
  ],
  "cloud": {
    "provider": "gcp",
    "region": "your-region"
  },
  "stack": {
    "web": "Next.js + TypeScript",
    "db": "PostgreSQL + an ORM"
  },
  "enabledPacks": [
    "iot-pack",
    "web-pack"
  ]
}
`

// writeOverlayFile seeds <dir>/.claude/project.json and returns the project dir.
func writeOverlayFile(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	claude := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude, "project.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func overlayPath(dir string) string { return filepath.Join(dir, ".claude", "project.json") }

// assertReportedBackup takes ConfigWriteResult.Backup at its word: it is a
// recovery instruction, so the path it names — relative to the project dir, or
// absolute — must resolve to a file that exists and holds the pre-write bytes.
// A reported path nobody can open is worse than no report at all.
func assertReportedBackup(t *testing.T, projectDir, reported, wantBody string) {
	t.Helper()
	if reported == "" {
		t.Fatal("Backup is empty")
	}
	path := reported
	if !filepath.IsAbs(path) {
		path = filepath.Join(projectDir, reported)
	}
	if got := readFileString(t, path); got != wantBody {
		t.Errorf("reported backup %q holds:\n%s\n--- want the pre-write contents ---\n%s",
			reported, got, wantBody)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// The headline case. A brand-new key lands at the end and NOTHING else moves:
// the assertion is the whole file, byte for byte.
func TestWriteProjectConfigPreservesEveryForeignKey(t *testing.T) {
	dir := writeOverlayFile(t, realOverlay)

	res, err := WriteProjectConfig(dir, "tracker",
		json.RawMessage(`{"baseUrl":"acme.example.net","repro":{"test":"make test"}}`))
	if err != nil {
		t.Fatalf("WriteProjectConfig: %v", err)
	}
	if !res.Changed {
		t.Error("Changed = false, want true for a key that was not there")
	}
	if res.Backup != ".claude/project.json.bak" {
		t.Errorf("Backup = %q, want .claude/project.json.bak", res.Backup)
	}

	want := `{
  "$schema": "../_schema/project.schema.json",
  "name": "example",
  "displayName": "Example Project",
  "codePath": "/path/to/your/project",
  "mainApp": "web-app",
  "apps": [
    "web-app",
    "marketing-site"
  ],
  "repos": [
    "apps/web-app",
    "infrastructure"
  ],
  "cloud": {
    "provider": "gcp",
    "region": "your-region"
  },
  "stack": {
    "web": "Next.js + TypeScript",
    "db": "PostgreSQL + an ORM"
  },
  "enabledPacks": [
    "iot-pack",
    "web-pack"
  ],
  "tracker": {
    "baseUrl": "acme.example.net",
    "repro": {
      "test": "make test"
    }
  }
}
`
	if got := readFileString(t, overlayPath(dir)); got != want {
		t.Errorf("project.json after write:\n%s\n--- want ---\n%s", got, want)
	}
}

// The backup must hold what was there BEFORE, or it is not a backup.
func TestWriteProjectConfigBackupHoldsPreviousContents(t *testing.T) {
	dir := writeOverlayFile(t, realOverlay)
	if _, err := WriteProjectConfig(dir, "tracker", json.RawMessage(`{"a":1}`)); err != nil {
		t.Fatalf("WriteProjectConfig: %v", err)
	}
	if got := readFileString(t, overlayPath(dir)+".bak"); got != realOverlay {
		t.Errorf(".bak =\n%s\n--- want the pre-write contents ---\n%s", got, realOverlay)
	}

	// A second write overwrites the .bak with the state before THAT write: the
	// freshest backup is the useful one, and attach reads exactly that.
	afterFirst := readFileString(t, overlayPath(dir))
	if _, err := WriteProjectConfig(dir, "tracker", json.RawMessage(`{"a":2}`)); err != nil {
		t.Fatalf("second WriteProjectConfig: %v", err)
	}
	if got := readFileString(t, overlayPath(dir)+".bak"); got != afterFirst {
		t.Errorf(".bak after the second write =\n%s\n--- want the first write's output ---\n%s", got, afterFirst)
	}
}

// Replacing an existing key must not relocate it — a diff that moves a block to
// the bottom of the file is a diff nobody can review.
func TestWriteProjectConfigReplacementKeepsItsPosition(t *testing.T) {
	dir := writeOverlayFile(t, `{
  "name": "example",
  "tracker": {
    "baseUrl": "old.example.net"
  },
  "enabledPacks": [
    "web-pack"
  ]
}
`)
	if _, err := WriteProjectConfig(dir, "tracker",
		json.RawMessage(`{"baseUrl":"new.example.net","projectKey":"ABC"}`)); err != nil {
		t.Fatalf("WriteProjectConfig: %v", err)
	}

	want := `{
  "name": "example",
  "tracker": {
    "baseUrl": "new.example.net",
    "projectKey": "ABC"
  },
  "enabledPacks": [
    "web-pack"
  ]
}
`
	if got := readFileString(t, overlayPath(dir)); got != want {
		t.Errorf("project.json after replace:\n%s\n--- want ---\n%s", got, want)
	}
}

// Numbers survive the round trip verbatim. A decode/encode merge would turn
// every integer into a float and every large one into scientific notation.
func TestWriteProjectConfigDoesNotReformatUntouchedValues(t *testing.T) {
	dir := writeOverlayFile(t, `{
  "budget": {
    "maxFiles": 5,
    "ratio": 0.50,
    "big": 10000000000000000000
  },
  "name": "example"
}
`)
	if _, err := WriteProjectConfig(dir, "name", json.RawMessage(`{"a":"b"}`)); err != nil {
		t.Fatalf("WriteProjectConfig: %v", err)
	}
	got := readFileString(t, overlayPath(dir))
	for _, lit := range []string{`"maxFiles": 5`, `"ratio": 0.50`, `"big": 10000000000000000000`} {
		if !strings.Contains(got, lit) {
			t.Errorf("untouched literal %s was reformatted:\n%s", lit, got)
		}
	}
}

// Writing the value that is already there is not a change — but the file is
// still rewritten, so a hand-mangled indentation gets normalised either way.
func TestWriteProjectConfigIdenticalValueReportsUnchanged(t *testing.T) {
	dir := writeOverlayFile(t, `{
  "tracker": {
    "baseUrl": "acme.example.net"
  }
}
`)
	res, err := WriteProjectConfig(dir, "tracker",
		json.RawMessage(`{ "baseUrl" : "acme.example.net" }`))
	if err != nil {
		t.Fatalf("WriteProjectConfig: %v", err)
	}
	if res.Changed {
		t.Error("Changed = true for a byte-equivalent value, want false")
	}
	want := `{
  "tracker": {
    "baseUrl": "acme.example.net"
  }
}
`
	if got := readFileString(t, overlayPath(dir)); got != want {
		t.Errorf("project.json =\n%s\n--- want ---\n%s", got, want)
	}
}

// A hand-edited overlay can carry the same key twice. Leaving the second copy
// would let the stale value win on the next read — the opposite of what the
// operator just asked for.
func TestWriteProjectConfigCollapsesDuplicateKeys(t *testing.T) {
	dir := writeOverlayFile(t, `{"tracker":{"v":1},"name":"example","tracker":{"v":2}}`)
	res, err := WriteProjectConfig(dir, "tracker", json.RawMessage(`{"v":3}`))
	if err != nil {
		t.Fatalf("WriteProjectConfig: %v", err)
	}
	if !res.Changed {
		t.Error("Changed = false, want true")
	}
	want := `{
  "tracker": {
    "v": 3
  },
  "name": "example"
}
`
	if got := readFileString(t, overlayPath(dir)); got != want {
		t.Errorf("project.json =\n%s\n--- want ---\n%s", got, want)
	}
}

// The daemon does not conjure an overlay out of one key: a project.json built
// from a single pack's block would be missing every field the rest of the
// toolchain reads. That is attach's job, and scripts/init.sh's.
func TestWriteProjectConfigMissingFileIsRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := WriteProjectConfig(dir, "tracker", json.RawMessage(`{"a":1}`))
	if !errors.Is(err, ErrNoProjectConfig) {
		t.Fatalf("err = %v, want ErrNoProjectConfig", err)
	}
	if _, serr := os.Stat(overlayPath(dir)); !os.IsNotExist(serr) {
		t.Error("project.json was created; the daemon must not bootstrap an overlay")
	}
}

// A file we cannot parse is a file whose foreign keys we cannot promise to
// preserve, so it is never overwritten.
func TestWriteProjectConfigMalformedFileIsNeverOverwritten(t *testing.T) {
	for name, body := range map[string]string{
		"invalid JSON":        `{not json`,
		"a JSON array":        `["not", "an", "object"]`,
		"a bare string":       `"nope"`,
		"truncated object":    `{"name": "example"`,
		"non-string key form": `{42: 1}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := writeOverlayFile(t, body)
			_, err := WriteProjectConfig(dir, "tracker", json.RawMessage(`{"a":1}`))
			if !errors.Is(err, ErrBadProjectConfig) {
				t.Fatalf("err = %v, want ErrBadProjectConfig", err)
			}
			if got := readFileString(t, overlayPath(dir)); got != body {
				t.Errorf("file was modified:\n%s\n--- want it untouched ---\n%s", got, body)
			}
			if _, serr := os.Stat(overlayPath(dir) + ".bak"); serr == nil {
				t.Error("a .bak was written for a file that was never rewritten")
			}
		})
	}
}

// The multi-repo consumer overlay pattern: <project>/.claude is a symlink into
// a shared agents repo. The merge has to land in the symlink TARGET, and the
// temp file has to be created there too — a temp file beside the symlink would
// make os.Rename cross a filesystem boundary.
func TestWriteProjectConfigFollowsSymlinkedClaudeDir(t *testing.T) {
	shared := t.TempDir()
	agents := filepath.Join(shared, "agents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agents, "project.json"), []byte(realOverlay), 0o644); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if err := os.Symlink(agents, filepath.Join(project, ".claude")); err != nil {
		t.Fatal(err)
	}

	res, err := WriteProjectConfig(project, "tracker", json.RawMessage(`{"baseUrl":"acme.example.net"}`))
	if err != nil {
		t.Fatalf("WriteProjectConfig through a symlink: %v", err)
	}

	// The write reached the shared repo's real file...
	target := filepath.Join(agents, "project.json")
	realTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != realTarget {
		t.Errorf("Path = %q, want the symlink target %q", res.Path, realTarget)
	}
	if body := readFileString(t, target); !strings.Contains(body, "acme.example.net") {
		t.Errorf("symlink target was not written:\n%s", body)
	}
	if body := readFileString(t, target); !strings.Contains(body, `"$schema"`) {
		t.Errorf("symlink target lost its foreign keys:\n%s", body)
	}
	// ...the .bak sits beside it, in the shared repo...
	if _, serr := os.Stat(target + ".bak"); serr != nil {
		t.Errorf("backup not written to the symlink target: %v", serr)
	}
	// ...and the path REPORTED for it reaches that same file: the .bak is
	// reachable at .claude/project.json.bak through the symlinked directory, so
	// the friendly relative path is still the honest one.
	if res.Backup != ".claude/project.json.bak" {
		t.Errorf("Backup = %q, want .claude/project.json.bak", res.Backup)
	}
	assertReportedBackup(t, project, res.Backup, realOverlay)
	// ...and no temp file was left behind in the destination directory.
	entries, err := os.ReadDir(agents)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
	if len(entries) != 2 {
		t.Errorf("agents dir = %d entries, want exactly project.json + project.json.bak", len(entries))
	}
}

// project.json itself being the symlink is the other half of the same pattern.
func TestWriteProjectConfigFollowsSymlinkedFile(t *testing.T) {
	shared := t.TempDir()
	target := filepath.Join(shared, "project.json")
	if err := os.WriteFile(target, []byte(realOverlay), 0o644); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	claude := filepath.Join(project, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(claude, "project.json")); err != nil {
		t.Fatal(err)
	}

	res, err := WriteProjectConfig(project, "tracker", json.RawMessage(`{"a":1}`))
	if err != nil {
		t.Fatalf("WriteProjectConfig: %v", err)
	}
	// The .bak lands beside the TARGET, and <project>/.claude/project.json.bak
	// does not exist at all — reporting the friendly relative path here would
	// send the operator to a file that is not there.
	if _, serr := os.Stat(filepath.Join(claude, "project.json.bak")); !os.IsNotExist(serr) {
		t.Fatalf("stat .claude/project.json.bak = %v, want it absent for a symlinked FILE", serr)
	}
	realTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if res.Backup != realTarget+".bak" {
		t.Errorf("Backup = %q, want the real backup %q", res.Backup, realTarget+".bak")
	}
	assertReportedBackup(t, project, res.Backup, realOverlay)
	// The symlink survives as a symlink — a rename over it would have replaced
	// it with a regular file and detached the shared repo.
	fi, err := os.Lstat(filepath.Join(claude, "project.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a regular file")
	}
	if body := readFileString(t, target); !strings.Contains(body, `"tracker"`) {
		t.Errorf("symlink target was not written:\n%s", body)
	}
}

// The overlay is read by the operator and by agent-work.sh, so its mode is not
// the daemon's to tighten — os.CreateTemp's 0600 must not survive the rename.
func TestWriteProjectConfigKeepsFileMode(t *testing.T) {
	dir := writeOverlayFile(t, realOverlay)
	if err := os.Chmod(overlayPath(dir), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteProjectConfig(dir, "tracker", json.RawMessage(`{"a":1}`)); err != nil {
		t.Fatalf("WriteProjectConfig: %v", err)
	}
	fi, err := os.Stat(overlayPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o640 {
		t.Errorf("mode = %o, want 0640 preserved", got)
	}
}

func TestWriteProjectConfigRejectsBadArguments(t *testing.T) {
	dir := writeOverlayFile(t, realOverlay)
	for name, tc := range map[string]struct {
		key   string
		value json.RawMessage
	}{
		"empty key":          {"", json.RawMessage(`{"a":1}`)},
		"invalid JSON value": {"tracker", json.RawMessage(`{not json`)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := WriteProjectConfig(dir, tc.key, tc.value); err == nil {
				t.Fatal("err = nil, want a refusal")
			}
			if got := readFileString(t, overlayPath(dir)); got != realOverlay {
				t.Errorf("file was modified:\n%s", got)
			}
		})
	}
}

// An unreadable overlay is neither "absent" nor "malformed" — it must surface as
// itself rather than be mistaken for a project that was never attached.
func TestWriteProjectConfigUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	dir := writeOverlayFile(t, realOverlay)
	if err := os.Chmod(overlayPath(dir), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(overlayPath(dir), 0o644) })

	_, err := WriteProjectConfig(dir, "tracker", json.RawMessage(`{"a":1}`))
	if err == nil {
		t.Fatal("err = nil, want a read failure")
	}
	if errors.Is(err, ErrNoProjectConfig) {
		t.Errorf("err = %v, want a read error rather than ErrNoProjectConfig", err)
	}
}

// A destination directory that cannot take a temp file must fail loudly and
// leave the original in place, backup included.
func TestWriteProjectConfigUnwritableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := writeOverlayFile(t, realOverlay)
	claude := filepath.Join(dir, ".claude")
	if err := os.Chmod(claude, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(claude, 0o755) })

	if _, err := WriteProjectConfig(dir, "tracker", json.RawMessage(`{"a":1}`)); err == nil {
		t.Fatal("err = nil, want a write failure")
	}
	if got := readFileString(t, overlayPath(dir)); got != realOverlay {
		t.Errorf("original was damaged:\n%s", got)
	}
}
