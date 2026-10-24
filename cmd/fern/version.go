package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

func versionString() string {
	var b strings.Builder
	b.WriteString("fern-oracle ")
	b.WriteString(buildVersion())
	fmt.Fprintf(&b, "\nbuilt with %s for %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	return b.String()
}

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

func formatBuildVersion(revision, when string, dirty bool, moduleVersion string) string {
	if revision == "" {
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
