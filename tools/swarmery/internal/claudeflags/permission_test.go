package claudeflags

import (
	"strings"
	"testing"
)

const siteEnv = "SWARMERY_TESTSITE_PERMISSION_MODE"

// The default is the whole point of the package: an unconfigured headless spawn
// must get a mode that lets it write, run and commit. Assert the literal flag
// pair, because this is what lands in argv.
func TestPermissionModeArgs_DefaultIsBypass(t *testing.T) {
	t.Setenv(siteEnv, "")
	t.Setenv(ModeEnv, "")

	got := PermissionModeArgs(siteEnv)
	want := []string{"--permission-mode", "bypassPermissions"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("PermissionModeArgs() = %q, want %q", got, want)
	}
}

func TestPermissionModeArgs_SiteEnvBeatsGlobal(t *testing.T) {
	t.Setenv(ModeEnv, "acceptEdits")
	t.Setenv(siteEnv, "plan")

	got := PermissionModeArgs(siteEnv)
	if len(got) != 2 || got[1] != "plan" {
		t.Fatalf("PermissionModeArgs() = %q, want mode plan (site knob wins)", got)
	}
}

func TestPermissionModeArgs_GlobalUsedWhenSiteUnset(t *testing.T) {
	t.Setenv(siteEnv, "")
	t.Setenv(ModeEnv, "acceptEdits")

	got := PermissionModeArgs(siteEnv)
	if len(got) != 2 || got[1] != "acceptEdits" {
		t.Fatalf("PermissionModeArgs() = %q, want mode acceptEdits from %s", got, ModeEnv)
	}
}

// The escape hatch must produce NO flag — not an empty value, which `claude`
// would reject.
func TestPermissionModeArgs_OmitSpellings(t *testing.T) {
	for _, spelling := range []string{"off", "OFF", "none", "default"} {
		t.Run(spelling, func(t *testing.T) {
			t.Setenv(ModeEnv, "")
			t.Setenv(siteEnv, spelling)
			if got := PermissionModeArgs(siteEnv); got != nil {
				t.Fatalf("PermissionModeArgs() = %q, want nil (flag omitted)", got)
			}
		})
	}
}

// A typo must not reach the CLI: `claude` exits on an unknown choice, which
// would turn it into a run that never starts.
func TestPermissionModeArgs_InvalidFallsBackToDefault(t *testing.T) {
	t.Setenv(ModeEnv, "")
	t.Setenv(siteEnv, "bypassPermision") // one 's' short

	got := PermissionModeArgs(siteEnv)
	if len(got) != 2 || got[1] != DefaultMode {
		t.Fatalf("PermissionModeArgs() = %q, want the default %q", got, DefaultMode)
	}
}

// Case-insensitive acceptance, canonical spelling out: operators type
// bypasspermissions, the CLI only takes bypassPermissions.
func TestPermissionModeArgs_CanonicalizesCase(t *testing.T) {
	t.Setenv(ModeEnv, "")
	for in, want := range map[string]string{
		"bypasspermissions": "bypassPermissions",
		"BYPASSPERMISSIONS": "bypassPermissions",
		"acceptedits":       "acceptEdits",
		"dontask":           "dontAsk",
	} {
		t.Setenv(siteEnv, in)
		got := PermissionModeArgs(siteEnv)
		if len(got) != 2 || got[1] != want {
			t.Errorf("PermissionModeArgs() for %q = %q, want mode %q", in, got, want)
		}
	}
}

// A site with no knob of its own still resolves the global and the default —
// PermissionModeArgs("") must not be read as "omit".
func TestPermissionModeArgs_EmptySiteEnv(t *testing.T) {
	t.Setenv(ModeEnv, "")
	if got := PermissionModeArgs(""); len(got) != 2 || got[1] != DefaultMode {
		t.Fatalf(`PermissionModeArgs("") = %q, want the default %q`, got, DefaultMode)
	}
	t.Setenv(ModeEnv, "plan")
	if got := PermissionModeArgs(""); len(got) != 2 || got[1] != "plan" {
		t.Fatalf(`PermissionModeArgs("") = %q, want plan from %s`, got, ModeEnv)
	}
}

// Every mode `claude --help` lists must pass through, so a future operator
// pinning one does not silently get the default instead.
func TestPermissionModeArgs_AllCLIChoicesAccepted(t *testing.T) {
	t.Setenv(ModeEnv, "")
	for _, mode := range []string{"acceptEdits", "auto", "bypassPermissions", "manual", "dontAsk", "plan"} {
		t.Setenv(siteEnv, mode)
		got := PermissionModeArgs(siteEnv)
		if len(got) != 2 || got[1] != mode {
			t.Errorf("PermissionModeArgs() for %q = %q, want it passed through verbatim", mode, got)
		}
		if strings.TrimSpace(got[1]) == "" {
			t.Errorf("mode %q resolved to blank", mode)
		}
	}
}
