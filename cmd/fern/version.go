package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// versionString describes the running binary well enough to reproduce a
// bug report against it.
//
// There is no release version to print: `main` publishes a ROLLING
// nightly tag, so "which nightly" is a commit, not a number, and every
// build is `go build ./cmd/fern` with no `-X` stamp. Go records the
// commit itself — ReadBuildInfo's vcs.* settings for a build from a work
// tree, Main.Version for `go install …@version` — so the answer is
// already in the binary and needs no build-system cooperation to stay
// true.
//
// The Go version and platform ride along because they are the next two
// questions anyone triaging a miscompile asks.
func versionString() string {
	var b strings.Builder
	b.WriteString("fern ")
	b.WriteString(buildVersion())
	fmt.Fprintf(&b, "\nbuilt with %s for %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	return b.String()
}

// buildVersion is the version half: the commit the binary was built
// from, marked when the work tree had uncommitted changes, falling back
// to the module version when there is no VCS stamp (an install from the
// module proxy). The commit is preferred because a build from a checkout
// synthesises a pseudo-version that encodes the same commit far less
// readably.
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "(unknown build)"
	}
	var revision, when string
	dirty := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.time":
			when = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	return formatBuildVersion(revision, when, dirty, info.Main.Version)
}

// formatBuildVersion renders the build identity from the four facts
// ReadBuildInfo can supply. It is separate from the reading so it can be
// tested: Go does not stamp VCS information into `go test` binaries, so
// a test that called buildVersion() directly would only ever exercise
// the no-stamp branch.
func formatBuildVersion(revision, when string, dirty bool, moduleVersion string) string {
	if revision == "" {
		// No VCS stamp: installed from the module proxy rather than
		// built from a checkout, so the module version IS the answer.
		// (A source tarball has neither, and says so.)
		if moduleVersion != "" && moduleVersion != "(devel)" {
			return moduleVersion
		}
		return "(devel)"
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	out := revision
	if when != "" {
		out += " (" + when + ")"
	}
	if dirty {
		out += " with uncommitted changes"
	}
	return out
}
