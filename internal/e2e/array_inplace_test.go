package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Ownership-aware materialization for a same-shape map (#9733), from the
// outside.
//
// The IR tests prove the rewrite fires where it should and declines where it
// should not. They cannot prove it computes the same thing: writing through
// the donor's buffer while reading from it is exactly the transform that gives
// a plausible-looking wrong answer — off by one element, or every element
// folded through its own overwritten neighbour.
//
// So this compares the combinator against a hand-written loop inside the
// program, over the shapes where an in-place write goes wrong differently:
// empty, one element, a length that crosses the header read, and a relay
// through a second `own` frame.
const arrayInPlaceSrc = `import "std/array";

function dbl(x: i64): i64 { return x * (2 as i64); }
function twice(own xs: i64[]): i64[] { return xs.map((x: i64): i64 => dbl(x)); }

// The donor reaches twice through a second frame, which is the longest chain
// E051 admits for an owned argument.
function relay(own ys: i64[]): i64[] { return twice(ys); }

function build(n: i32): i64[] {
	var xs: i64[] = [];
	var i: i32 = 0;
	while (i < n) { xs = xs.append((i as i64) + (1 as i64)); i = i + 1; }
	return xs;
}

// The same transform written as a loop over a borrowed array, so it cannot be
// rewritten and is a real control.
function loop_twice(xs: i64[]): i64[] {
	var out: i64[] = [];
	var i: i32 = 0;
	while (i < xs.len()) { out = out.append(dbl(xs[i])); i = i + 1; }
	return out;
}

function same(a: i64[], b: i64[]): boolean {
	if (a.len() != b.len()) { return false; }
	var i: i32 = 0;
	while (i < a.len()) {
		if (a[i] != b[i]) { return false; }
		i = i + 1;
	}
	return true;
}

function main(): i32 {
	var sizes: i32[] = [0, 1, 2, 3, 17, 64];
	var i: i32 = 0;
	while (i < sizes.len()) {
		var n: i32 = sizes[i];
		if (!same(twice(build(n)), loop_twice(build(n)))) { return 90 + i; }
		if (!same(relay(build(n)), loop_twice(build(n)))) { return 80 + i; }
		i = i + 1;
	}

	// A fresh construction at the call site, which is the other shape E051
	// admits, and the one a reader will write first.
	var d: i64[] = twice([5 as i64, 6 as i64]);
	if (d[0] != 10 as i64) { return 70; }
	if (d[1] != 12 as i64) { return 71; }
	if (d.len() != 2) { return 72; }

	// The donated buffer is a normal array afterwards: appendable, indexable,
	// and its length header survived being written through.
	var e: i64[] = twice(build(4)).append(99 as i64);
	if (e.len() != 5) { return 73; }
	if (e[4] != 99 as i64) { return 74; }
	if (e[0] != 2 as i64) { return 75; }
	return 0;
}
`

// 9x = the size at which the direct call disagreed with the loop, 8x = the
// same through a relay, 7x = the fresh-construction and post-donation checks.
func TestArm64OwnedMapMatchesTheLoop(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, arrayInPlaceSrc); code != 0 {
		t.Errorf("owned map on arm64: got %d, want 0", code)
	}
}

func TestX86_64OwnedMapMatchesTheLoop(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, arrayInPlaceSrc); code != 0 {
		t.Errorf("owned map on x86-64: got %d, want 0", code)
	}
}

func TestWASMOwnedMapMatchesTheLoop(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, arrayInPlaceSrc); got != 0 {
		t.Errorf("owned map on wasm: got %d, want 0", got)
	}
}

func TestInterpOwnedMapMatchesTheLoop(t *testing.T) {
	if got := runInterpExit(t, arrayInPlaceSrc); got != 0 {
		t.Errorf("owned map on interp: got %d, want 0", got)
	}
}
