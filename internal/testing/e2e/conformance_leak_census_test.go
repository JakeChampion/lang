// CI-DARK: FERN_LEAK_CENSUS_DUMP — This flag regenerates the reference data.
package e2e

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jakechampion/lang/internal/envflag"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

var errCrashed = errors.New("fixture crashed (killed by a signal)")

type censusRow struct {
	name     string
	unpaired int
}

func TestConformanceLeakCensusX86_64(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs the whole conformance corpus; not a -short test")
	}
	_, runner := x86_64Tooling(t)
	e2eharness.SelfHostCLI(t)
	e2eharness.SelfHostStdlibRoot(t)
	cases := runnableFixtures(t)
	if len(cases) < 300 {
		t.Fatalf("found %d runnable fixtures; the corpus glob is wrong", len(cases))
	}

	var mu sync.Mutex
	var crashed []string
	unmeasured := map[string]error{}
	rows := make([]censusRow, 0, len(cases))
	sites := map[string]string{}
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, c := range cases {
		wg.Add(1)
		go func(c string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			n, top, err := traceOneFixture(t, runner, c)
			mu.Lock()
			defer mu.Unlock()
			if errors.Is(err, errCrashed) {
				crashed = append(crashed, filepath.Base(c))
				rows = append(rows, censusRow{filepath.Base(c), -1})
				return
			}
			if err != nil {
				unmeasured[filepath.Base(c)] = err
				rows = append(rows, censusRow{filepath.Base(c), -1})
				return
			}
			rows = append(rows, censusRow{filepath.Base(c), n})
			if n > 0 {
				sites[filepath.Base(c)] = top
			}
		}(c)
	}
	wg.Wait()
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	names := make([]string, 0, len(unmeasured))
	for name := range unmeasured {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Errorf("%s: could not be built or run, so it was not measured: %v", name, unmeasured[name])
	}

	if envflag.On("FERN_LEAK_CENSUS_DUMP") {
		for _, r := range rows {
			fmt.Printf("%-52s %d\n", r.name, r.unpaired)
		}
		t.Skip("dumped the census; not comparing")
	}

	if len(crashed) > 0 {
		sort.Strings(crashed)
		if len(crashed) > 8 {
			crashed = append(crashed[:8], "…")
		}
		t.Errorf("%d fixture(s) were killed by a signal: %s — an over-release reads as a "+
			"crash, and the leak counts below cannot see it",
			len(crashed), strings.Join(crashed, ", "))
	}

	want := loadCensus(t)
	var leaky, total int
	for _, r := range rows {
		if r.unpaired > 0 {
			leaky++
			total += r.unpaired
		}
		w, pinned := want[r.name]
		if !pinned {
			t.Errorf("%s: not in testdata/conformance-leak-census.txt (measured %d unpaired alloc(s)) — "+
				"a new fixture needs a pinned verdict; regenerate with FERN_LEAK_CENSUS_DUMP=1",
				r.name, r.unpaired)
			continue
		}
		if r.unpaired > w {
			t.Errorf("%s: %d unpaired alloc(s), pinned at %d — this fixture leaks more than it did. "+
				"Top alloc site(s): %s",
				r.name, r.unpaired, w, sites[r.name])
		}
		if r.unpaired >= 0 && w > 0 && r.unpaired < w {
			t.Errorf("%s: %d unpaired alloc(s), pinned at %d — this leak got smaller, which is good news "+
				"that has to be recorded: regenerate with FERN_LEAK_CENSUS_DUMP=1",
				r.name, r.unpaired, w)
		}
	}
	t.Logf("%d fixtures measured, %d leak, %d unpaired allocs total", len(rows), leaky, total)
}

func runnableFixtures(t *testing.T) []string {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join(conformanceCases, "*"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var out []string
	for _, d := range dirs {
		if _, err := os.Stat(filepath.Join(d, "main.fern")); err != nil {
			continue
		}
		if neg, _ := filepath.Glob(filepath.Join(d, "expected*error")); len(neg) > 0 {
			continue
		}
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

func traceOneFixture(t *testing.T, runner []string, dir string) (int, string, error) {
	tmp, err := os.MkdirTemp("", "census")
	if err != nil {
		return 0, "", err
	}
	defer os.RemoveAll(tmp)
	bin, err := buildTracedX86_64(t, filepath.Join(dir, "main.fern"), tmp)
	if err != nil {
		return 0, "", err
	}
	cmd := runX86_64Bin(runner, bin)
	_, stderr, exit := runSplit(t, cmd)
	if exit == -1 {
		return 0, "", errCrashed
	}
	return pairRcTrace(stderr)
}

func pairRcTrace(stderr string) (int, string, error) {
	live := map[string]int{}
	site := map[string]string{}
	for _, line := range strings.Split(stderr, "\n") {
		m := rcTraceLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		ptr := m[2]
		if m[1] == "a" {
			if live[ptr] == 0 {
				site[ptr] = m[4]
			}
			live[ptr]++
		} else {
			live[ptr]--
		}
	}
	bySite := map[string]int{}
	n := 0
	for ptr, c := range live {
		if c > 0 {
			n += c
			bySite[site[ptr]] += c
		}
	}
	var ks []string
	for k := range bySite {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool {
		if bySite[ks[i]] != bySite[ks[j]] {
			return bySite[ks[i]] > bySite[ks[j]]
		}
		return ks[i] < ks[j]
	})
	var b strings.Builder
	for i, k := range ks {
		if i == 3 {
			break
		}
		fmt.Fprintf(&b, "%s x%d ", k, bySite[k])
	}
	return n, strings.TrimSpace(b.String()), nil
}

func loadCensus(t *testing.T) map[string]int {
	t.Helper()
	path := filepath.Join("testdata", "conformance-leak-census.txt")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	out := map[string]int{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fs := strings.Fields(line)
		if len(fs) != 2 {
			t.Fatalf("%s: malformed row %q", path, line)
		}
		n, err := strconv.Atoi(fs[1])
		if err != nil {
			t.Fatalf("%s: bad count in %q", path, line)
		}
		if _, dup := out[fs[0]]; dup {
			t.Fatalf("%s: %s has two rows", path, fs[0])
		}
		out[fs[0]] = n
	}
	return out
}
