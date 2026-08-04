package main

import (
	"runtime"
	"strings"
	"testing"
)

// `fern -version` exists because the release channel is a ROLLING tag:
// without it a user cannot say which build they have, and neither can a
// bug report.
func TestVersionStringNamesBinaryGoAndPlatform(t *testing.T) {
	out := versionString()

	if !strings.HasPrefix(out, "fern ") {
		t.Errorf("version output does not name the binary:\n%s", out)
	}
	// The Go version and platform are the next two questions anyone
	// triaging a miscompile asks, so they travel with the commit.
	if !strings.Contains(out, runtime.Version()) {
		t.Errorf("version output omits the Go version %q:\n%s", runtime.Version(), out)
	}
	if !strings.Contains(out, runtime.GOOS+"/"+runtime.GOARCH) {
		t.Errorf("version output omits the platform:\n%s", out)
	}
}

// The build identity is rendered from what ReadBuildInfo supplies, and
// each of its four shapes has to say something a bug report can use.
// (Go does not stamp VCS data into `go test` binaries, which is why this
// tests the rendering rather than calling buildVersion.)
func TestFormatBuildVersion(t *testing.T) {
	for _, tc := range []struct {
		name     string
		revision string
		when     string
		dirty    bool
		module   string
		want     string
	}{
		{"built from a clean checkout",
			"be167834e1b82a27e2ca907a20e4be78b754119c", "2026-08-04T13:06:07Z", false, "(devel)",
			"be167834e1b8 (2026-08-04T13:06:07Z)"},
		{"uncommitted changes are called out",
			"be167834e1b82a27e2ca907a20e4be78b754119c", "2026-08-04T13:06:07Z", true, "(devel)",
			"be167834e1b8 (2026-08-04T13:06:07Z) with uncommitted changes"},
		{"installed from the proxy falls back to the module version",
			"", "", false, "v0.0.0-20260804130607-be167834e1b8",
			"v0.0.0-20260804130607-be167834e1b8"},
		{"neither stamp available",
			"", "", false, "(devel)",
			"(devel)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatBuildVersion(tc.revision, tc.when, tc.dirty, tc.module); got != tc.want {
				t.Errorf("formatBuildVersion() = %q, want %q", got, tc.want)
			}
		})
	}
}
