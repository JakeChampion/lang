package e2eselfhost

import "testing"

// A shape #11073 found the self-host refusing under E068 while native compiled
// it and ran it without allocating. The program checks its own allocation with
// __heap_bump_bytes, so a compile that succeeds but builds a fresh box exits
// with a code naming that, not 0.

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
