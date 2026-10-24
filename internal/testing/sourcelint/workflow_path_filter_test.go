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

func pathFilterEntries(t *testing.T, workflow string) []string {
	t.Helper()
	if f, ok := laneTable(t)[strings.TrimSuffix(workflow, ".yml")]; ok {
		return append(append([]string(nil), f.Paths...), f.PathsIgnore...)
	}
	b, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", workflow))
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
			continue
		}
		indet = -1
	}
	return out
}

func workflowFiles(t *testing.T) []string {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join("..", "..", "..", ".github", "workflows"))
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

func TestWorkflowPathFiltersHaveNoDeadEntries(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	checked := 0
	for _, wf := range workflowFiles(t) {
		for _, entry := range pathFilterEntries(t, wf) {
			checked++
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

var filterCoverage = []struct {
	workflow string
	pkg      string
}{
	{"macos.yml", "./cmd/fern"},
	{"docs-build.yml", "./cmd/fern"},
	{"pages.yml", "./cmd/fern"},
	{"playground-e2e.yml", "./cmd/fern"},
}

func TestWorkflowPathFiltersCoverEveryDependency(t *testing.T) {
	if testing.Short() {
		t.Skip("runs `go list -deps`; not a -short test")
	}
	root := filepath.Join("..", "..", "..")
	for _, c := range filterCoverage {
		t.Run(c.workflow+" "+c.pkg, func(t *testing.T) {
			cmd := exec.Command("go", "list", "-deps", c.pkg)
			cmd.Dir = root
			cmd.Env = ciEnv()
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
				pkg := strings.TrimPrefix(line, mod)
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
