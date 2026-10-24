package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const arrayFusionFlatMapSrc = `import "std/array";

function build(n: i32): i64[] {
	let xs: i64[] = [];
	let i: i32 = 0;
	while (i < n) { xs = xs.append((i as i64) + (1 as i64)); i = i + 1; }
	return xs;
}

function same(a: i64[], b: i64[]): boolean {
	if (a.len() != b.len()) { return false; }
	let i: i32 = 0;
	while (i < a.len()) { if (a[i] != b[i]) { return false; } i = i + 1; }
	return true;
}

function via_fold(xs: i64[]): i64 {
	return xs.flat_map((x: i64): i64[] => [x, x * (3 as i64)])
	         .fold(0 as i64, (a: i64, b: i64): i64 => a * (2 as i64) + b);
}
function loop_fold(xs: i64[]): i64 {
	let acc: i64 = 0 as i64;
	let i: i32 = 0;
	while (i < xs.len()) {
		acc = acc * (2 as i64) + xs[i];
		acc = acc * (2 as i64) + xs[i] * (3 as i64);
		i = i + 1;
	}
	return acc;
}

// Filters on both sides, and inner arrays that are sometimes empty.
function via_filters_reduce(xs: i64[]): i64 {
	let out: Option[i64] = xs
		.filter((x: i64): boolean => x % (4 as i64) != (0 as i64))
		.flat_map((x: i64): i64[] => {
			if (x % (3 as i64) == (0 as i64)) { return []; }
			return [x, x + (1 as i64), x + (2 as i64)];
		})
		.filter((y: i64): boolean => y % (5 as i64) != (0 as i64))
		.reduce((a: i64, b: i64): i64 => a * (2 as i64) - b);
	match (out) { Some(v) => { return v; }, None => { return 0 as i64 - (1 as i64); } }
}
function loop_filters_reduce(xs: i64[]): i64 {
	let seen: boolean = false;
	let acc: i64 = 0 as i64;
	let i: i32 = 0;
	while (i < xs.len()) {
		let x: i64 = xs[i];
		if (x % (4 as i64) != (0 as i64) && x % (3 as i64) != (0 as i64)) {
			let d: i64 = 0 as i64;
			while (d < (3 as i64)) {
				let y: i64 = x + d;
				if (y % (5 as i64) != (0 as i64)) {
					if (seen) { acc = acc * (2 as i64) - y; } else { acc = y; seen = true; }
				}
				d = d + (1 as i64);
			}
		}
		i = i + 1;
	}
	if (!seen) { return 0 as i64 - (1 as i64); }
	return acc;
}

function via_scan(xs: i64[]): i64[] {
	return xs.flat_map((x: i64): i64[] => [x, (0 as i64) - x])
	         .scan(1 as i64, (a: i64, b: i64): i64 => a * (3 as i64) + b);
}
function loop_scan(xs: i64[]): i64[] {
	let out: i64[] = [];
	let acc: i64 = 1 as i64;
	let i: i32 = 0;
	while (i < xs.len()) {
		acc = acc * (3 as i64) + xs[i];
		out = out.append(acc);
		acc = acc * (3 as i64) - xs[i];
		out = out.append(acc);
		i = i + 1;
	}
	return out;
}

// The element function only reads its capture, so it cannot abort and a
// take may stop the pipeline from inside the inner loop.
function via_take_inside(xs: i64[], ws: i64[], k: i32): i64 {
	return xs.flat_map((x: i64): i64[] => ws)
	         .map((w: i64): i64 => w + (1 as i64))
	         .take(k)
	         .fold(0 as i64, (a: i64, b: i64): i64 => a * (2 as i64) + b);
}
function loop_take_inside(xs: i64[], ws: i64[], k: i32): i64 {
	let acc: i64 = 0 as i64;
	let taken: i32 = 0;
	let i: i32 = 0;
	while (i < xs.len()) {
		let j: i32 = 0;
		while (j < ws.len()) {
			if (taken >= k) { return acc; }
			acc = acc * (2 as i64) + ws[j] + (1 as i64);
			taken = taken + 1;
			j = j + 1;
		}
		i = i + 1;
	}
	return acc;
}

function via_take_outside(xs: i64[], k: i32): i64 {
	return xs.take(k)
	         .flat_map((x: i64): i64[] => [x, x])
	         .fold(0 as i64, (a: i64, b: i64): i64 => a * (2 as i64) + b);
}
function loop_take_outside(xs: i64[], k: i32): i64 {
	let acc: i64 = 0 as i64;
	let i: i32 = 0;
	while (i < xs.len() && i < k) { acc = (acc * (2 as i64) + xs[i]) * (2 as i64) + xs[i]; i = i + 1; }
	return acc;
}

function via_two_levels(xs: i64[]): i64 {
	return xs.flat_map((x: i64): i64[] => [x, x + (1 as i64)])
	         .flat_map((y: i64): i64[] => [y * (2 as i64), y * (5 as i64)])
	         .fold(0 as i64, (a: i64, b: i64): i64 => a * (3 as i64) - b);
}
function loop_two_levels(xs: i64[]): i64 {
	let acc: i64 = 0 as i64;
	let i: i32 = 0;
	while (i < xs.len()) {
		let d: i64 = 0 as i64;
		while (d < (2 as i64)) {
			let y: i64 = xs[i] + d;
			acc = acc * (3 as i64) - y * (2 as i64);
			acc = acc * (3 as i64) - y * (5 as i64);
			d = d + (1 as i64);
		}
		i = i + 1;
	}
	return acc;
}

function check(n: i32): i32 {
	let xs: i64[] = build(n);
	let ws: i64[] = [7 as i64, 8 as i64, 9 as i64];
	if (via_fold(xs) != loop_fold(xs)) { return 10; }
	if (via_filters_reduce(xs) != loop_filters_reduce(xs)) { return 11; }
	if (!same(via_scan(xs), loop_scan(xs))) { return 12; }
	if (via_two_levels(xs) != loop_two_levels(xs)) { return 13; }
	let counts: i32[] = [0 - 1, 0, 1, 2, 4, 100];
	for k in counts {
		if (via_take_inside(xs, ws, k) != loop_take_inside(xs, ws, k)) { return 14; }
		if (via_take_outside(xs, k) != loop_take_outside(xs, k)) { return 15; }
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

func TestSelfHostArrayFusionFlatMap(t *testing.T) {
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			for _, disabled := range []string{"0", "1"} {
				t.Run("disabled="+disabled, func(t *testing.T) {
					bin := e2eharness.CompileSelfHostSource(t, target, arrayFusionFlatMapSrc,
						[]string{"FERN_NO_ARRAY_FUSION=" + disabled, "FERN_LEAKCHECK=1"})
					cmd := runScaleTarget(t, target, bin)
					out, err := cmd.CombinedOutput()
					if err != nil {
						code := cmd.ProcessState.ExitCode()
						t.Fatalf("got exit %d: case %d at size index %d\n%s", code, (code+40)/5, (code+40)%5, out)
					}
					var allocs, frees, live int64
					if _, err := fmtSscan(leakSummaryLine(string(out)), &allocs, &frees, &live); err != nil || allocs == 0 || allocs != frees || live != 0 {
						t.Fatalf("flat_map ownership: %v\n%s", err, out)
					}
				})
			}
		})
	}
}
