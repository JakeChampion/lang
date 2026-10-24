package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRunnerBenchExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/bench_test.fern")
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

const benchNeutralSrc = `import "std/bench";
import "std/i32";
import "std/string";

function one_allocation(n: i32): i32 {
	let s: string = "abcdefgh" + n.to_string();
	return s.len();
}

function main(): i32 {
	let acc: i32 = 0;
	// An empty body: the harness's own cost, and it must be nothing.
	let quiet: bench.Report = bench.run("quiet", 5, 200, () => { acc = acc + 1; });
	if (acc == 0) { return 90; }
	if (quiet.allocs != (0 as i64)) { return 91; }
	// A fixed body, twice: the count must scale exactly, so the harness adds
	// neither a per-iteration cost nor a per-run one.
	let a: bench.Report = bench.run("work", 5, 100, () => { acc = acc + one_allocation(3); });
	let b: bench.Report = bench.run("work", 5, 200, () => { acc = acc + one_allocation(3); });
	if (a.allocs <= (0 as i64)) { return 92; }
	if (b.allocs != a.allocs * (2 as i64)) { return 93; }
	if (a.allocs_per_op_milli() != b.allocs_per_op_milli()) { return 94; }
	// A quiet region after a mark must read clean, and the reads themselves
	// are scalars, so asking the question cannot change the answer.
	let mk: i64 = bench.alloc_count();
	let i: i32 = 0;
	let sum: i32 = 0;
	while (i < 100000) { sum = sum + i; i = i + 1; }
	if (sum == 0) { return 95; }
	if (!bench.no_allocs_after(mk)) { return 96; }
	// And the observable is not simply stuck at zero: one more string moves it.
	if (one_allocation(1) == 0) { return 97; }
	if (bench.allocs_after(mk) <= (0 as i64)) { return 98; }
	return 42;
}
`

func benchX86Cmd(runner []string, bin string, args ...string) *exec.Cmd {
	if len(runner) == 0 {
		return exec.Command(bin, args...)
	}
	return exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), bin), args...)...)
}

func TestBenchHarnessDoesNotChargeItsOwnAllocations(t *testing.T) {
	runner := x86NativeRunner(t)
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
	cmd := benchX86Cmd(runner, bin)
	out, _ := cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Fatalf("exit %d, want 42 — see the source for what each code names\n%s", code, out)
	}
}

const benchRecyclingSrc = `import "std/bench";
import "std/i32";
import "std/string";

function one_allocation(n: i32): i32 {
	let s: string = "abcdefgh" + n.to_string();
	return s.len();
}

function main(): i32 {
	let acc: i32 = 0;
	let r: bench.Report = bench.run("recycling", 5, 500, () => { acc = acc + one_allocation(3); });
	print(r.allocs.to_string() + " " + r.fresh_bytes.to_string());
	return 0;
}
`

func TestBenchReportsCallsAndBytesSeparately(t *testing.T) {
	runner := x86NativeRunner(t)
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
	out, err := benchX86Cmd(runner, bin).CombinedOutput()
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
	if fresh >= allocs {
		t.Errorf("fresh_bytes = %d with %d allocations: the steady state is buying memory, not recycling it", fresh, allocs)
	}
}
