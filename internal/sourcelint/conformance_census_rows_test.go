package sourcelint

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestConformanceCensusHasARowPerFixture keeps conformance/cases and
// internal/e2e/testdata/conformance-leak-census.txt in step.
//
// TestConformanceLeakCensusX86_64 already requires a pinned verdict for every
// runnable fixture, but it gets there by COMPILING and RUNNING the whole
// corpus, so it lives in the e2e lane. That makes the omission cheap to
// commit and expensive to notice: a PR that adds a fixture is green against a
// base whose census predates it, and the lane only goes red on main
// afterwards, for everyone. It happened three times on 2026-09-05 —
// arrow_lambda_block_body, cast_int_arith_to_float, and the two tuple-binder
// fixtures — and two of those were reported as separate bugs (#8518, #8534)
// before the shared cause was noticed (#8655).
//
// This asks the same question with no compiler in it: milliseconds, in the
// unit lane, so the PR that adds the fixture is the one that fails.
//
// It deliberately does NOT check the verdict — only that a row exists. What
// the number should be is a measurement, and measuring is the e2e test's job.
func TestConformanceCensusHasARowPerFixture(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}

	fixtures := runnableFixtureNames(t, filepath.Join(root, "conformance", "cases"))
	if len(fixtures) < 300 {
		t.Fatalf("found %d runnable fixtures; the glob is wrong", len(fixtures))
	}
	rows := censusRowNames(t, filepath.Join(root, "internal", "e2e", "testdata", "conformance-leak-census.txt"))

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
			"  FERN_LEAK_CENSUS_DUMP=1 go test ./internal/e2e/ -run TestConformanceLeakCensusX86_64\n"+
			"and add the row in sorted order.", len(missing), strings.Join(missing, ", "))
	}
	if len(stale) > 0 {
		t.Errorf("%d census row(s) name no runnable fixture: %s\n"+
			"A deleted or negative-ified fixture leaves its row behind, and nothing else "+
			"reports that — the e2e census only walks the fixtures that exist.", len(stale), strings.Join(stale, ", "))
	}
}

// runnableFixtureNames mirrors runnableFixtures in
// internal/e2e/conformance_leak_census_test.go: a directory with a main.fern
// and no expected*error sidecar. The rule is duplicated rather than shared
// because that package compiles the corpus and cannot run in the unit lane;
// if the rule changes there, this fails loudly rather than drifting quietly.
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

// censusRowNames reads the fixture names out of the pin file. A row is
// `<name> <count>`; everything else is a `#` comment or blank.
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
