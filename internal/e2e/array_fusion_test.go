package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Producer-consumer fusion of std/array pipelines (#9731), from the outside.
//
// internal/ir's tests prove the chain became one loop. They cannot prove it
// became the RIGHT loop: a fused pipeline that drops the last element, or
// seeds `reduce` with `h(x, x)` on a singleton, passes every structural check
// there and returns the wrong number here. So this asserts the answers, and
// asserts them on each backend rather than one — the fused loop is emitted
// from a single op stream, but the backends disagree about how strictly that
// stream is typed, and wasm rejected an i64 element in an i32 slot that arm64
// had been running happily.
//
// Each case computes the same thing twice: once through the combinators, once
// through a hand-written loop written not to be fusible. Comparing the two
// inside the program is what makes this a differential test rather than a set
// of golden numbers that a wrong-but-consistent fusion could be adjusted to.
const arrayFusionSrc = `import "std/array";

function build(n: i32): i64[] {
	var xs: i64[] = [];
	var i: i32 = 0;
	while (i < n) { xs = xs.append((i as i64) + (1 as i64)); i = i + 1; }
	return xs;
}

// map -> fold, and the same sum spelled as a loop.
function via_map_fold(xs: i64[]): i64 {
	return xs.map((x: i64): i64 => x * (3 as i64))
	         .fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
function loop_map_fold_plus_one(xs: i64[]): i64 {
	var acc: i64 = 0 as i64;
	var i: i32 = 0;
	while (i < xs.len()) { acc = acc + xs[i] + (1 as i64); i = i + 1; }
	return acc;
}
function loop_map_fold(xs: i64[]): i64 {
	var acc: i64 = 0 as i64;
	var i: i32 = 0;
	while (i < xs.len()) { acc = acc + xs[i] * (3 as i64); i = i + 1; }
	return acc;
}

// filter -> map -> fold. The filter makes the fused loop's cursor and the
// sink's element count disagree, which is the whole of the 'skip' outcome.
function via_filter_map_fold(xs: i64[]): i64 {
	return xs.filter((x: i64): boolean => x % (2 as i64) == (0 as i64))
	         .map((x: i64): i64 => x + (10 as i64))
	         .fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
function loop_filter_map_fold(xs: i64[]): i64 {
	var acc: i64 = 0 as i64;
	var i: i32 = 0;
	while (i < xs.len()) {
		if (xs[i] % (2 as i64) == (0 as i64)) { acc = acc + xs[i] + (10 as i64); }
		i = i + 1;
	}
	return acc;
}

// map -> map -> reduce. 'reduce' has no seed: its accumulator is the first
// element to arrive, so 'h' runs one time fewer than there are elements.
function via_map_map_reduce(xs: i64[]): i64 {
	var out: Option[i64] = xs
		.map((x: i64): i64 => x + (1 as i64))
		.map((x: i64): i64 => x * (2 as i64))
		.reduce((a: i64, b: i64): i64 => a + b);
	match (out) { Some(v) => { return v; }, None => { return 0 as i64 - (1 as i64); } }
}
function loop_map_map_reduce(xs: i64[]): i64 {
	if (xs.len() == 0) { return 0 as i64 - (1 as i64); }
	var acc: i64 = (xs[0] + (1 as i64)) * (2 as i64);
	var i: i32 = 1;
	while (i < xs.len()) { acc = acc + (xs[i] + (1 as i64)) * (2 as i64); i = i + 1; }
	return acc;
}

// A reduce whose combining function is NOT associative or commutative. A
// fused loop that walked the elements in another order, or that folded the
// seed in twice, agrees with the loop on a sum and disagrees here.
function via_order_sensitive(xs: i64[]): i64 {
	var out: Option[i64] = xs
		.map((x: i64): i64 => x + (1 as i64))
		.reduce((a: i64, b: i64): i64 => a * (2 as i64) - b);
	match (out) { Some(v) => { return v; }, None => { return 0 as i64 - (1 as i64); } }
}
function loop_order_sensitive(xs: i64[]): i64 {
	if (xs.len() == 0) { return 0 as i64 - (1 as i64); }
	var acc: i64 = xs[0] + (1 as i64);
	var i: i32 = 1;
	while (i < xs.len()) { acc = acc * (2 as i64) - (xs[i] + (1 as i64)); i = i + 1; }
	return acc;
}

// A filter that admits nothing, feeding a reduce: the loop never runs, so the
// answer is None and not a zero accumulator dressed up as Some.
function via_all_filtered(xs: i64[]): i64 {
	var out: Option[i64] = xs
		.filter((x: i64): boolean => x < (0 as i64))
		.reduce((a: i64, b: i64): i64 => a + b);
	match (out) { Some(v) => { return v; }, None => { return 0 as i64 - (7 as i64); } }
}

// Two chains in one function: emitting the first shifts every op index the
// second was planned against.
function via_two_chains(xs: i64[]): i64 {
	var a: i64 = xs.map((x: i64): i64 => x + (1 as i64))
	               .fold(0 as i64, (p: i64, q: i64): i64 => p + q);
	var b: i64 = xs.filter((y: i64): boolean => y > (2 as i64))
	               .fold(0 as i64, (p: i64, q: i64): i64 => p + q);
	return a + b;
}
function loop_two_chains(xs: i64[]): i64 {
	var a: i64 = 0 as i64;
	var b: i64 = 0 as i64;
	var i: i32 = 0;
	while (i < xs.len()) {
		a = a + xs[i] + (1 as i64);
		if (xs[i] > (2 as i64)) { b = b + xs[i]; }
		i = i + 1;
	}
	return a + b;
}

// A chain inside a loop. The fused body is emitted into the middle of an
// enclosing structured-control-flow region, so a br target off by one level
// leaves the enclosing loop spinning or exits it early.
function via_chain_in_loop(xs: i64[], rounds: i32): i64 {
	var total: i64 = 0 as i64;
	var i: i32 = 0;
	while (i < rounds) {
		total = total + xs.map((x: i64): i64 => x + (1 as i64))
		                  .fold(0 as i64, (p: i64, q: i64): i64 => p + q);
		i = i + 1;
	}
	return total;
}
function loop_chain_in_loop(xs: i64[], rounds: i32): i64 {
	var total: i64 = 0 as i64;
	var r: i32 = 0;
	while (r < rounds) {
		var i: i32 = 0;
		while (i < xs.len()) { total = total + xs[i] + (1 as i64); i = i + 1; }
		r = r + 1;
	}
	return total;
}

// Three stages, to exercise concatenation past the two-stage case. The proof
// is quantified over any number of stages, so a pass that happened to handle
// exactly two would satisfy every other case here.
function via_three_maps(xs: i64[]): i64 {
	return xs.map((a: i64): i64 => a + (1 as i64))
	         .map((a: i64): i64 => a * (2 as i64))
	         .map((a: i64): i64 => a - (1 as i64))
	         .fold(0 as i64, (p: i64, q: i64): i64 => p + q);
}
function loop_three_maps(xs: i64[]): i64 {
	var acc: i64 = 0 as i64;
	var i: i32 = 0;
	while (i < xs.len()) { acc = acc + ((xs[i] + (1 as i64)) * (2 as i64)) - (1 as i64); i = i + 1; }
	return acc;
}

// A chain in a branch, and one whose receiver is a call result rather than a
// named local: both put the fused loop somewhere the op-range arithmetic has
// to land on the right boundary.
function via_in_branch(xs: i64[], c: i32): i64 {
	if (c > 0) {
		return xs.map((x: i64): i64 => x + (1 as i64))
		         .fold(0 as i64, (p: i64, q: i64): i64 => p + q);
	}
	return 0 as i64;
}

function doubled(xs: i64[]): i64[] {
	var out: i64[] = [];
	var i: i32 = 0;
	while (i < xs.len()) { out = out.append(xs[i] * (2 as i64)); i = i + 1; }
	return out;
}
function via_call_receiver(xs: i64[]): i64 {
	return doubled(xs).map((x: i64): i64 => x + (1 as i64))
	                  .fold(0 as i64, (p: i64, q: i64): i64 => p + q);
}
function loop_call_receiver(xs: i64[]): i64 {
	var acc: i64 = 0 as i64;
	var i: i32 = 0;
	while (i < xs.len()) { acc = acc + xs[i] * (2 as i64) + (1 as i64); i = i + 1; }
	return acc;
}

function check(n: i32): i32 {
	var xs: i64[] = build(n);
	if (via_map_fold(xs) != loop_map_fold(xs)) { return 10; }
	if (via_filter_map_fold(xs) != loop_filter_map_fold(xs)) { return 11; }
	if (via_map_map_reduce(xs) != loop_map_map_reduce(xs)) { return 12; }
	if (via_order_sensitive(xs) != loop_order_sensitive(xs)) { return 13; }
	if (via_all_filtered(xs) != 0 as i64 - (7 as i64)) { return 14; }
	if (via_two_chains(xs) != loop_two_chains(xs)) { return 15; }
	if (via_chain_in_loop(xs, 3) != loop_chain_in_loop(xs, 3)) { return 16; }
	if (via_three_maps(xs) != loop_three_maps(xs)) { return 17; }
	if (via_in_branch(xs, 1) != loop_map_fold_plus_one(xs)) { return 18; }
	if (via_in_branch(xs, 0) != 0 as i64) { return 19; }
	if (via_call_receiver(xs) != loop_call_receiver(xs)) { return 20; }
	return 0;
}

function main(): i32 {
	// Empty, singleton, and a length where the filter admits some but not
	// all. The singleton is the case a reduce gets wrong by calling its
	// combining function on one element.
	var sizes: i32[] = [0, 1, 2, 3, 17];
	var i: i32 = 0;
	while (i < sizes.len()) {
		var bad: i32 = check(sizes[i]);
		if (bad != 0) { return bad * 10 + i; }
		i = i + 1;
	}
	return 0;
}
`

// The allocation gate is a separate program because the interpreter does not
// model the heap: __heap_alloc_count() never moves there, so an assertion that
// fused chains stop allocating would pass on the interpreter for the wrong
// reason and could never fail. The correctness program above runs everywhere;
// this one runs where the counter is real.
const arrayFusionAllocSrc = `import "std/array";

function build(n: i32): i64[] {
	var xs: i64[] = [];
	var i: i32 = 0;
	while (i < n) { xs = xs.append((i as i64) + (1 as i64)); i = i + 1; }
	return xs;
}

function via_filter_map_fold(xs: i64[]): i64 {
	return xs.filter((x: i64): boolean => x % (2 as i64) == (0 as i64))
	         .map((x: i64): i64 => x + (10 as i64))
	         .fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}

function main(): i32 {
	// The counter has to move at all, or every assertion below is vacuous.
	var mark: i64 = __heap_alloc_count();
	var probe: i64[] = build(64);
	if (probe.len() != 64) { return 98; }
	if (__heap_alloc_count() - mark <= 0 as i64) { return 99; }

	// The intermediates are gone, not merely recycled: unfused, each of the
	// two stages allocates a buffer linear in the input, once per round.
	var big: i64[] = build(2000);
	var before: i64 = __heap_alloc_count();
	var sink: i64 = 0 as i64;
	var r: i32 = 0;
	while (r < 20) { sink = sink + via_filter_map_fold(big); r = r + 1; }
	var used: i64 = __heap_alloc_count() - before;
	if (sink == 0 as i64) { return 96; }
	// Fused this is zero. The bound is loose so a sink that boxes its answer
	// once per round still passes, and tight enough that one buffer per stage
	// per round (40) cannot.
	if (used > 20 as i64) { return 97; }
	return 0;
}
`

// The exit code names the case: 1x = which pipeline disagreed with its loop,
// the trailing digit = which input size.
func TestArm64ArrayFusionMatchesHandWrittenLoops(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, arrayFusionSrc); code != 0 {
		t.Errorf("array fusion on arm64: got %d, want 0", code)
	}
}

func TestX86_64ArrayFusionMatchesHandWrittenLoops(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, arrayFusionSrc); code != 0 {
		t.Errorf("array fusion on x86-64: got %d, want 0", code)
	}
}

func TestWASMArrayFusionMatchesHandWrittenLoops(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, arrayFusionSrc); got != 0 {
		t.Errorf("array fusion on wasm: got %d, want 0", got)
	}
}

func TestInterpArrayFusionMatchesHandWrittenLoops(t *testing.T) {
	if got := runInterpExit(t, arrayFusionSrc); got != 0 {
		t.Errorf("array fusion on interp: got %d, want 0", got)
	}
}

// 96 means the pipeline was optimized away entirely and the count meant
// nothing; 97 means the intermediates are still being allocated; 98/99 mean
// the counter itself stopped working.
func TestArm64ArrayFusionStopsAllocatingIntermediates(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, arrayFusionAllocSrc); code != 0 {
		t.Errorf("array fusion allocations on arm64: got %d, want 0", code)
	}
}

func TestX86_64ArrayFusionStopsAllocatingIntermediates(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, arrayFusionAllocSrc); code != 0 {
		t.Errorf("array fusion allocations on x86-64: got %d, want 0", code)
	}
}

func TestWASMArrayFusionStopsAllocatingIntermediates(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, arrayFusionAllocSrc); got != 0 {
		t.Errorf("array fusion allocations on wasm: got %d, want 0", got)
	}
}
