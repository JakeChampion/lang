package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Producer-consumer fusion of std/array pipelines in the self-host compiler
// (#11072, compiler/semfuse.fern), on every self-host target.
//
// The pass runs on the typed semantic graphs all three backends lower from, so
// each target is run rather than one: what is shared is the graph, and what
// differs is how strictly each backend types the loop built from it.
//
// Three programs. The first computes every chain shape native fuses, and a
// few more, both through the combinators and through a hand-written loop, and
// compares the two inside the program, so a wrong fused loop fails even where
// it is consistently wrong. The second counts allocations, and the third
// prints from element functions, so the order of their effects is visible.

const selfHostArrayFusionSrc = `import "std/array";

function build(n: i32): i64[] {
	let xs: i64[] = [];
	let i: i32 = 0;
	while (i < n) { xs = xs.append((i as i64) + (1 as i64)); i = i + 1; }
	return xs;
}

function dbl(x: i64): i64 { return x * (2 as i64); }

function via_map_fold(xs: i64[]): i64 {
	return xs.map((x: i64): i64 => x * (3 as i64))
	         .fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
function loop_map_fold(xs: i64[]): i64 {
	let acc: i64 = 0 as i64;
	let i: i32 = 0;
	while (i < xs.len()) { acc = acc + xs[i] * (3 as i64); i = i + 1; }
	return acc;
}

function via_filter_map_fold(xs: i64[]): i64 {
	return xs.filter((x: i64): boolean => x % (2 as i64) == (0 as i64))
	         .map((x: i64): i64 => x + (10 as i64))
	         .fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
function loop_filter_map_fold(xs: i64[]): i64 {
	let acc: i64 = 0 as i64;
	let i: i32 = 0;
	while (i < xs.len()) {
		if (xs[i] % (2 as i64) == (0 as i64)) { acc = acc + xs[i] + (10 as i64); }
		i = i + 1;
	}
	return acc;
}

function via_map_map_reduce(xs: i64[]): i64 {
	let out: Option[i64] = xs
		.map((x: i64): i64 => x + (1 as i64))
		.map((x: i64): i64 => x * (2 as i64))
		.reduce((a: i64, b: i64): i64 => a + b);
	match (out) { Some(v) => { return v; }, None => { return 0 as i64 - (1 as i64); } }
}
function loop_map_map_reduce(xs: i64[]): i64 {
	if (xs.len() == 0) { return 0 as i64 - (1 as i64); }
	let acc: i64 = (xs[0] + (1 as i64)) * (2 as i64);
	let i: i32 = 1;
	while (i < xs.len()) { acc = acc + (xs[i] + (1 as i64)) * (2 as i64); i = i + 1; }
	return acc;
}

// Neither associative nor commutative: a loop that walked the elements in
// another order, or folded the first one in twice, disagrees here.
function via_order_sensitive(xs: i64[]): i64 {
	let out: Option[i64] = xs
		.map((x: i64): i64 => x + (1 as i64))
		.reduce((a: i64, b: i64): i64 => a * (2 as i64) - b);
	match (out) { Some(v) => { return v; }, None => { return 0 as i64 - (1 as i64); } }
}
function loop_order_sensitive(xs: i64[]): i64 {
	if (xs.len() == 0) { return 0 as i64 - (1 as i64); }
	let acc: i64 = xs[0] + (1 as i64);
	let i: i32 = 1;
	while (i < xs.len()) { acc = acc * (2 as i64) - (xs[i] + (1 as i64)); i = i + 1; }
	return acc;
}

// A filter ahead of a reduce: the first element to ARRIVE seeds the
// accumulator, and it is not the array's first.
function via_filter_reduce(xs: i64[]): i64 {
	let out: Option[i64] = xs
		.filter((x: i64): boolean => x % (3 as i64) != (1 as i64))
		.reduce((a: i64, b: i64): i64 => a * (2 as i64) - b);
	match (out) { Some(v) => { return v; }, None => { return 0 as i64 - (1 as i64); } }
}
function loop_filter_reduce(xs: i64[]): i64 {
	let seen: boolean = false;
	let acc: i64 = 0 as i64;
	let i: i32 = 0;
	while (i < xs.len()) {
		if (xs[i] % (3 as i64) != (1 as i64)) {
			if (seen) { acc = acc * (2 as i64) - xs[i]; } else { acc = xs[i]; seen = true; }
		}
		i = i + 1;
	}
	if (!seen) { return 0 as i64 - (1 as i64); }
	return acc;
}

function via_all_filtered(xs: i64[]): i64 {
	let out: Option[i64] = xs
		.filter((x: i64): boolean => x < (0 as i64))
		.reduce((a: i64, b: i64): i64 => a + b);
	match (out) { Some(v) => { return v; }, None => { return 0 as i64 - (7 as i64); } }
}

function via_two_chains(xs: i64[]): i64 {
	let a: i64 = xs.map((x: i64): i64 => x + (1 as i64))
	               .fold(0 as i64, (p: i64, q: i64): i64 => p + q);
	let b: i64 = xs.filter((y: i64): boolean => y > (2 as i64))
	               .fold(0 as i64, (p: i64, q: i64): i64 => p + q);
	return a + b;
}
function loop_two_chains(xs: i64[]): i64 {
	let a: i64 = 0 as i64;
	let b: i64 = 0 as i64;
	let i: i32 = 0;
	while (i < xs.len()) {
		a = a + xs[i] + (1 as i64);
		if (xs[i] > (2 as i64)) { b = b + xs[i]; }
		i = i + 1;
	}
	return a + b;
}

function via_chain_in_loop(xs: i64[], rounds: i32): i64 {
	let total: i64 = 0 as i64;
	let i: i32 = 0;
	while (i < rounds) {
		total = total + xs.map((x: i64): i64 => x + (1 as i64))
		                  .fold(0 as i64, (p: i64, q: i64): i64 => p + q);
		i = i + 1;
	}
	return total;
}
function loop_plus_one(xs: i64[]): i64 {
	let acc: i64 = 0 as i64;
	let i: i32 = 0;
	while (i < xs.len()) { acc = acc + xs[i] + (1 as i64); i = i + 1; }
	return acc;
}

function via_three_maps(xs: i64[]): i64 {
	return xs.map((a: i64): i64 => a + (1 as i64))
	         .map((a: i64): i64 => a * (2 as i64))
	         .map((a: i64): i64 => a - (1 as i64))
	         .fold(0 as i64, (p: i64, q: i64): i64 => p + q);
}
function loop_three_maps(xs: i64[]): i64 {
	let acc: i64 = 0 as i64;
	let i: i32 = 0;
	while (i < xs.len()) { acc = acc + ((xs[i] + (1 as i64)) * (2 as i64)) - (1 as i64); i = i + 1; }
	return acc;
}

function via_in_branch(xs: i64[], c: i32): i64 {
	if (c > 0) {
		return xs.map((x: i64): i64 => x * (3 as i64))
		         .fold(0 as i64, (p: i64, q: i64): i64 => p + q);
	}
	return 0 as i64;
}

function doubled(xs: i64[]): i64[] {
	let out: i64[] = [];
	let i: i32 = 0;
	while (i < xs.len()) { out = out.append(xs[i] * (2 as i64)); i = i + 1; }
	return out;
}
function via_call_receiver(xs: i64[]): i64 {
	return doubled(xs).map((x: i64): i64 => x * (3 as i64))
	                  .fold(0 as i64, (p: i64, q: i64): i64 => p + q);
}

// The free-function spelling, and a named function as the element function.
function via_free_spelling(xs: i64[]): i64 {
	return array.fold(array.map(xs, dbl), 0 as i64, (a: i64, b: i64): i64 => a + b);
}
function loop_dbl(xs: i64[]): i64 {
	let acc: i64 = 0 as i64;
	let i: i32 = 0;
	while (i < xs.len()) { acc = acc + xs[i] * (2 as i64); i = i + 1; }
	return acc;
}

// An intermediate bound to a name and read once is still one traversal.
function via_named_once(xs: i64[]): i64 {
	let ys: i64[] = xs.map(dbl);
	return ys.fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}

// Element functions that capture.
function via_capture(xs: i64[], k: i64): i64 {
	let m: i64 = k * (3 as i64);
	return xs.map((x: i64): i64 => x * m)
	         .filter((x: i64): boolean => x > k)
	         .fold(k, (a: i64, b: i64): i64 => a + b);
}
function loop_capture(xs: i64[], k: i64): i64 {
	let acc: i64 = k;
	let i: i32 = 0;
	while (i < xs.len()) {
		let y: i64 = xs[i] * k * (3 as i64);
		if (y > k) { acc = acc + y; }
		i = i + 1;
	}
	return acc;
}

// A map that changes the element type, and a fold over the narrower one.
function via_narrowing(xs: i64[]): i32 {
	return xs.map((x: i64): i32 => (x as i32) * 5)
	         .filter((y: i32): boolean => y % 2 == 1)
	         .fold(1, (a: i32, b: i32): i32 => a * 3 + b);
}
function loop_narrowing(xs: i64[]): i32 {
	let acc: i32 = 1;
	let i: i32 = 0;
	while (i < xs.len()) {
		let y: i32 = (xs[i] as i32) * 5;
		if (y % 2 == 1) { acc = acc * 3 + y; }
		i = i + 1;
	}
	return acc;
}

// Not fused: the intermediate is read twice.
function via_shared(xs: i64[]): i64 {
	let ys: i64[] = xs.map((x: i64): i64 => x + (1 as i64));
	return ys.fold(0 as i64, (a: i64, b: i64): i64 => a + b)
	     + ys.fold(0 as i64, (a: i64, b: i64): i64 => a * (2 as i64) - b);
}
function loop_shared(xs: i64[]): i64 {
	let s: i64 = 0 as i64;
	let t: i64 = 0 as i64;
	let i: i32 = 0;
	while (i < xs.len()) { s = s + xs[i] + (1 as i64); t = t * (2 as i64) - (xs[i] + (1 as i64)); i = i + 1; }
	return s + t;
}

// Not fused: the element function is a value from a parameter.
function via_param_fn(xs: i64[], f: (i64) => i64): i64 {
	return xs.map(f).fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}

function check(n: i32): i32 {
	let xs: i64[] = build(n);
	if (via_map_fold(xs) != loop_map_fold(xs)) { return 10; }
	if (via_filter_map_fold(xs) != loop_filter_map_fold(xs)) { return 11; }
	if (via_map_map_reduce(xs) != loop_map_map_reduce(xs)) { return 12; }
	if (via_order_sensitive(xs) != loop_order_sensitive(xs)) { return 13; }
	if (via_filter_reduce(xs) != loop_filter_reduce(xs)) { return 14; }
	if (via_all_filtered(xs) != 0 as i64 - (7 as i64)) { return 15; }
	if (via_two_chains(xs) != loop_two_chains(xs)) { return 16; }
	if (via_chain_in_loop(xs, 3) != (3 as i64) * loop_plus_one(xs)) { return 17; }
	if (via_three_maps(xs) != loop_three_maps(xs)) { return 18; }
	if (via_in_branch(xs, 1) != loop_map_fold(xs) || via_in_branch(xs, 0) != 0 as i64) { return 19; }
	if (via_call_receiver(xs) != (2 as i64) * loop_map_fold(xs)) { return 20; }
	if (via_free_spelling(xs) != loop_dbl(xs)) { return 21; }
	if (via_named_once(xs) != loop_dbl(xs)) { return 22; }
	if (via_capture(xs, 4 as i64) != loop_capture(xs, 4 as i64)) { return 23; }
	if (via_narrowing(xs) != loop_narrowing(xs)) { return 24; }
	if (via_shared(xs) != loop_shared(xs)) { return 25; }
	if (via_param_fn(xs, (x: i64): i64 => x * (3 as i64)) != loop_map_fold(xs)) { return 26; }
	return 0;
}

function main(): i32 {
	// Empty, singleton, and lengths where each filter admits some but not all.
	let sizes: i32[] = [0, 1, 2, 3, 17];
	let i: i32 = 0;
	while (i < sizes.len()) {
		let bad: i32 = check(sizes[i]);
		if (bad != 0) { return bad * 5 + i - 40; }
		i = i + 1;
	}
	return 0;
}
`

// Each chain runs `rounds` times over 2000 elements. Unfused, every
// intermediate costs a buffer per round and a regrow per doubling, so a chain
// that allocates fewer blocks than it has rounds per allowance has built none.
const selfHostArrayFusionAllocSrc = `import "std/array";

function build(n: i32): i64[] {
	let xs: i64[] = [];
	let i: i32 = 0;
	while (i < n) { xs = xs.append((i as i64) + (1 as i64)); i = i + 1; }
	return xs;
}

function dbl(x: i64): i64 { return x * (2 as i64); }

function filter_map_fold(xs: i64[]): i64 {
	return xs.filter((x: i64): boolean => x % (2 as i64) == (0 as i64))
	         .map((x: i64): i64 => x + (10 as i64))
	         .fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}

function map_map_reduce(xs: i64[]): i64 {
	let out: Option[i64] = xs.map((x: i64): i64 => x + (1 as i64))
	                         .map((x: i64): i64 => x * (2 as i64))
	                         .reduce((a: i64, b: i64): i64 => a + b);
	match (out) { Some(v) => { return v; }, None => { return 0 as i64; } }
}

function free_spelling(xs: i64[]): i64 {
	return array.fold(array.map(xs, dbl), 0 as i64, (a: i64, b: i64): i64 => a + b);
}

function named_once(xs: i64[]): i64 {
	let ys: i64[] = xs.filter((x: i64): boolean => x > (5 as i64));
	return ys.fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}

function capture(xs: i64[], k: i64): i64 {
	return xs.map((x: i64): i64 => x * k).fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}

function shared(xs: i64[]): i64 {
	let ys: i64[] = xs.map((x: i64): i64 => x + (1 as i64));
	return ys.fold(0 as i64, (a: i64, b: i64): i64 => a + b) + ys.fold(0 as i64, (a: i64, b: i64): i64 => a + b * (2 as i64));
}

function param_fn(xs: i64[], f: (i64) => i64): i64 {
	return xs.map(f).fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}

function rounds(): i32 { return 20; }

// The blocks one call of the chain numbered 'which' allocates, over every round.
function measure(xs: i64[], which: i32): i64 {
	let before: i64 = __heap_alloc_count();
	let sink: i64 = 0 as i64;
	let r: i32 = 0;
	while (r < rounds()) {
		if (which == 0) { sink = sink + filter_map_fold(xs); }
		if (which == 1) { sink = sink + map_map_reduce(xs); }
		if (which == 2) { sink = sink + free_spelling(xs); }
		if (which == 3) { sink = sink + named_once(xs); }
		if (which == 4) { sink = sink + capture(xs, (r as i64) + (2 as i64)); }
		if (which == 5) { sink = sink + shared(xs); }
		if (which == 6) { sink = sink + param_fn(xs, (x: i64): i64 => x + (3 as i64)); }
		r = r + 1;
	}
	let used: i64 = __heap_alloc_count() - before;
	if (sink == 0 as i64) { return 0 as i64 - (1 as i64); }
	return used;
}

function main(): i32 {
	// The counter has to move at all, or every bound below is vacuous.
	let mark: i64 = __heap_alloc_count();
	let probe: i64[] = build(64);
	if (probe.len() != 64) { return 98; }
	if (__heap_alloc_count() - mark <= 0 as i64) { return 99; }

	let big: i64[] = build(2000);
	let n: i64 = rounds() as i64;
	// Fused, a chain allocates no intermediate. The allowance is one block
	// per round, for a reduce's Some, and two for the capturing chain, whose
	// closure builds its environment and the captured parameter's cell on
	// each call. Unfused, each intermediate is a buffer per round plus a
	// regrow per doubling, which is several times either.
	let w: i32 = 0;
	while (w < 5) {
		let used: i64 = measure(big, w);
		let allowed: i64 = n;
		if (w == 4) { allowed = n * (2 as i64); }
		if (used < 0 as i64) { return 70 + w; }
		if (used > allowed) { return 80 + w; }
		w = w + 1;
	}
	// Not fused: an intermediate read twice, and an element function the
	// chain was handed. Each builds its intermediate every round.
	if (measure(big, 5) < n) { return 90; }
	if (measure(big, 6) < n) { return 91; }
	return 0;
}
`

// Element functions that print. Fused, the stages' calls interleave per
// element, so a chain whose element functions reach an effect must stay one
// traversal per stage. That holds whether the effect is the stage's own or a
// callee's, and whether the second party is another stage or the sink.
const selfHostArrayFusionEffectSrc = `import "std/array";

function noisy(x: i64): i64 {
	print("n");
	return x;
}

function main(): i32 {
	let xs: i64[] = [1 as i64, 2 as i64, 3 as i64];
	let a: i64 = xs.map((x: i64): i64 => { print("m"); return x; })
	               .fold(0 as i64, (p: i64, q: i64): i64 => { print("f"); return p + q; });
	let b: i64 = xs.filter((x: i64): boolean => { print("p"); return true; })
	               .map((x: i64): i64 => { print("m"); return x; })
	               .fold(0 as i64, (p: i64, q: i64): i64 => p + q);
	let o: Option[i64] = xs.map((x: i64): i64 => noisy(x))
	                       .reduce((p: i64, q: i64): i64 => { print("r"); return p + q; });
	let c: i64 = 0 as i64;
	match (o) { Some(v) => { c = v; }, None => { c = 0 as i64; } }
	if (a != 6 as i64 || b != 6 as i64 || c != 6 as i64) { return 1; }
	return 0;
}
`

const selfHostArrayFusionEffectWant = "mmmfffpppmmmnnnrr"

// runSelfHostFusionProgram compiles src with the self-host compiler for target
// and runs it, returning its stdout and exit code.
func runSelfHostFusionProgram(t *testing.T, target, src string) (string, int) {
	t.Helper()
	switch target {
	case e2eharness.TargetX86_64Linux:
		return e2eharness.CompileAndRunX86_64(t, src)
	case e2eharness.TargetArm64Linux:
		return e2eharness.CompileAndRunArm64(t, src)
	}
	srcPath := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	core := e2eharness.CompileSelfHostFile(t, e2eharness.TargetWasm32Wasi, srcPath, nil)
	cmd := e2eharness.RunWasmCore(t, core)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil {
		t.Fatalf("wasmtime did not run %s", core)
	}
	return string(out), cmd.ProcessState.ExitCode()
}

var selfHostFusionTargets = []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi}

// Exit 10..26 names the case that disagreed with its loop (see check), and the
// size it disagreed at: (case * 5 + size index) - 40.
func TestSelfHostArrayFusionMatchesHandWrittenLoops(t *testing.T) {
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			if _, code := runSelfHostFusionProgram(t, target, selfHostArrayFusionSrc); code != 0 {
				t.Errorf("got exit %d, want 0: case %d at size index %d", code, (code+40)/5, (code+40)%5)
			}
		})
	}
}

// 70+w: chain w computed nothing; 80+w: chain w still allocates its
// intermediates; 90 / 91: a chain that must not fuse stopped building its
// intermediate; 98 / 99: the counter itself stopped working.
func TestSelfHostArrayFusionStopsAllocatingIntermediates(t *testing.T) {
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			if _, code := runSelfHostFusionProgram(t, target, selfHostArrayFusionAllocSrc); code != 0 {
				t.Errorf("got exit %d, want 0", code)
			}
		})
	}
}

func TestSelfHostArrayFusionKeepsEffectOrder(t *testing.T) {
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			out, code := runSelfHostFusionProgram(t, target, selfHostArrayFusionEffectSrc)
			if code != 0 {
				t.Errorf("got exit %d, want 0", code)
			}
			if got := strings.Join(strings.Fields(out), ""); got != selfHostArrayFusionEffectWant {
				t.Errorf("effects ran in the order %q, want %q", got, selfHostArrayFusionEffectWant)
			}
		})
	}
}
