package e2e

import (
	"strings"
	"testing"
)

// __scale_f64 is the eighth kernel of docs/ATLAS-PLATFORM-PLAN.md §3, and the
// first over an ARRAY rather than a string, with a buffer out rather than a
// scalar: it allocates its result, header and all, so the caller owns one
// fresh f64[] per call.
//
// It ships SCALAR on all eight backends at once, which is §3.4's step 1, and
// this corpus is written while every body is still one multiply per element
// so that it is not written to fit whatever the vector code later does.
//
// What a port of a buffer-out kernel gets wrong, and none of it is the
// arithmetic:
//
//   - the HEADER. cap, rc and len sit at data-12 / -8 / -4 on every native
//     backend, and the length is what every later read and the release
//     consult; a body that writes the length into the wrong slot passes the
//     first element read and fails `len()`.
//   - the TAIL. A vector body handles a whole block at a time, so lengths
//     that are not a multiple of the lane count are the ones that lose an
//     element or write one past the end. The sweep covers 0..40 and the
//     boundaries of a two- and four-lane block.
//   - the ALLOCATION. Exactly one, for the result: a body that appends
//     element by element allocates the geometric regrows too, and one that
//     writes into its input allocates none and corrupts the caller. The
//     native legs count allocations across the call.
//   - the INPUT. It is borrowed, so it must read the same after the call,
//     and the result must be a different buffer.
//
// The program is self-checking against a Fern reference loop and prints its
// verdict, 42 for every check passed, so the same source runs on every leg
// and the corpus runners' exit-0 convention holds.
const scaleF64Body = `import "std/array";
import "std/i32";

function build(n: i32, seed: f64): f64[] {
    var xs: f64[] = [];
    var i: i32 = 0;
    while (i < n) {
        xs = xs.append(seed + (i as f64) * 1.25 - 7.0);
        i = i + 1;
    }
    return xs;
}

function ref(xs: f64[], k: f64): f64[] {
    var out: f64[] = [];
    var i: i32 = 0;
    while (i < xs.len()) {
        out = out.append(xs[i] * k);
        i = i + 1;
    }
    return out;
}

// Bitwise-or-both-NaN equality: a kernel owes the reference's exact bits.
function same(a: f64, b: f64): boolean {
    if (a == b) { return true; }
    return a != a && b != b;
}

function check(xs: f64[], k: f64, counters: boolean): i32 {
    var want: f64[] = ref(xs, k);
    var before: f64[] = ref(xs, 1.0);
    var c0: i64 = __heap_alloc_count();
    var got: f64[] = __scale_f64(xs, k);
    var c1: i64 = __heap_alloc_count();
    if (got.len() != xs.len()) { return 1; }
    var i: i32 = 0;
    while (i < xs.len()) {
        if (!same(got[i], want[i])) { return 2; }
        if (!same(xs[i], before[i])) { return 3; }
        i = i + 1;
    }
    if (counters && c1 - c0 != 1 as i64) { return 4; }
    // The result is a fresh buffer: writing it leaves the input alone.
    if (xs.len() > 0) {
        var w: f64[] = got.with(0, 12345.5);
        if (!same(xs[0], before[0]) || w[0] != 12345.5) { return 5; }
    }
    return 0;
}

function verdict(): i32 {
    var probe: i64 = __heap_alloc_count();
    var warm: f64[] = build(3, 0.0);
    var counters: boolean = __heap_alloc_count() - probe > 0 as i64;
    if (warm.len() != 3) { return 90; }
    var ks: f64[] = [2.5, 0.0 - 0.5, 0.0, 1.0, 3.0e300, 0.0 / 0.0, 1.0e-310];
    var n: i32 = 0;
    while (n <= 40) {
        var xs: f64[] = build(n, 0.5);
        var j: i32 = 0;
        while (j < ks.len()) {
            var code: i32 = check(xs, ks[j], counters);
            if (code != 0) { return 10 + code; }
            j = j + 1;
        }
        n = n + 1;
    }
    var big: i32[] = [63, 64, 65, 127, 128, 129, 1000, 4097];
    var b: i32 = 0;
    while (b < big.len()) {
        var xs: f64[] = build(big[b], 0.0 - 100.0);
        var code: i32 = check(xs, 2.5, counters);
        if (code != 0) { return 20 + code; }
        b = b + 1;
    }
    // The wrapper the kernel is for agrees with it exactly.
    var v: f64[] = build(17, 3.0);
    var a: f64[] = array.scale_f64(v, 0.75);
    var g: f64[] = __scale_f64(v, 0.75);
    var q: i32 = 0;
    while (q < 17) {
        if (!same(a[q], g[q])) { return 30; }
        q = q + 1;
    }
    return 42;
}
`

// The verdict as the exit status, for the legs that read one.
const scaleF64Src = scaleF64Body + `
function main(): i32 { return verdict(); }
`

// The verdict printed, for the corpus runners that require exit 0 and hand
// back stdout.
const scaleF64PrintingSrc = scaleF64Body + `
function main(): i32 {
    write(verdict().to_string());
    write("\n");
    return 0;
}
`

func scaleF64Verdict(t *testing.T, out string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if got := strings.TrimSpace(lines[len(lines)-1]); got != "42" {
		t.Errorf("__scale_f64 verdict = %q, want 42 (see scaleF64Src for what each code means)\noutput:\n%s", got, out)
	}
}

func TestInterpScaleF64(t *testing.T) {
	if got := runInterpExit(t, scaleF64Src); got != 42 {
		t.Errorf("__scale_f64 on interp = %d, want 42 (see scaleF64Src for what each code means)", got)
	}
}

func TestX86_64ScaleF64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, scaleF64Src); got != 42 {
		t.Errorf("__scale_f64 on x86-64 = %d, want 42 (see scaleF64Src for what each code means)", got)
	}
}

func TestArm64ScaleF64(t *testing.T) {
	if _, got := compileAndRunArm64(t, scaleF64Src); got != 42 {
		t.Errorf("__scale_f64 on arm64 = %d, want 42 (see scaleF64Src for what each code means)", got)
	}
}

func TestWASMScaleF64(t *testing.T) {
	if got := runWasm(t, scaleF64Src); got != 42 {
		t.Errorf("__scale_f64 on wasm = %d, want 42 (see scaleF64Src for what each code means)", got)
	}
}

// The `-backend ssa` legs, which §3.4 counts as backends seven and eight and
// which an adoption forgets first, so they get the lowering and the coverage
// with the other six rather than after.
func TestArm64SSAScaleF64(t *testing.T) {
	scaleF64Verdict(t, arm64SSACorpusRunner(t)(t, scaleF64PrintingSrc))
}

func TestX86_64SSAScaleF64(t *testing.T) {
	scaleF64Verdict(t, x86_64SSACorpusRunner(t)(t, scaleF64PrintingSrc))
}
