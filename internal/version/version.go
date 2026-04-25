// Package version exposes build-time version metadata injected
// via -ldflags at build time, plus VCS info read from the Go
// build system at startup. See Makefile `LD_FLAGS`.
//
// Values default to "dev" / "unknown" for unbuilt or test runs;
// at production-build time the Makefile injects real values via
// -ldflags. The Commit / Dirty fields come from
// runtime/debug.ReadBuildInfo(), populated by `go build
// -buildvcs=true` (the Makefile's default).
package version

import (
	"runtime"
	"runtime/debug"
)

var (
	// Version is the git describe --tags --always --dirty output,
	// or "dev" for local unbuilt runs.
	Version = "dev"

	// BuildDate is the ISO-8601 UTC timestamp at build time.
	BuildDate = "unknown"

	// Commit is the VCS revision (full git SHA) embedded by
	// `go build -buildvcs=true`. Falls back to "unknown" if VCS
	// info isn't available (typical for `go test` runs).
	Commit = readVCSSetting("vcs.revision")

	// Dirty is "true" if the working tree had uncommitted changes
	// at build time, "false" otherwise. Same source as Commit.
	Dirty = readVCSSetting("vcs.modified")

	// GoVersion is the runtime Go version (e.g. "go1.22.3"). Captured
	// at process start; useful for quick "what's running" checks
	// across a fleet without shelling into every host.
	GoVersion = runtime.Version()
)

// String returns a human-readable one-line summary.
func String() string {
	return Version + " (" + BuildDate + ", " + GoVersion + ")"
}

// readVCSSetting fetches a single key from runtime/debug's VCS
// settings map. Used at package-init time to seed Commit + Dirty.
func readVCSSetting(key string) string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, s := range info.Settings {
		if s.Key == key {
			return s.Value
		}
	}
	return "unknown"
}
