package sourcelint

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestConformanceCensusHasARowPerFixture(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}

	fixtures := runnableFixtureNames(t, filepath.Join(root, "conformance", "cases"))
	if len(fixtures) < 300 {
		t.Fatalf("found %d runnable fixtures; the glob is wrong", len(fixtures))
	}
	rows := censusRowNames(t, filepath.Join(root, "internal", "testing", "e2e", "testdata", "conformance-leak-census.txt"))

	var missing, stale []string
	for _, name := range fixtures {
		if !rows[name] {
			missing = append(missing, name)
		}
	}
	seen := map[string]bool{}
	for _, name := range fixtures {
		seen[name] = true
	}
	for name := range rows {
		if !seen[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)

	if len(missing) > 0 {
		t.Errorf("%d conformance fixture(s) have no row in conformance-leak-census.txt: %s\n"+
			"Every runnable fixture needs a pinned verdict, or TestConformanceLeakCensusX86_64 "+
			"goes red on main for everyone. Measure with:\n"+
			"  FERN_LEAK_CENSUS_DUMP=1 go test ./internal/testing/e2e/ -run TestConformanceLeakCensusX86_64\n"+
			"and add the row in sorted order.", len(missing), strings.Join(missing, ", "))
	}
	if len(stale) > 0 {
		t.Errorf("%d census row(s) name no runnable fixture: %s\n"+
			"A deleted or negative-ified fixture leaves its row behind, and nothing else "+
			"reports that — the e2e census only walks the fixtures that exist.", len(stale), strings.Join(stale, ", "))
	}
}

func runnableFixtureNames(t *testing.T, casesDir string) []string {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join(casesDir, "*"))
	if err != nil {
		t.Fatalf("glob %s: %v", casesDir, err)
	}
	var out []string
	for _, d := range dirs {
		if _, err := os.Stat(filepath.Join(d, "main.fern")); err != nil {
			continue
		}
		if neg, _ := filepath.Glob(filepath.Join(d, "expected*error")); len(neg) > 0 {
			continue
		}
		out = append(out, filepath.Base(d))
	}
	sort.Strings(out)
	return out
}

func censusRowNames(t *testing.T, path string) map[string]bool {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open census: %v", err)
	}
	defer f.Close()
	rows := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if name, _, ok := strings.Cut(line, " "); ok {
			rows[name] = true
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read census: %v", err)
	}
	if len(rows) < 300 {
		t.Fatalf("parsed %d census rows; the format changed", len(rows))
	}
	return rows
}
