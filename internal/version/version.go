// Package version holds the build-time version string, injected via -ldflags.
package version

import (
	"runtime/debug"
	"strings"
)

// Version is overridden at build time with
// -ldflags "-X github.com/master2sid2/sshm/internal/version.Version=...".
// It carries the "v" prefix, as git describe and release tags do.
var Version = "dev"

func init() {
	if Version != "dev" {
		return
	}
	// "go install github.com/master2sid2/sshm@v0.2.1" records the tag in the
	// build info; untagged builds get a pseudo-version, which stays "dev".
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := bi.Main.Version; v != "" && v != "(devel)" && !strings.Contains(v, "-") {
			Version = v
		}
	}
}
