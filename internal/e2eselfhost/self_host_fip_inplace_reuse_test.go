package e2eselfhost

import "testing"

// The two shapes #11073 found the self-host refusing under E068 while native
// compiled them and ran them without allocating. Each program checks its own
// allocation with __heap_bump_bytes, so a compile that succeeds but builds a
// fresh box exits with a code naming that, not 0.

// R7 of docs/REUSE-CONTRACT.md: `xs.map(f)` over an `own` i64 array whose
// element type does not change writes through the donor's buffer. 200 maps of
// a 64-element array must not move the bump cursor (90-92). The second half
// is the soundness pin: `b` still holds the buffer `a` is handed in, so the
// donor reaches the uniqueness test shared and must be copied, leaving `b`
// unchanged (93-95).
const selfHostFipOwnedMapSrc = `import "std/array";

fip function dbl(x: i64): i64 { return x * (2 as i64); }
fip function twice(own xs: i64[]): i64[] { return xs.map((x: i64): i64 => dbl(x)); }

function build(n: i32): i64[] {
	var xs: i64[] = [];
	var i: i32 = 0;
	while (i < n) { xs = xs.append((i as i64) + (1 as i64)); i = i + 1; }
	return xs;
}

function churn(own xs: i64[], rounds: i32): i64[] {
	if (rounds == 0) { return xs; }
	return churn(twice(xs), rounds - 1);
}

function main(): i32 {
	var xs: i64[] = build(64);
	var before: i64 = __heap_bump_bytes();
	xs = churn(xs, 200);
	var grew: i64 = __heap_bump_bytes() - before;
	if (xs.len() != 64) { return 90; }
	if (xs[0] == 1 as i64) { return 91; }
	if (grew != 0 as i64) { return 92; }

	var a: i64[] = build(4);
	var b: i64[] = a;
	a = twice(a);
	if (b[0] != 1 as i64 || b[3] != 4 as i64) { return 93; }
	if (a[0] != 2 as i64 || a[3] != 8 as i64) { return 94; }
	if (b.len() != 4 || a.len() != 4) { return 95; }
	return 0;
}
`

// A receiver read after the map does not die at the call, so R7 declines and
// the map builds a fresh array: the receiver's elements must read back as
// they were (1), and the result must be the mapped one (2).
const selfHostOwnedMapReceiverLiveSrc = `import "std/array";

function inc(x: i64): i64 { return x + (1 as i64); }
function after(own xs: i64[]): i64 {
	var ys: i64[] = xs.map((x: i64): i64 => inc(x));
	return ys[0] * (100 as i64) + xs[0];
}

function main(): i32 {
	var xs: i64[] = [5 as i64, 6 as i64];
	var r: i64 = after(xs);
	if (r != 605 as i64) { return 1; }
	return 0;
}
`

// The consuming match (R4): each Cons is rebuilt in the box of the Cons it
// matched. 100 maps over a 50-cell list must not move the bump cursor (999)
// and must sum to 6275 (998). The self-host turns the recursion into a loop
// (tail recursion modulo cons), so the matched cell dies at its payload read
// and the rebuilt one is constructed later in the same block. The shared half:
// `b` holds the list `a` is handed in, so every cell reaches the uniqueness
// test shared and is copied, leaving `b` summing to its original 15 (997).
const selfHostFbipMapSrc = `enum List { Cons(i32, List), Nil }
fbip function map_inc(own xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, map_inc(t)); },
        Nil => { return Nil; },
    }
}
function loop_map(own xs: List, i: i32): List {
    if (i == 0) { return xs; }
    return loop_map(map_inc(xs), i - 1);
}
function sum(l: List): i32 {
    match (l) { Cons(h, t) => { return h + sum(t); }, Nil => { return 0; } }
}
function build(n: i32): List {
    if (n == 0) { return Nil; }
    return Cons(n, build(n - 1));
}
function run(own xs: List): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var r: List = loop_map(xs, 100);
    var grew: i32 = (__heap_bump_bytes() as i32) - before;
    if (sum(r) != 6275) { return 998; }
    if (grew != 0) { return 999; }
    return 0;
}
function main(): i32 {
    var code: i32 = run(build(50));
    if (code != 0) { return code; }
    var a: List = build(5);
    var b: List = a;
    a = map_inc(a);
    if (sum(b) != 15 || sum(a) != 20) { return 997; }
    return 0;
}`

func TestSelfHostFipInPlaceReuse(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range []struct{ name, src, codes string }{
		{"owned-map-zero-alloc-shared-donor-copied", selfHostFipOwnedMapSrc, "90-92 the unique path, 93-95 the shared donor"},
		{"owned-map-live-receiver-not-written", selfHostOwnedMapReceiverLiveSrc, "1 the receiver was written, 2 the result is wrong"},
		{"fbip-cons-rebuilt-in-place-shared-copied", selfHostFbipMapSrc, "998 wrong sum, 999 heap grew, 997 the shared list changed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if want := interpExit(t, interpBin, tc.src); want != 0 {
				t.Fatalf("interpreter = %d, want 0: the case does not hold on the oracle", want)
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					if stderr, code := cli.exitOf(t, tc.src, target, "FERN_STRICT_IR=1"); code != 0 {
						t.Errorf("exited %d, want 0 (%s)\n%s", code, tc.codes, stderr)
					}
				})
			}
		})
	}
}
