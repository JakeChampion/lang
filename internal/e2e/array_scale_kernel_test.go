package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// The scale kernel rewrite (#9735), from the outside.
//
// The IR tests prove the rewrite fires where it should and declines where it
// should not. They cannot prove it computes the same thing: swapping a scalar
// loop for a vectorised kernel is exactly the transform that gives a
// plausible-looking wrong answer — the last element or two dropped where the
// block loop hands over to its tail, or a sign lost on a negative factor.
//
// So this compares each rewritten shape against a hand-written loop inside
// the program, over the lengths where a 4-lane block (x86-64 AVX2) and a
// 2-lane one (arm64 NEON, wasm v128) hand over to their tails: 0 through 9
// covers two whole blocks and every tail remainder of both.
const arrayScaleKernelSrc = `import "std/array";

// Each of these is the shape the kernel takes: one multiply by a literal.
function scale2(xs: f64[]): f64[] { return xs.map((x: f64): f64 => x * 2.0); }
function scale_neg(xs: f64[]): f64[] { return xs.map((x: f64): f64 => x * -1.5); }
function scale_zero(xs: f64[]): f64[] { return xs.map((x: f64): f64 => x * 0.0); }

// The named spelling, which reaches the call unparked.
function half(x: f64): f64 { return x * 0.5; }
function scale_named(xs: f64[]): f64[] { return xs.map(half); }

// A closure bound to a variable, which the pass DECLINES -- its build sits
// before the receiver is evaluated, so the range that would cancel it would
// take the receiver with it. Here to prove the declined shape still runs,
// and still answers what the loop answers.
function scale_bound(xs: f64[]): f64[] {
	var f: (f64) => f64 = (x: f64): f64 => x * 2.0;
	return xs.map(f);
}

// The same transform written as a loop. It is not a map at all, so nothing
// rewrites it and it stays a real control.
function loop_scale(xs: f64[], k: f64): f64[] {
	var out: f64[] = [];
	var i: i32 = 0;
	while (i < xs.len()) { out = out.append(xs[i] * k); i = i + 1; }
	return out;
}

// All NaNs count as equal: a payload is not guaranteed across backends, and
// the sign of a NaN product is not what this is testing.
function bits_equal(a: f64, b: f64): boolean {
	if (a != a) { return b != b; }
	return a == b;
}

function same(a: f64[], b: f64[]): boolean {
	if (a.len() != b.len()) { return false; }
	var i: i32 = 0;
	while (i < a.len()) {
		if (!bits_equal(a[i], b[i])) { return false; }
		i = i + 1;
	}
	return true;
}

// Values a scale can go wrong on: sign, zero, a fraction, and an infinity
// whose product with zero is NaN.
function build(n: i32): f64[] {
	var inf: f64 = 1.0e308 * 10.0;
	var seed: f64[] = [1.0, -2.0, 0.0, 0.5, -0.25, 1000000.0, inf, -1.0, 7.5];
	var xs: f64[] = [];
	var i: i32 = 0;
	while (i < n) { xs = xs.append(seed[i]); i = i + 1; }
	return xs;
}

function main(): i32 {
	var n: i32 = 0;
	while (n <= 9) {
		var xs: f64[] = build(n);
		if (!same(scale2(xs), loop_scale(xs, 2.0))) { return 10 + n; }
		if (!same(scale_neg(xs), loop_scale(xs, -1.5))) { return 30 + n; }
		if (!same(scale_zero(xs), loop_scale(xs, 0.0))) { return 50 + n; }
		if (!same(scale_named(xs), loop_scale(xs, 0.5))) { return 70 + n; }
		if (!same(scale_bound(xs), loop_scale(xs, 2.0))) { return 110 + n; }
		n = n + 1;
	}

	// The kernel's result is a normal array afterwards: its length header is
	// right, it indexes, and it appends.
	var ys: f64[] = scale2(build(3));
	if (ys.len() != 3) { return 91; }
	if (ys[1] != -4.0) { return 92; }
	var zs: f64[] = ys.append(99.0);
	if (zs.len() != 4 || zs[3] != 99.0) { return 93; }

	// The receiver is untouched by the kernel, which borrows it.
	var src: f64[] = build(5);
	var out: f64[] = scale2(src);
	if (!same(src, build(5))) { return 94; }
	if (out.len() != 5) { return 95; }
	return 0;
}
`

// 1x/3x/5x/7x = the length at which a scaled shape disagreed with the loop,
// for the doubling, the negative factor, the zero factor and the named
// element function; 9x = the result-shape and borrowed-receiver checks;
// 11x = the variable-bound shape the pass declines, which still has to run.
func TestArm64ScaleKernelMatchesTheLoop(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, arrayScaleKernelSrc); code != 0 {
		t.Errorf("scale kernel on arm64: got %d, want 0", code)
	}
}

func TestX86_64ScaleKernelMatchesTheLoop(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, arrayScaleKernelSrc); code != 0 {
		t.Errorf("scale kernel on x86-64: got %d, want 0", code)
	}
}

func TestWASMScaleKernelMatchesTheLoop(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, arrayScaleKernelSrc); got != 0 {
		t.Errorf("scale kernel on wasm: got %d, want 0", got)
	}
}

func TestInterpScaleKernelMatchesTheLoop(t *testing.T) {
	if got := runInterpExit(t, arrayScaleKernelSrc); got != 0 {
		t.Errorf("scale kernel on interp: got %d, want 0", got)
	}
}
