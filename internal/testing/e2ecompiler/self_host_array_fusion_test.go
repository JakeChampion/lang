package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

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

// take and take_while: the loop stops once the count is reached or the
// predicate fails. Counts below zero, zero,
// inside the array and past its end, ahead of and behind a filter, into each
// sink, and two takes in one chain.
function via_map_take_fold(xs: i64[], k: i32): i64 {
	return xs.map((x: i64): i64 => x * (3 as i64))
	         .take(k)
	         .fold(0 as i64, (a: i64, b: i64): i64 => a * (2 as i64) + b);
}
function loop_map_take_fold(xs: i64[], k: i32): i64 {
	let acc: i64 = 0 as i64;
	let i: i32 = 0;
	while (i < xs.len() && i < k) { acc = acc * (2 as i64) + xs[i] * (3 as i64); i = i + 1; }
	return acc;
}

function via_filter_take_reduce(xs: i64[], k: i32): i64 {
	let out: Option[i64] = xs
		.filter((x: i64): boolean => x % (2 as i64) == (0 as i64))
		.take(k)
		.reduce((a: i64, b: i64): i64 => a * (2 as i64) - b);
	match (out) { Some(v) => { return v; }, None => { return 0 as i64 - (1 as i64); } }
}
function loop_filter_take_reduce(xs: i64[], k: i32): i64 {
	let seen: boolean = false;
	let acc: i64 = 0 as i64;
	let taken: i32 = 0;
	let i: i32 = 0;
	while (i < xs.len() && taken < k) {
		if (xs[i] % (2 as i64) == (0 as i64)) {
			if (seen) { acc = acc * (2 as i64) - xs[i]; } else { acc = xs[i]; seen = true; }
			taken = taken + 1;
		}
		i = i + 1;
	}
	if (!seen) { return 0 as i64 - (1 as i64); }
	return acc;
}

function via_take_filter_scan(xs: i64[], k: i32): i64[] {
	return xs.take(k)
	         .filter((x: i64): boolean => x > (2 as i64))
	         .scan(1 as i64, (a: i64, b: i64): i64 => a * (3 as i64) - b);
}
function loop_take_filter_scan(xs: i64[], k: i32): i64[] {
	let out: i64[] = [];
	let acc: i64 = 1 as i64;
	let i: i32 = 0;
	while (i < xs.len() && i < k) {
		if (xs[i] > (2 as i64)) { acc = acc * (3 as i64) - xs[i]; out = out.append(acc); }
		i = i + 1;
	}
	return out;
}

function via_two_takes(xs: i64[], k: i32): i64 {
	return xs.take(5)
	         .map((x: i64): i64 => x + (1 as i64))
	         .take(k)
	         .fold(0 as i64, (a: i64, b: i64): i64 => a * (2 as i64) + b);
}
function loop_two_takes(xs: i64[], k: i32): i64 {
	let acc: i64 = 0 as i64;
	let i: i32 = 0;
	while (i < xs.len() && i < 5 && i < k) { acc = acc * (2 as i64) + xs[i] + (1 as i64); i = i + 1; }
	return acc;
}

function via_map_take_while_fold(xs: i64[], k: i32): i64 {
	let limit: i64 = (k as i64) * (3 as i64);
	return array.take_while(xs.map((x: i64): i64 => x * (3 as i64)), (x: i64): boolean => x <= limit)
	         .filter((x: i64): boolean => x % (2 as i64) == (0 as i64))
	         .fold(0 as i64, (a: i64, b: i64): i64 => a * (2 as i64) + b);
}
function loop_map_take_while_fold(xs: i64[], k: i32): i64 {
	let limit: i64 = (k as i64) * (3 as i64);
	let acc: i64 = 0 as i64;
	let i: i32 = 0;
	while (i < xs.len() && xs[i] * (3 as i64) <= limit) {
		let y: i64 = xs[i] * (3 as i64);
		if (y % (2 as i64) == (0 as i64)) { acc = acc * (2 as i64) + y; }
		i = i + 1;
	}
	return acc;
}

function via_take_while_reduce(xs: i64[], k: i32): i64 {
	let out: Option[i64] = array.take_while(xs, (x: i64): boolean => x < (k as i64))
		.reduce((a: i64, b: i64): i64 => a * (2 as i64) - b);
	match (out) { Some(v) => { return v; }, None => { return 0 as i64 - (1 as i64); } }
}
function loop_take_while_reduce(xs: i64[], k: i32): i64 {
	if (xs.len() == 0 || xs[0] >= (k as i64)) { return 0 as i64 - (1 as i64); }
	let acc: i64 = xs[0];
	let i: i32 = 1;
	while (i < xs.len() && xs[i] < (k as i64)) { acc = acc * (2 as i64) - xs[i]; i = i + 1; }
	return acc;
}

function same(a: i64[], b: i64[]): boolean {
	if (a.len() != b.len()) { return false; }
	let i: i32 = 0;
	while (i < a.len()) { if (a[i] != b[i]) { return false; } i = i + 1; }
	return true;
}

function check_take(xs: i64[]): i32 {
	let counts: i32[] = [0 - 1, 0, 1, 2, 9, 100];
	for k in counts {
		if (via_map_take_fold(xs, k) != loop_map_take_fold(xs, k)) { return 27; }
		if (via_filter_take_reduce(xs, k) != loop_filter_take_reduce(xs, k)) { return 28; }
		if (!same(via_take_filter_scan(xs, k), loop_take_filter_scan(xs, k))) { return 29; }
		if (via_two_takes(xs, k) != loop_two_takes(xs, k)) { return 30; }
		if (via_map_take_while_fold(xs, k) != loop_map_take_while_fold(xs, k)) { return 31; }
		if (via_take_while_reduce(xs, k) != loop_take_while_reduce(xs, k)) { return 32; }
	}
	return 0;
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
	return check_take(xs);
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

function taken(xs: i64[]): i64 {
	return xs.map((x: i64): i64 => x + (1 as i64)).take(1500).fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}

function taken_while(xs: i64[]): i64 {
	return array.take_while(xs, (x: i64): boolean => x < (1500 as i64)).fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}

function flattened(xs: i64[]): i64 {
	return xs.flat_map((x: i64): i64[] => [x, x]).fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}

// What the fused flat_map should cost: the element function's array per
// element, and nothing else.
function flattened_by_hand(xs: i64[]): i64 {
	let acc: i64 = 0 as i64;
	let i: i32 = 0;
	while (i < xs.len()) {
		let ys: i64[] = [xs[i], xs[i]];
		let j: i32 = 0;
		while (j < ys.len()) { acc = acc + ys[j]; j = j + 1; }
		i = i + 1;
	}
	return acc;
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
		if (which == 7) { sink = sink + taken(xs); }
		if (which == 8) { sink = sink + taken_while(xs); }
		if (which == 9) { sink = sink + flattened(xs); }
		if (which == 10) { sink = sink + flattened_by_hand(xs); }
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
	while (w < 9) {
		let used: i64 = measure(big, w);
		let allowed: i64 = n;
		if (w == 4) { allowed = n * (2 as i64); }
		if (used < 0 as i64) { return 70 + w; }
		if (used > allowed) { return 80 + w; }
		w = w + 1;
		if (w == 5) { w = 7; }
	}
	// A fused flat_map allocates what its element function returns, as the
	// hand-written loop does, and no concatenated array.
	if (measure(big, 9) > measure(big, 10) + n) { return 92; }
	// Not fused: an intermediate read twice, and an element function the
	// chain was handed. Each builds its intermediate every round.
	if (measure(big, 5) < n) { return 90; }
	if (measure(big, 6) < n) { return 91; }
	return 0;
}
`

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
	let d: i64 = xs.map((x: i64): i64 => { print("t"); return x; })
	               .take(1)
	               .fold(0 as i64, (p: i64, q: i64): i64 => p + q);
	let e: i64 = array.take_while(xs, (x: i64): boolean => { print("w"); return x < (2 as i64); })
	               .fold(0 as i64, (p: i64, q: i64): i64 => p + q);
	if (a != 6 as i64 || b != 6 as i64 || c != 6 as i64 || d != 1 as i64 || e != 1 as i64) { return 1; }
	return 0;
}
`

const selfHostArrayFusionEffectWant = "mmmfffpppmmmnnnrrtttww"

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

func TestSelfHostArrayFusionMatchesHandWrittenLoops(t *testing.T) {
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			if _, code := runSelfHostFusionProgram(t, target, selfHostArrayFusionSrc); code != 0 {
				t.Errorf("got exit %d, want 0: case %d at size index %d", code, (code+40)/5, (code+40)%5)
			}
		})
	}
}

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
