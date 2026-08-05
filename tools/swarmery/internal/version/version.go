// Package version is the single source of truth for the swarmery version,
// shared by `swarmery status` (internal/installer) and GET /api/health.
//
// Two identities live here. Version is the hand-maintained semver of the
// release line — stable, and the only thing the frozen /api/health "version"
// field is allowed to carry. String() is the *build* identity: the commit the
// running binary was actually built from, so a rebuilt daemon never reports
// the same string as the one it replaced.
package version

import (
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
)

// Version is the semantic version of the swarmery CLI/daemon. A var rather
// than a const so a release build can stamp the tag it was cut from:
//
//	go build -ldflags "-X <pkg>/internal/version.Version=0.3.0"
var Version = "0.2.0"

// Build is the raw build identity stamped at link time by the Makefile and the
// release workflow — `git describe --tags --match 'swarmery-v*' --always
// --dirty`, e.g. "swarmery-v0.2.0-15-g41157a8-dirty". Empty for a plain `go
// build`, where String() falls back to Go's own VCS stamping.
var Build = ""

// semverPrefix matches a leading MAJOR.MINOR.PATCH — how we tell a describe
// output that carries a tag ("0.2.0-15-g41157a8") from one that does not
// ("41157a8", produced by --always outside any matching tag).
var semverPrefix = regexp.MustCompile(`^\d+\.\d+\.\d+`)

var (
	buildOnce sync.Once
	buildID   string
)

// String returns the build identity of this binary: the stamped `git describe`
// output with its tag prefix stripped ("0.2.0-15-g41157a8-dirty"), or — when
// nothing was stamped — Version decorated with the VCS revision Go records in
// the build info ("0.2.0+41157a8-dirty"). Falls back to bare Version when the
// binary carries no VCS information at all (built outside a checkout).
func String() string {
	buildOnce.Do(func() { buildID = resolveBuild(Build, vcsStamp) })
	return buildID
}

// resolveBuild is String()'s pure core: `build` is the -ldflags value, `vcs`
// reports the revision Go stamped into the build info.
func resolveBuild(build string, vcs func() (rev string, dirty, ok bool)) string {
	if b := normalizeDescribe(build); b != "" {
		if semverPrefix.MatchString(b) {
			return b
		}
		return Version + "+" + b
	}
	rev, dirty, ok := vcs()
	if !ok || rev == "" {
		return Version
	}
	out := Version + "+" + shortRev(rev)
	if dirty {
		out += "-dirty"
	}
	return out
}

// normalizeDescribe trims the tag prefix off a `git describe` output, so
// "swarmery-v0.2.0-15-g41157a8" reads as "0.2.0-15-g41157a8".
func normalizeDescribe(build string) string {
	b := strings.TrimSpace(build)
	b = strings.TrimPrefix(b, "swarmery-")
	return strings.TrimPrefix(b, "v")
}

// vcsStamp reads the revision Go records for any build made inside a checkout.
func vcsStamp() (rev string, dirty, ok bool) {
	info, found := debug.ReadBuildInfo()
	if !found {
		return "", false, false
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	return rev, dirty, rev != ""
}

// shortRev abbreviates a full commit sha the way git does.
func shortRev(rev string) string {
	if len(rev) > 7 {
		return rev[:7]
	}
	return rev
}
