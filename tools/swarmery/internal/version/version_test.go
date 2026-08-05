package version

import (
	"regexp"
	"testing"
)

// The frozen /api/health contract requires Version to stay strict semver even
// when a release build stamps it via -ldflags.
func TestVersionIsSemver(t *testing.T) {
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(Version) {
		t.Fatalf("Version = %q, want MAJOR.MINOR.PATCH", Version)
	}
}

func noVCS() (string, bool, bool) { return "", false, false }

func TestResolveBuild(t *testing.T) {
	stamped := func(rev string, dirty bool) func() (string, bool, bool) {
		return func() (string, bool, bool) { return rev, dirty, true }
	}

	tests := []struct {
		name  string
		build string
		vcs   func() (string, bool, bool)
		want  string
	}{
		{
			name:  "describe with tag keeps its commit distance",
			build: "swarmery-v0.2.0-15-g41157a8",
			vcs:   noVCS,
			want:  "0.2.0-15-g41157a8",
		},
		{
			name:  "dirty describe survives normalization",
			build: "swarmery-v0.2.0-15-g41157a8-dirty",
			vcs:   noVCS,
			want:  "0.2.0-15-g41157a8-dirty",
		},
		{
			name:  "exactly on a tag reports the plain version",
			build: "swarmery-v0.2.0",
			vcs:   noVCS,
			want:  "0.2.0",
		},
		{
			name:  "untagged describe --always is decorated with the version",
			build: "41157a8-dirty",
			vcs:   noVCS,
			want:  Version + "+41157a8-dirty",
		},
		{
			name:  "unstamped build falls back to the Go VCS revision",
			build: "",
			vcs:   stamped("41157a8f2b1c9d0e5a6b7c8d9e0f1a2b3c4d5e6f", false),
			want:  Version + "+41157a8",
		},
		{
			name:  "unstamped dirty build is marked dirty",
			build: "  ",
			vcs:   stamped("41157a8f2b1c9d0e5a6b7c8d9e0f1a2b3c4d5e6f", true),
			want:  Version + "+41157a8-dirty",
		},
		{
			name:  "no stamp and no VCS info degrades to the bare version",
			build: "",
			vcs:   noVCS,
			want:  Version,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveBuild(tc.build, tc.vcs); got != tc.want {
				t.Errorf("resolveBuild(%q) = %q, want %q", tc.build, got, tc.want)
			}
		})
	}
}

// String() must never come back empty — the header renders it verbatim.
func TestStringNonEmpty(t *testing.T) {
	if String() == "" {
		t.Fatal("String() = \"\", want a build identity")
	}
	if got := String(); got != String() {
		t.Errorf("String() is not stable: %q then %q", got, String())
	}
}
