package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Lazy core/iter chains fused into one loop (#6795). Each chain starts from
// `iter.of(xs)` or `iter.range(lo, hi)`, runs `map`, `filter`, `take` and
// `flat_map` adapters, and ends in `fold` or `count`; a `skip` chain runs
// unfused beside them. It is compared with a hand-written
// loop inside the program, with fusion on and off, and the leak checker
// confirms nothing the unfused chains build is left behind.
const iterFusionSrc = `import "core/iter";

function build(n: i32): i64[] {
	let xs: i64[] = [];
	let i: i32 = 0;
	while (i < n) { xs = xs.append((i as i64) * (3 as i64) - (7 as i64)); i = i + 1; }
	return xs;
}

function via_filter_map(xs: i64[]): i64 {
	return iter.of(xs)
		.filter((x: i64): boolean => x % (2 as i64) == (0 as i64))
		.map((x: i64): i64 => x * (10 as i64))
		.fold(0 as i64, (a: i64, b: i64): i64 => a * (3 as i64) + b);
}
function loop_filter_map(xs: i64[]): i64 {
	let acc: i64 = 0 as i64;
	let i: i32 = 0;
	while (i < xs.len()) {
		if (xs[i] % (2 as i64) == (0 as i64)) { acc = acc * (3 as i64) + xs[i] * (10 as i64); }
		i = i + 1;
	}
	return acc;
}

function via_take(xs: i64[], k: i32): i64 {
	return iter.of(xs)
		.map((x: i64): i64 => x + (1 as i64))
		.take(k)
		.fold(0 as i64, (a: i64, b: i64): i64 => a * (2 as i64) + b);
}
function loop_take(xs: i64[], k: i32): i64 {
	let acc: i64 = 0 as i64;
	let i: i32 = 0;
	while (i < xs.len() && i < k) { acc = acc * (2 as i64) + xs[i] + (1 as i64); i = i + 1; }
	return acc;
}

function via_float(xs: i64[]): f64 {
	return iter.of(xs)
		.map((x: i64): f64 => (x as f64) * 0.5)
		.filter((y: f64): boolean => y > 1.0)
		.fold(0.25, (a: f64, b: f64): f64 => a + b);
}
function loop_float(xs: i64[]): f64 {
	let acc: f64 = 0.25;
	let i: i32 = 0;
	while (i < xs.len()) {
		let y: f64 = (xs[i] as f64) * 0.5;
		if (y > 1.0) { acc = acc + y; }
		i = i + 1;
	}
	return acc;
}

function via_bare(xs: i64[]): i64 {
	return iter.of(xs).fold(7 as i64, (a: i64, b: i64): i64 => a - b);
}
function loop_bare(xs: i64[]): i64 {
	let acc: i64 = 7 as i64;
	let i: i32 = 0;
	while (i < xs.len()) { acc = acc - xs[i]; i = i + 1; }
	return acc;
}

function via_count(xs: i64[]): i32 {
	return iter.of(xs).filter((x: i64): boolean => x > (2 as i64)).count();
}
function loop_count(xs: i64[]): i32 {
	let n: i32 = 0;
	let i: i32 = 0;
	while (i < xs.len()) { if (xs[i] > (2 as i64)) { n = n + 1; } i = i + 1; }
	return n;
}

function via_range(lo: i32, hi: i32): i32 {
	return iter.range(lo, hi).map((i: i32): i32 => i * i - 3).fold(1, (a: i32, b: i32): i32 => a * 3 + b);
}
function loop_range(lo: i32, hi: i32): i32 {
	let acc: i32 = 1;
	let i: i32 = lo;
	while (i < hi) { acc = acc * 3 + i * i - 3; i = i + 1; }
	return acc;
}

function via_range_count(lo: i32, hi: i32, k: i32): i32 {
	return iter.range(lo, hi).filter((i: i32): boolean => i % 3 != 0).take(k).count();
}
function loop_range_count(lo: i32, hi: i32, k: i32): i32 {
	let n: i32 = 0;
	let i: i32 = lo;
	while (i < hi && n < k) { if (i % 3 != 0) { n = n + 1; } i = i + 1; }
	return n;
}

function via_flat_map(xs: i64[]): i64 {
	return iter.of(xs)
		.flat_map((x: i64): i64[] => { if (x % (3 as i64) == (0 as i64)) { let none: i64[] = []; return none; } return [x, x * (2 as i64)]; })
		.fold(0 as i64, (a: i64, b: i64): i64 => a * (3 as i64) + b);
}
function loop_flat_map(xs: i64[]): i64 {
	let acc: i64 = 0 as i64;
	let i: i32 = 0;
	while (i < xs.len()) {
		if (xs[i] % (3 as i64) != (0 as i64)) { acc = acc * (3 as i64) + xs[i]; acc = acc * (3 as i64) + xs[i] * (2 as i64); }
		i = i + 1;
	}
	return acc;
}

function via_skip(xs: i64[], k: i32): i64 {
	return iter.of(xs).skip(k).map((x: i64): i64 => x - (1 as i64)).fold(0 as i64, (a: i64, b: i64): i64 => a * (2 as i64) + b);
}
function loop_skip(xs: i64[], k: i32): i64 {
	let acc: i64 = 0 as i64;
	let i: i32 = k;
	if (i < 0) { i = 0; }
	while (i < xs.len()) { acc = acc * (2 as i64) + xs[i] - (1 as i64); i = i + 1; }
	return acc;
}

function check(n: i32): i32 {
	let xs: i64[] = build(n);
	if (via_filter_map(xs) != loop_filter_map(xs)) { return 10; }
	if (via_float(xs) != loop_float(xs)) { return 11; }
	if (via_bare(xs) != loop_bare(xs)) { return 12; }
	if (via_count(xs) != loop_count(xs)) { return 13; }
	if (via_range(0 - n, n) != loop_range(0 - n, n)) { return 14; }
	if (via_flat_map(xs) != loop_flat_map(xs)) { return 17; }
	let counts: i32[] = [0 - 1, 0, 1, 2, 100];
	for k in counts {
		if (via_take(xs, k) != loop_take(xs, k)) { return 15; }
		if (via_range_count(0 - n, n, k) != loop_range_count(0 - n, n, k)) { return 16; }
		if (via_skip(xs, k) != loop_skip(xs, k)) { return 18; }
	}
	return 0;
}

function main(): i32 {
	let sizes: i32[] = [0, 1, 2, 3, 17];
	let i: i32 = 0;
	while (i < sizes.len()) {
		let bad: i32 = check(sizes[i]);
		if (bad != 0) { return bad * 5 + i - 40; }
		i = i + 1;
	}
	if (__rc_underflow_count() != 0) { return 99; }
	return 0;
}
`

// Exit 10..54 names the case that disagreed with its loop and the size it
// disagreed at: (case * 5 + size index) - 40. 99 is a refcount underflow.
func TestSelfHostIterFusion(t *testing.T) {
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			for _, disabled := range []string{"0", "1"} {
				t.Run("disabled="+disabled, func(t *testing.T) {
					bin := e2eharness.CompileSelfHostSource(t, target, iterFusionSrc,
						[]string{"FERN_NO_ARRAY_FUSION=" + disabled, "FERN_LEAKCHECK=1"})
					cmd := runScaleTarget(t, target, bin)
					out, err := cmd.CombinedOutput()
					if err != nil {
						code := cmd.ProcessState.ExitCode()
						t.Fatalf("got exit %d: case %d at size index %d\n%s", code, (code+40)/5, (code+40)%5, out)
					}
					var allocs, frees, live int64
					if _, err := fmtSscan(leakSummaryLine(string(out)), &allocs, &frees, &live); err != nil || allocs == 0 || allocs != frees || live != 0 {
						t.Fatalf("lazy chain ownership: %v\n%s", err, out)
					}
				})
			}
		})
	}
}

// A fused lazy chain allocates nothing: not the iterator, not an adapter, not
// the Option and tuple each `next` returns. Unfused, the same calls allocate,
// which keeps the zero from passing vacuously. The program prints how many
// allocations each chain made; exit 20..23 names a chain with a wrong result.
const iterFusionAllocationSrc = `import "core/iter";
import "std/i64";
@noinline function mapped(xs: i64[]): i64 {
	return iter.of(xs).filter((x: i64): boolean => x > (1 as i64)).map((x: i64): i64 => x * x).fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
@noinline function taken(xs: i64[]): i64 {
	return iter.of(xs).take(3).fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
@noinline function counted(xs: i64[]): i32 {
	return iter.of(xs).filter((x: i64): boolean => x % (2 as i64) == (0 as i64)).count();
}
@noinline function ranged(n: i32): i32 {
	return iter.range(0, n).map((i: i32): i32 => i + 1).fold(0, (a: i32, b: i32): i32 => a + b);
}
function main(): i32 {
	let xs: i64[] = [1 as i64, 2 as i64, 3 as i64, 4 as i64, 5 as i64, 6 as i64];
	let mark = __heap_alloc_count();
	if (mapped(xs) != (90 as i64)) { return 20; }
	let a = __heap_alloc_count() - mark;
	mark = __heap_alloc_count();
	if (taken(xs) != (6 as i64)) { return 21; }
	let b = __heap_alloc_count() - mark;
	mark = __heap_alloc_count();
	if (counted(xs) != 3) { return 22; }
	let c = __heap_alloc_count() - mark;
	mark = __heap_alloc_count();
	if (ranged(10) != 55) { return 23; }
	let d = __heap_alloc_count() - mark;
	print("allocs " + a.to_string() + " " + b.to_string() + " " + c.to_string() + " " + d.to_string());
	return 0;
}`

func TestSelfHostIterFusionAllocatesNothing(t *testing.T) {
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			for _, disabled := range []string{"0", "1"} {
				t.Run("disabled="+disabled, func(t *testing.T) {
					bin := e2eharness.CompileSelfHostSource(t, target, iterFusionAllocationSrc, []string{"FERN_NO_ARRAY_FUSION=" + disabled})
					cmd := runScaleTarget(t, target, bin)
					out, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("got exit %d\n%s", cmd.ProcessState.ExitCode(), out)
					}
					var counts [4]int64
					if _, err := fmt.Sscan(strings.TrimPrefix(strings.TrimSpace(string(out)), "allocs "), &counts[0], &counts[1], &counts[2], &counts[3]); err != nil {
						t.Fatalf("no allocation counts: %v\n%s", err, out)
					}
					for i, n := range counts {
						if disabled == "0" && n != 0 || disabled == "1" && n == 0 {
							t.Fatalf("chain %d made %d allocations with fusion disabled=%s\n%s", i, n, disabled, out)
						}
					}
				})
			}
		})
	}
}

// The report names each lazy chain it fuses and why it leaves one alone.
const iterFusionReportSrc = `import "core/iter";
function filtered(xs: i64[]): i64 {
	return iter.of(xs).filter((x: i64): boolean => x > (1 as i64)).map((x: i64): i64 => x * x).fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
function counted(n: i32): i32 {
	return iter.range(0, n).take(4).count();
}
function numbered(xs: i64[]): i64 {
	return iter.of(xs).enumerate().fold(0 as i64, (a: i64, p: (i32, i64)): i64 => a + p.1);
}
function fanned(xs: i64[]): i64 {
	return iter.of(xs).flat_map((x: i64): i64[] => [x, x]).fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
function skipped(xs: i64[]): i64 {
	return iter.of(xs).skip(1).fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
function shared(xs: i64[]): i64 {
	let m = iter.of(xs).map((x: i64): i64 => x + (1 as i64));
	return m.fold(0 as i64, (a: i64, b: i64): i64 => a + b) + m.fold(1 as i64, (a: i64, b: i64): i64 => a * b);
}
function main(): i32 {
	let xs: i64[] = [1 as i64, 2 as i64, 3 as i64];
	if (filtered(xs) + numbered(xs) + fanned(xs) + skipped(xs) + shared(xs) != (69 as i64) || counted(9) != 4) { return 1; }
	return 0;
}
`

func TestSelfHostIterFusionReport(t *testing.T) {
	_, runner, driver := buildModloadDriverX86(t)
	dir := e2eharness.WriteSourceModloadProject(t, iterFusionReportSrc)
	cmd := runX86_64Bin(runner, driver, filepath.Join(dir, "main.fern"))
	cmd.Env = append(os.Environ(), "FERN_ARRAY_REPORT=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if _, err := cmd.Output(); err != nil {
		t.Fatalf("compile: %v\n%s", err, stderr.String())
	}
	report := stderr.String()
	for _, tc := range []struct{ name, plan, reason string }{
		{"filtered", "of -> filter -> map -> fold; one traversal", "fused"},
		{"counted", "range -> take -> count; one traversal", "fused"},
		{"numbered", "enumerate; not fused", "operator-outside-algebra"},
		{"fanned", "of -> flat_map -> fold; one traversal", "fused"},
		{"skipped", "skip; not fused", "operator-outside-algebra"},
		{"shared", "fold; not fused", "intermediate-shared"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := strings.Index(report, tc.name+": "+tc.plan+"\n")
			if start < 0 {
				t.Fatalf("missing plan %s: %s\n%s", tc.name, tc.plan, report)
			}
			diagnostic := strings.SplitN(report[start:], "\n", 3)[1]
			if !strings.Contains(diagnostic, "reason="+tc.reason+" ") {
				t.Fatalf("wrong decision: %s", diagnostic)
			}
		})
	}
	t.Run("runs", func(t *testing.T) {
		cmd := runScaleTarget(t, e2eharness.TargetX86_64Linux, e2eharness.CompileSelfHostSource(t, e2eharness.TargetX86_64Linux, iterFusionReportSrc, nil))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("got exit %d\n%s", cmd.ProcessState.ExitCode(), out)
		}
	})
}
