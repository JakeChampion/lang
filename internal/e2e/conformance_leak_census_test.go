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

	"github.com/jakechampion/lang/internal/e2eharness"
)

// --- Which conformance fixtures leak, and where ------------------------------
//
// `docs/TEST-GATES.md` states the gap plainly: the rc detector counts
// over-RELEASES only, a leak reads as a clean 0, and while FERN_LEAKCHECK
// sees that a leak happened and FERN_RC_TRACE names the alloc site it came
// from, "neither runs as part of any gate — you have to go looking".
//
// This goes looking, over the whole conformance corpus. Each fixture is
// compiled by the self-host with the heap tracer on, run, and its `rctrace` records paired by
// pointer; an alloc with no matching free is memory the program never gave
// back on the path it took.
//
// The verdict is pinned per fixture in testdata/conformance-leak-census.txt,
// the same shape as the self-host leak matrix's pin: a fixture that starts
// leaking, or leaks MORE, fails here rather than being discovered later.
//
// What is pinned is the COUNT, not the sites. A `site` is a runtime return
// address, so it moves with any codegen change and would make the file churn
// for reasons unrelated to reference counting. The sites are printed on
// failure instead, where they are the actionable half.
//
// Regenerate with FERN_LEAK_CENSUS_DUMP=1, the same convention as
// FERN_LEAK_MATRIX_DUMP.
//
// x86-64 only, like the tracer itself. Fixtures with an `expected.error` file
// are negative fixtures and are skipped: they are not supposed to compile.

// errCrashed marks a fixture the run killed with a signal.
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
	// Built here, not in a worker: a first build that fails reports through t.
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
				// A fixture this pass cannot build or run is unmeasured,
				// not clean, and fails below: a -1 row compares as
				// leaking nothing, so without that a compiler change that
				// breaks the link of a fixture pinned at 0 passed here.
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

	// CI-DARK: FERN_LEAK_CENSUS_DUMP — a regeneration tool, not coverage:
	// it prints measured census lines INSTEAD of comparing, so a lane
	// setting it would disable this gate. The compare path below is the
	// CI behaviour. Same reasoning as FERN_LEAK_MATRIX_DUMP.
	if os.Getenv("FERN_LEAK_CENSUS_DUMP") == "1" {
		for _, r := range rows {
			fmt.Printf("%-52s %d\n", r.name, r.unpaired)
		}
		t.Skip("dumped the census; not comparing")
	}

	// No fixture may die of a signal. This is a correctness floor rather
	// than a pinned number: a crash is never an acceptable steady state,
	// so there is nothing to ratchet.
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

// runnableFixtures lists the conformance fixtures that are supposed to
// compile.
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
		// A fixture with an expected-error file is a negative one: it is
		// supposed to fail, so there is nothing to run. Two spellings
		// exist — `expected.error` for a check-time diagnostic and
		// `expected.lowering-error` for one the lowering raises — so
		// this globs rather than naming them, and a third spelling
		// added later is picked up rather than silently measured.
		if neg, _ := filepath.Glob(filepath.Join(d, "expected*error")); len(neg) > 0 {
			continue
		}
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// traceOneFixture compiles dir/main.fern with the heap tracer on, runs it,
// and returns how many allocs never got a matching free. Every failure is
// returned rather than reported: t.Fatalf is illegal from the worker
// goroutines below, and a fixture this pass cannot build has to be recorded
// as unmeasured rather than end the run.
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
	// A fixture killed by a signal is a CRASH, not a verdict. Go reports
	// that as exit code -1; an ordinary non-zero status is just the
	// program's own `main` result and says nothing.
	//
	// This is here because the census did not have it and should have. An
	// attempted rc fix (docs/rc-log/2026-08-30-match-binding-rebind-
	// overretain.md) segfaulted a program, and this pass — which compiles
	// and RUNS all 453 fixtures — stayed green through it, because it read
	// only the trace and threw the status away. A leak gate is blind to an
	// over-release by construction; noticing that the program died is the
	// cheapest repair.
	//
	// It would NOT have caught that particular one, and the check was
	// re-run against the broken compiler to find that out rather than
	// assumed: the crash was in examples/proposals/unidiff.fern, and this
	// corpus is conformance/cases only, where nothing crashed. What caught
	// it was TestArm64SSABackendDifferential, which runs examples/.
	//
	// So this closes the shape of the gap, not that instance of it.
	// Widening the census to examples/ would close the instance too, and
	// would give leak figures for whole programs rather than fixtures —
	// worth doing, and a bigger change than this one.
	if exit == -1 {
		return 0, "", errCrashed
	}
	return pairRcTrace(stderr)
}

// pairRcTrace counts allocs with no matching free, and names the alloc sites
// the survivors came from, commonest first.
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
		// A second row would silently replace the first, so a merge that
		// keeps both sides' rows must not pass for the one read last.
		if _, dup := out[fs[0]]; dup {
			t.Fatalf("%s: %s has two rows", path, fs[0])
		}
		out[fs[0]] = n
	}
	return out
}
