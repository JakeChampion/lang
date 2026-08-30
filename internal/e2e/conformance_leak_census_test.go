package e2e

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/e2eharness"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// --- Which conformance fixtures leak, and where ------------------------------
//
// `docs/TEST-GATES.md` states the gap plainly: the rc detector counts
// over-RELEASES only, a leak reads as a clean 0, and while FERN_LEAKCHECK
// sees that a leak happened and FERN_RC_TRACE names the alloc site it came
// from, "neither runs as part of any gate — you have to go looking".
//
// This goes looking, over the whole conformance corpus. Each fixture is
// emitted with the heap tracer on, run, and its `rctrace` records paired by
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
// failure instead, where they are the actionable half — `-g` plus addr2line
// turns one into a source line.
//
// Regenerate with FERN_LEAK_CENSUS_DUMP=1, the same convention as
// FERN_LEAK_MATRIX_DUMP.
//
// x86-64 only, like the tracer itself. Fixtures with an `expected.error` file
// are negative fixtures and are skipped: they are not supposed to compile.

type censusRow struct {
	name     string
	unpaired int
}

func TestConformanceLeakCensusX86_64(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs the whole conformance corpus; not a -short test")
	}
	gcc, runner, ok := e2eharness.LookupX86_64Tooling()
	if !ok {
		t.Skip("no x86-64 toolchain")
	}
	cases := runnableFixtures(t)
	if len(cases) < 300 {
		t.Fatalf("found %d runnable fixtures; the corpus glob is wrong", len(cases))
	}

	var mu sync.Mutex
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
			n, top, err := traceOneFixture(t, gcc, runner, c)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				// A fixture this pass cannot build or run is counted as
				// unmeasured rather than clean; a silent zero here would
				// be the vacuous-pass failure mode.
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
				"Top alloc site(s): %s (addr2line on a -g build names the source line)",
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

// traceOneFixture emits dir/main.fern with the heap tracer on, runs it, and
// returns how many allocs never got a matching free.
// It runs the load/check/monomorph chain itself rather than through
// e2eharness.LoadCheckMono: that helper reports failures with t.Fatalf,
// which is illegal from the worker goroutines below, and a fixture this
// pass cannot build has to be recorded as unmeasured rather than end the
// run.
func traceOneFixture(t *testing.T, gcc string, runner []string, dir string) (int, string, error) {
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		return 0, "", err
	}
	if err := constfold.Fold(prog, nil); err != nil {
		return 0, "", err
	}
	info, err := checker.Check(prog)
	if err != nil {
		return 0, "", err
	}
	if err := monomorph.Run(prog, info); err != nil {
		return 0, "", err
	}
	asm, err := emitWithTracer(prog, info)
	if err != nil {
		return 0, "", err
	}

	tmp, err := os.MkdirTemp("", "census")
	if err != nil {
		return 0, "", err
	}
	defer os.RemoveAll(tmp)
	asmPath, binPath := filepath.Join(tmp, "p.s"), filepath.Join(tmp, "p")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		return 0, "", err
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		return 0, "", fmt.Errorf("gcc: %v\n%s", err, out)
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), binPath)...)
	}
	_, stderr, _ := runSplit(t, cmd)
	return pairRcTrace(stderr)
}

// emitTracerMu serialises the emit, which toggles package-level ast flags.
var emitTracerMu sync.Mutex

func emitWithTracer(prog *ast.Program, info *checker.Info) (string, error) {
	emitTracerMu.Lock()
	defer emitTracerMu.Unlock()
	prevFree, prevTrace := ast.RcFreeEnabled, ast.RcTrace
	defer func() { ast.RcFreeEnabled, ast.RcTrace = prevFree, prevTrace }()
	ast.RcFreeEnabled, ast.RcTrace = true, true
	return x86_64.Emit(prog, info)
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
		out[fs[0]] = n
	}
	return out
}
