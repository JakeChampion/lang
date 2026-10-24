package e2e

import (
	"testing"
)

const scaleF64Body = `import "std/array";
import "std/i32";

function build(n: i32, seed: f64): f64[] {
    let xs: f64[] = [];
    let i: i32 = 0;
    while (i < n) {
        xs = xs.append(seed + (i as f64) * 1.25 - 7.0);
        i = i + 1;
    }
    return xs;
}

function ref(xs: f64[], k: f64): f64[] {
    let out: f64[] = [];
    let i: i32 = 0;
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
    let want: f64[] = ref(xs, k);
    let before: f64[] = ref(xs, 1.0);
    let c0: i64 = __heap_alloc_count();
    let got: f64[] = __scale_f64(xs, k);
    let c1: i64 = __heap_alloc_count();
    if (got.len() != xs.len()) { return 1; }
    let i: i32 = 0;
    while (i < xs.len()) {
        if (!same(got[i], want[i])) { return 2; }
        if (!same(xs[i], before[i])) { return 3; }
        i = i + 1;
    }
    if (counters && c1 - c0 != 1 as i64) { return 4; }
    // The result is a fresh buffer: writing it leaves the input alone.
    if (xs.len() > 0) {
        let w: f64[] = got.with(0, 12345.5);
        if (!same(xs[0], before[0]) || w[0] != 12345.5) { return 5; }
    }
    return 0;
}

function verdict(): i32 {
    let probe: i64 = __heap_alloc_count();
    let warm: f64[] = build(3, 0.0);
    let counters: boolean = __heap_alloc_count() - probe > 0 as i64;
    if (warm.len() != 3) { return 90; }
    let ks: f64[] = [2.5, 0.0 - 0.5, 0.0, 1.0, 3.0e300, 0.0 / 0.0, 1.0e-310];
    let n: i32 = 0;
    while (n <= 40) {
        let xs: f64[] = build(n, 0.5);
        let j: i32 = 0;
        while (j < ks.len()) {
            let code: i32 = check(xs, ks[j], counters);
            if (code != 0) { return 10 + code; }
            j = j + 1;
        }
        n = n + 1;
    }
    let big: i32[] = [63, 64, 65, 127, 128, 129, 1000, 4097];
    let b: i32 = 0;
    while (b < big.len()) {
        let xs: f64[] = build(big[b], 0.0 - 100.0);
        let code: i32 = check(xs, 2.5, counters);
        if (code != 0) { return 20 + code; }
        b = b + 1;
    }
    // The wrapper the kernel is for agrees with it exactly.
    let v: f64[] = build(17, 3.0);
    let a: f64[] = array.scale_f64(v, 0.75);
    let g: f64[] = __scale_f64(v, 0.75);
    let q: i32 = 0;
    while (q < 17) {
        if (!same(a[q], g[q])) { return 30; }
        q = q + 1;
    }
    return 42;
}
`

const scaleF64Src = scaleF64Body + `
function main(): i32 { return verdict(); }
`

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
