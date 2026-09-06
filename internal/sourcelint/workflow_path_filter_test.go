package sourcelint

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A workflow's `paths:` / `paths-ignore:` filter decides when the lane runs,
// and it had drifted from what the lanes are built from (#8474, #8627). Nothing
// in a filter can be computed at run time, so the list is checked here instead.
//
// Two gates, and the second is the one that matters:
//
//   - every entry must match something on disk, so an entry that reads as
//     coverage and provides none is caught;
//   - every internal package a lane's binary is built from must be covered,
//     so narrowing a filter back to an enumeration cannot silently drop one.

// pathFilterEntries returns the `paths:` / `paths-ignore:` globs inside a
// workflow's top-level `on:` mapping. Only those two keys are read: a
// `workflows:` list names other workflows and a `branches:` list names refs,
// neither of which is a path.
func pathFilterEntries(t *testing.T, workflow string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", workflow))
	if err != nil {
		t.Fatalf("read %s: %v", workflow, err)
	}
	on, ok := onBlock(string(b))
	if !ok {
		return nil
	}
	var (
		key   = regexp.MustCompile(`^(\s*)(paths|paths-ignore):\s*$`)
		item  = regexp.MustCompile(`^(\s*)- "([^"]+)"\s*$`)
		out   []string
		indet = -1
	)
	for _, line := range strings.Split(on, "\n") {
		if m := key.FindStringSubmatch(line); m != nil {
			indet = len(m[1])
			continue
		}
		if indet < 0 {
			continue
		}
		if m := item.FindStringSubmatch(line); m != nil && len(m[1]) > indet {
			out = append(out, m[2])
			continue
		}
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue // blank lines and comments stay inside the block
		}
		indet = -1 // any other line ends the list
	}
	return out
}

func workflowFiles(t *testing.T) []string {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join("..", "..", ".github", "workflows"))
	if err != nil {
		t.Fatalf("read workflows: %v", err)
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yml") {
			out = append(out, e.Name())
		}
	}
	if len(out) == 0 {
		t.Fatal("no workflows found — did the directory move?")
	}
	sort.Strings(out)
	return out
}

// TestWorkflowPathFiltersHaveNoDeadEntries catches the `internal/prelude/**`
// shape across EVERY workflow. The per-file version of this gate was scoped to
// macos.yml, which is why the same dead entry survived in three other lanes
// (#8627): a dead entry contributes nothing to the match, so an allowlist
// silently narrows to whatever its remaining entries happen to cover.
func TestWorkflowPathFiltersHaveNoDeadEntries(t *testing.T) {
	root := filepath.Join("..", "..")
	checked := 0
	for _, wf := range workflowFiles(t) {
		for _, entry := range pathFilterEntries(t, wf) {
			checked++
			// `dir/**` matches anything under dir; the rest are literal paths
			// or a single-segment glob. Reduce each to something
			// filepath.Glob can answer, then require at least one hit.
			probe := strings.TrimSuffix(entry, "/**")
			matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(probe)))
			if err != nil {
				t.Errorf("%s: entry %q is not a valid pattern: %v", wf, entry, err)
				continue
			}
			if len(matches) == 0 {
				t.Errorf("%s: path filter entry %q matches nothing on disk — it reads "+
					"as coverage and provides none (#8474, #8627)", wf, entry)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no path filter entries found — if the filter shape changed, update this gate with it")
	}
}

// filterCoverage names, per lane, the command whose dependency closure the
// lane's filter must cover, and the GOOS/GOARCH it is built for. cmd/fern-wasm
// is `//go:build js && wasm`, so a host-GOOS `go list` reports an empty
// closure and would make the gate vacuous.
var filterCoverage = []struct {
	workflow string
	pkg      string
	goos     string
	goarch   string
}{
	// The only lane that EXECUTES an arm64-darwin binary: Linux CI
	// cross-compiles Mach-O and checks it is well-formed, which cannot catch
	// an ABI bug.
	{"macos.yml", "./cmd/fern", "", ""},
	// The docs site stages the playground wasm and regenerates the stdlib
	// reference, so it is built from both binaries' closures.
	{"docs-build.yml", "./cmd/fern-wasm", "js", "wasm"},
	{"docs-build.yml", "./cmd/ferndoc", "", ""},
	{"pages.yml", "./cmd/fern-wasm", "js", "wasm"},
	{"pages.yml", "./cmd/ferndoc", "", ""},
	// The playground is the wasm binary plus the page that hosts it.
	{"playground-e2e.yml", "./cmd/fern-wasm", "js", "wasm"},
}

func TestWorkflowPathFiltersCoverEveryDependency(t *testing.T) {
	if testing.Short() {
		t.Skip("runs `go list -deps`; not a -short test")
	}
	root := filepath.Join("..", "..")
	for _, c := range filterCoverage {
		t.Run(c.workflow+" "+c.pkg, func(t *testing.T) {
			cmd := exec.Command("go", "list", "-deps", c.pkg)
			cmd.Dir = root
			if c.goos != "" {
				cmd.Env = ciEnv("GOOS="+c.goos, "GOARCH="+c.goarch)
			} else {
				cmd.Env = ciEnv()
			}
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("go list -deps %s: %v", c.pkg, err)
			}
			entries := pathFilterEntries(t, c.workflow)
			if len(entries) == 0 {
				t.Fatalf("%s has no path filter — this gate assumes one", c.workflow)
			}

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
				t.Errorf("%s's path filter does not cover %d package(s) that %s is built "+
					"from, so a change to them lands with the lane never firing (#8474, #8627):\n  %s",
					c.workflow, len(uncovered), c.pkg, strings.Join(uncovered, "\n  "))
			}
		})
	}
}

// coveredByAny reports whether pkg (a slash path like "internal/platforms")
// falls under one of the filter's globs. Only the `dir/**` and exact-path
// forms are recognised, which is all the filters use; an entry in any other
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
