package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// std/bench is the measurement harness the FIP/FBIP experiments are built on
// (#9592): tail latency, allocation accounting, a "steady state starts now"
// mark, and machine-readable output.
//
// Two gates, because the module makes two kinds of claim. The arithmetic — the
// percentiles, the summary, the JSON and CSV shapes — is pinned by the TAP
// suite under `-interp`, where hand-built reports make every answer
// deterministic. What the interpreter cannot show is the part that matters
// most, since it has no allocator to count: that the harness does not charge
// its own bookkeeping to the body it measures. That is the compiled leg below.

// `examples/tests/bench_harness_test.fern` is the TAP suite. Passing → exit 0.
// Named for the module rather than "bench", because `bench_test.fern` is
// already std/test's own bench-helper suite and this is a different module.
func TestRunnerBenchHarnessExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/bench_harness_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/bench harness", "# pass 14", "# fail 0", "1..14"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// The measurement must not perturb what it measures: what the harness reports
// has to be what the BODY cost, with nothing of the harness's own added.
//
// This is not hypothetical. The first version of the module appended each
// sample inside the timed region and built a struct to read the count back,
// and reported 57 allocations for 50 iterations of a body making one. The
// sample buffer is now built to full length before the mark and written
// through `.with`, which Perceus mutates in place, and the assertion path
// reads scalars rather than constructing a result.
//
// The two claims pinned here are the portable ones. How many allocations a
// given body costs is not: `"abcdefgh" + n.to_string()` costs two on x86-64
// and a shorter result costs fewer, because a small string lives inline and
// never reaches the allocator at all. So the test asserts that an empty body
// costs ZERO, and that a fixed body's cost scales EXACTLY with the iteration
// count — both of which are false the moment the harness charges anything of
// its own.
//
// The exit code carries the verdict so the program needs no output parsing:
// 42 is the neutral reading, and the other codes name which half slipped.
const benchNeutralSrc = `import "std/bench";
import "std/i32";
import "std/string";

function one_allocation(n: i32): i32 {
	var s: string = "abcdefgh" + n.to_string();
	return s.len();
}

function main(): i32 {
	var acc: i32 = 0;
	// An empty body: the harness's own cost, and it must be nothing.
	var quiet: bench.Report = bench.run("quiet", 5, 200, () => { acc = acc + 1; });
	if (acc == 0) { return 90; }
	if (quiet.allocs != (0 as i64)) { return 91; }
	// A fixed body, twice: the count must scale exactly, so the harness adds
	// neither a per-iteration cost nor a per-run one.
	var a: bench.Report = bench.run("work", 5, 100, () => { acc = acc + one_allocation(3); });
	var b: bench.Report = bench.run("work", 5, 200, () => { acc = acc + one_allocation(3); });
	if (a.allocs <= (0 as i64)) { return 92; }
	if (b.allocs != a.allocs * (2 as i64)) { return 93; }
	if (a.allocs_per_op_milli() != b.allocs_per_op_milli()) { return 94; }
	// A quiet region after a mark must read clean, and the reads themselves
	// are scalars, so asking the question cannot change the answer.
	var at: i64 = bench.mark_allocs();
	var i: i32 = 0;
	var sum: i32 = 0;
	while (i < 100000) { sum = sum + i; i = i + 1; }
	if (sum == 0) { return 95; }
	if (!bench.no_allocs_since(at)) { return 96; }
	// And the observable is not simply stuck at zero: one more string moves it.
	if (one_allocation(1) == 0) { return 97; }
	if (bench.allocs_since(at) <= (0 as i64)) { return 98; }
	return 42;
}
`

func TestBenchHarnessDoesNotChargeItsOwnAllocations(t *testing.T) {
	fern := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "neutral.fern")
	if err := os.WriteFile(src, []byte(benchNeutralSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "neutral")
	if out, err := exec.Command(fern, "-target", "x86-64-linux", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	cmd := exec.Command(bin)
	out, _ := cmd.CombinedOutput()
	// 91 means the harness allocated on an empty body's behalf, 93/94 that it
	// charges something per run or per iteration, 96 that reading the count
	// allocated, 98 that the count never moves and the rest passed for the
	// wrong reason.
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Fatalf("exit %d, want 42 — see the source for what each code names\n%s", code, out)
	}
}

// The harness reports the two observables separately because they answer
// different questions: a recycling steady state is busy in allocator CALLS and
// flat in fresh BYTES. A report that collapsed them would make the recycling
// case indistinguishable from the zero-allocation one, which is the whole
// distinction the experiments rest on.
const benchRecyclingSrc = `import "std/bench";
import "std/i32";
import "std/string";

function one_allocation(n: i32): i32 {
	var s: string = "abcdefgh" + n.to_string();
	return s.len();
}

function main(): i32 {
	var acc: i32 = 0;
	var r: bench.Report = bench.run("recycling", 5, 500, () => { acc = acc + one_allocation(3); });
	print(r.allocs.to_string() + " " + r.fresh_bytes.to_string());
	return 0;
}
`

func TestBenchReportsCallsAndBytesSeparately(t *testing.T) {
	fern := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "recycling.fern")
	if err := os.WriteFile(src, []byte(benchRecyclingSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "recycling")
	if out, err := exec.Command(fern, "-target", "x86-64-linux", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) != 2 {
		t.Fatalf("want `<allocs> <fresh_bytes>`, got %q", out)
	}
	allocs, err := strconv.Atoi(fields[0])
	if err != nil {
		t.Fatalf("allocs: %v", err)
	}
	fresh, err := strconv.Atoi(fields[1])
	if err != nil {
		t.Fatalf("fresh_bytes: %v", err)
	}
	if allocs%500 != 0 || allocs == 0 {
		t.Errorf("allocs = %d over 500 iterations: not a whole number per iteration, so something outside the body is counted", allocs)
	}
	// The warm-up bought the block this loop then recycles 500 times, so the
	// measured region buys nothing. Bytes far below calls is the signature of
	// recycling; equal-and-growing would be a leak.
	if fresh >= allocs {
		t.Errorf("fresh_bytes = %d with %d allocations: the steady state is buying memory, not recycling it", fresh, allocs)
	}
}
