package sourcelint

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// macos.yml is the only lane that EXECUTES an arm64-darwin binary — Linux CI
// cross-compiles Mach-O and checks it is well-formed, which cannot catch an
// ABI bug. Its `paths:` filter decides when it runs, and the filter had
// drifted from what the binary is built from (#8474): it listed
// `internal/prelude/**`, gone since the injector was removed, and omitted
// internal/stdlib, internal/platforms and 10 more real dependencies. A change
// to any of them landed with the darwin exec check never firing.
//
// These two gates are the "if the filter must stay literal" half of that
// issue: nothing in a workflow's `paths:` can be computed at run time, so the
// list is checked here instead.

// macosPathEntries reads the `paths:` globs out of macos.yml. Both the
// pull_request and push filters are returned together — they are required to
// mirror each other, which TestGateLanesRunOnMain pins separately.
func macosPathEntries(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "macos.yml"))
	if err != nil {
		t.Fatalf("read macos.yml: %v", err)
	}
	re := regexp.MustCompile(`^\s+- "([^"]+)"`)
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if m := re.FindStringSubmatch(line); m != nil {
			out = append(out, m[1])
		}
	}
	if len(out) == 0 {
		t.Fatal("no quoted path entries found in macos.yml — if the filter's shape changed, update this gate with it")
	}
	return out
}

// TestMacosPathFilterHasNoDeadEntries catches the `internal/prelude/**` shape:
// an entry that matches nothing, which reads as coverage and provides none.
func TestMacosPathFilterHasNoDeadEntries(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, entry := range macosPathEntries(t) {
		// `dir/**` matches anything under dir; the rest are literal paths or
		// a single-segment glob. Reduce each to something filepath.Glob can
		// answer, then require at least one hit.
		probe := strings.TrimSuffix(entry, "/**")
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(probe)))
		if err != nil {
			t.Errorf("entry %q is not a valid pattern: %v", entry, err)
			continue
		}
		if len(matches) == 0 {
			t.Errorf("macos.yml `paths:` entry %q matches nothing on disk — it reads as coverage and provides none (#8474)", entry)
		}
	}
}

// TestMacosPathFilterCoversEveryDependency is the inverse, and the one that
// matters: every internal package the produced binary is built from must be
// covered, so narrowing the filter back to an enumeration cannot silently
// drop one.
func TestMacosPathFilterCoversEveryDependency(t *testing.T) {
	if testing.Short() {
		t.Skip("runs `go list -deps`; not a -short test")
	}
	root := filepath.Join("..", "..")
	cmd := exec.Command("go", "list", "-deps", "./cmd/fern")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps ./cmd/fern: %v", err)
	}
	entries := macosPathEntries(t)

	const mod = "github.com/jakechampion/lang/"
	var uncovered []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, mod+"internal/") {
			continue
		}
		pkg := strings.TrimPrefix(line, mod) // e.g. internal/platforms
		if !coveredByAny(pkg, entries) {
			uncovered = append(uncovered, pkg)
		}
	}
	if len(uncovered) > 0 {
		t.Errorf("macos.yml's `paths:` filter does not cover %d package(s) that cmd/fern is built from, "+
			"so a change to them lands with the only arm64-darwin EXECUTION lane never firing (#8474):\n  %s",
			len(uncovered), strings.Join(uncovered, "\n  "))
	}
}

// coveredByAny reports whether pkg (a slash path like "internal/platforms")
// falls under one of the filter's globs. Only the `dir/**` and exact-path
// forms are recognised, which is all the filter uses; an entry in any other
// shape is treated as covering nothing, so it fails loudly rather than
// passing by accident.
func coveredByAny(pkg string, entries []string) bool {
	for _, e := range entries {
		if strings.HasSuffix(e, "/**") {
			if dir := strings.TrimSuffix(e, "/**"); pkg == dir || strings.HasPrefix(pkg, dir+"/") {
				return true
			}
			continue
		}
		if e == pkg {
			return true
		}
	}
	return false
}
