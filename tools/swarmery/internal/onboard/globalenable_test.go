package onboard

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeGlobalSettings(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func readEnabled(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("settings.json is not valid JSON after restore: %v", err)
	}
	ep, _ := d["enabledPlugins"].(map[string]any)
	return ep
}

// The headline case: the key was absent, the CLI's user-scope install added it,
// Restore must delete it again — otherwise repairing one symlinked-overlay
// consumer silently enables the pack for every project on the machine.
func TestRestoreDeletesKeyTheInstallAdded(t *testing.T) {
	dir := t.TempDir()
	p := writeGlobalSettings(t, dir, `{"enabledPlugins":{"core@swarmery":true}}`)

	snap, err := CaptureGlobalEnable(dir, "infra-pack@swarmery")
	if err != nil {
		t.Fatalf("CaptureGlobalEnable: %v", err)
	}
	// simulate `claude plugin install infra-pack@swarmery` at user scope
	writeGlobalSettings(t, dir, `{"enabledPlugins":{"core@swarmery":true,"infra-pack@swarmery":true}}`)

	if err := snap.Restore(); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	ep := readEnabled(t, p)
	if _, present := ep["infra-pack@swarmery"]; present {
		t.Errorf("infra-pack@swarmery survived the restore: %v", ep)
	}
	if ep["core@swarmery"] != true {
		t.Errorf("foreign key core@swarmery was lost: %v", ep)
	}
}

// A key the user had already set on purpose must come back with its value, not
// be deleted — restore is "put it back", not "remove it".
func TestRestorePutsBackPreExistingValue(t *testing.T) {
	for _, want := range []bool{true, false} {
		dir := t.TempDir()
		body := `{"enabledPlugins":{"infra-pack@swarmery":` + map[bool]string{true: "true", false: "false"}[want] + `}}`
		p := writeGlobalSettings(t, dir, body)

		snap, err := CaptureGlobalEnable(dir, "infra-pack@swarmery")
		if err != nil {
			t.Fatalf("CaptureGlobalEnable: %v", err)
		}
		writeGlobalSettings(t, dir, `{"enabledPlugins":{"infra-pack@swarmery":true}}`)

		if err := snap.Restore(); err != nil {
			t.Fatalf("Restore: %v", err)
		}
		if got := readEnabled(t, p)["infra-pack@swarmery"]; got != want {
			t.Errorf("value = %v, want the captured %v", got, want)
		}
	}
}

// Restore re-reads the file rather than writing the captured document back, so
// a key another writer added meanwhile (the provision engine also shells out to
// user-scope installs) must survive.
func TestRestorePreservesConcurrentForeignWrite(t *testing.T) {
	dir := t.TempDir()
	p := writeGlobalSettings(t, dir, `{"enabledPlugins":{"core@swarmery":true}}`)

	snap, err := CaptureGlobalEnable(dir, "infra-pack@swarmery")
	if err != nil {
		t.Fatalf("CaptureGlobalEnable: %v", err)
	}
	// our install, plus a concurrent one by another writer
	writeGlobalSettings(t, dir, `{"enabledPlugins":{"core@swarmery":true,"infra-pack@swarmery":true,"uav-pack@swarmery":true}}`)

	if err := snap.Restore(); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	ep := readEnabled(t, p)
	if _, present := ep["infra-pack@swarmery"]; present {
		t.Errorf("our key survived: %v", ep)
	}
	if ep["uav-pack@swarmery"] != true {
		t.Errorf("concurrent write was clobbered: %v", ep)
	}
}

// Non-enabledPlugins settings must be untouched — this file holds permissions,
// statusLine, env and more.
func TestRestorePreservesForeignTopLevelKeys(t *testing.T) {
	dir := t.TempDir()
	p := writeGlobalSettings(t, dir, `{"statusLine":{"type":"command"},"enabledPlugins":{}}`)

	snap, err := CaptureGlobalEnable(dir, "infra-pack@swarmery")
	if err != nil {
		t.Fatalf("CaptureGlobalEnable: %v", err)
	}
	writeGlobalSettings(t, dir, `{"statusLine":{"type":"command"},"enabledPlugins":{"infra-pack@swarmery":true}}`)
	if err := snap.Restore(); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	raw, _ := os.ReadFile(p)
	var d map[string]any
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	if _, ok := d["statusLine"].(map[string]any); !ok {
		t.Errorf("statusLine was lost: %s", raw)
	}
}

// Nothing changed ⇒ nothing written. Keeps the common no-op off the mtime and
// leaves no stray .bak.
func TestRestoreNoOpWritesNothing(t *testing.T) {
	dir := t.TempDir()
	p := writeGlobalSettings(t, dir, `{"enabledPlugins":{"core@swarmery":true}}`)

	snap, err := CaptureGlobalEnable(dir, "infra-pack@swarmery")
	if err != nil {
		t.Fatalf("CaptureGlobalEnable: %v", err)
	}
	if err := snap.Restore(); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := os.Stat(p + ".bak"); !os.IsNotExist(err) {
		t.Errorf("a no-op restore wrote a backup")
	}
}

// A snapshot that cannot be trusted must stop the caller BEFORE the install —
// the alternative is a global enable nobody can revert.
func TestCaptureRefusesMalformedSettings(t *testing.T) {
	dir := t.TempDir()
	writeGlobalSettings(t, dir, `{"enabledPlugins": [broken`)

	if _, err := CaptureGlobalEnable(dir, "infra-pack@swarmery"); !errors.Is(err, ErrGlobalSettingsUnreadable) {
		t.Fatalf("err = %v, want ErrGlobalSettingsUnreadable", err)
	}
}

func TestCaptureRefusesEnabledPluginsOfWrongShape(t *testing.T) {
	dir := t.TempDir()
	writeGlobalSettings(t, dir, `{"enabledPlugins": ["core@swarmery"]}`)

	if _, err := CaptureGlobalEnable(dir, "infra-pack@swarmery"); !errors.Is(err, ErrGlobalSettingsUnreadable) {
		t.Fatalf("err = %v, want ErrGlobalSettingsUnreadable", err)
	}
}

// No settings.json yet is a normal first-install state, not an error; Restore
// then deletes the key from whatever the CLI created.
func TestCaptureMissingFileThenRestoreDeletes(t *testing.T) {
	dir := t.TempDir()
	snap, err := CaptureGlobalEnable(dir, "infra-pack@swarmery")
	if err != nil {
		t.Fatalf("CaptureGlobalEnable on a missing file: %v", err)
	}
	p := writeGlobalSettings(t, dir, `{"enabledPlugins":{"infra-pack@swarmery":true}}`)
	if err := snap.Restore(); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, present := readEnabled(t, p)["infra-pack@swarmery"]; present {
		t.Errorf("key survived a restore captured from a missing file")
	}
}
