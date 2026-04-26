package version

import (
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

func TestString_DefaultValues(t *testing.T) {
	// At test time, ldflags haven't been applied. Version should be
	// the package default ("dev") and BuildDate the package default
	// ("unknown"); GoVersion is whatever the test binary is running
	// against. The point of String() is one-line summary — verify it
	// includes all three.
	got := String()
	if !strings.Contains(got, Version) {
		t.Errorf("String()=%q missing Version=%q", got, Version)
	}
	if !strings.Contains(got, BuildDate) {
		t.Errorf("String()=%q missing BuildDate=%q", got, BuildDate)
	}
	if !strings.Contains(got, GoVersion) {
		t.Errorf("String()=%q missing GoVersion=%q", got, GoVersion)
	}
}

func TestGoVersion_MatchesRuntime(t *testing.T) {
	// GoVersion is captured at package init from runtime.Version().
	// If the variable were ever overridden by ldflags + lost the
	// runtime tie, this catches it.
	if GoVersion != runtime.Version() {
		t.Errorf("GoVersion=%q != runtime.Version()=%q",
			GoVersion, runtime.Version())
	}
}

func TestReadVCSSetting_UnknownKey(t *testing.T) {
	// Asking for a key that isn't in BuildInfo settings must return
	// the documented "unknown" sentinel — never an empty string —
	// so /v1/version always has a non-empty field.
	got := readVCSSetting("definitely-not-a-real-vcs-key")
	if got != "unknown" {
		t.Errorf("readVCSSetting(unknown key) = %q, want %q", got, "unknown")
	}
}

func TestReadVCSSetting_MatchedKey(t *testing.T) {
	// The "key not found" path is covered by TestReadVCSSetting_UnknownKey;
	// this pins the matched-key path so a refactor that breaks the
	// loop-match (e.g. accidentally returning "unknown" for every
	// lookup) gets caught. Use a setting Go's build system always
	// records into a test binary — GOARCH — and assert that the
	// helper returns the same value the runtime confirms.
	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Skip("build info unavailable — cannot exercise matched-key path")
	}
	var want string
	for _, s := range info.Settings {
		if s.Key == "GOARCH" {
			want = s.Value
			break
		}
	}
	if want == "" {
		t.Skip("GOARCH not present in BuildInfo settings on this toolchain")
	}
	if got := readVCSSetting("GOARCH"); got != want {
		t.Errorf("readVCSSetting(\"GOARCH\") = %q, want %q", got, want)
	}
	if want != runtime.GOARCH {
		t.Errorf("BuildInfo GOARCH=%q != runtime.GOARCH=%q (sanity)", want, runtime.GOARCH)
	}
}

func TestVersionField_IsNonEmpty(t *testing.T) {
	// Default value should never be empty — clients display this
	// directly, and an empty version field is meaningfully
	// different from "dev". Belt-and-braces against a future
	// ldflags-only-init regression.
	if Version == "" {
		t.Error("Version is empty — must default to \"dev\" if ldflags didn't run")
	}
	if BuildDate == "" {
		t.Error("BuildDate is empty — must default to \"unknown\"")
	}
}
