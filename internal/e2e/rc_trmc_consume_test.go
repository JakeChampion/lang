package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Slice 2 TRMC-consuming peak-memory win. A consume-safe TRMC list-map frees
// each input cell as the hole-passing loop advances (recycling it into the next
// output node), so a single big `inc_all(build(N))` holds ~N cells live at peak
// instead of ~2N (the whole input PLUS the whole output). The bump high-water
// probe (__heap_bump_bytes) captures that: under owned-by-default (consume) the
// peak is ~HALF the borrow model on the identical source. Measured identically
// on all three backends (consume 62 vs borrow 125 cells for N=2000).
//
// Soundness (byte-identical output, no over-release across the whole fixture
// corpus) is the differential gate's job (rc_owned_by_default_test); this is the
// peak-memory dividend, plus a focused over-release guard on the consume path.

func trmcConsumePeakSrc(n, div string) string {
	return `enum List { Cons(i32, List), Nil }
function inc_all(xs: List): List {
    match (xs) { Cons(h, t) => { return Cons(h + 1, inc_all(t)); }, Nil => { return Nil; } }
}
function build(n: i32): List { var acc: List = Nil; var i: i32 = 0; while (i < n) { acc = Cons(i, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { var acc: i32 = 0; var cur: List = l; var go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var ys: List = inc_all(build(` + n + `));
    var peak: i32 = (__heap_bump_bytes() as i32) - before;
    if (sum(ys) < 0) { return 255; }
    return peak / ` + div + `;
}`
}

// Consuming TRMC path must stay rc-balanced: build a list, map it (consuming the
// input), read the result; the input cells are reclaimed in the loop and the
// output is reclaimed at exit, with zero over-release.
const trmcConsumeSoundSrc = `enum List { Cons(i32, List), Nil }
function inc_all(xs: List): List {
    match (xs) { Cons(h, t) => { return Cons(h + 1, inc_all(t)); }, Nil => { return Nil; } }
}
function build(n: i32): List { var acc: List = Nil; var i: i32 = 0; while (i < n) { acc = Cons(i, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { match (l) { Cons(h, t) => { return h + sum(t); }, Nil => { return 0; } } }
function main(): i32 {
    var ys: List = inc_all(build(100));      // sum(0..99)=4950, +100 = 5050
    if (sum(ys) != 5050) { return 100; }
    return __rc_underflow_count();
}`

// assertConsumeHalves checks the consume peak is meaningfully below the borrow
// peak (the in-place reuse win), not a marginal difference.
func assertConsumeHalves(t *testing.T, backend string, on, off int) {
	t.Helper()
	if on <= 0 {
		t.Errorf("%s: consume peak should be non-zero, got %d", backend, on)
	}
	if on >= off {
		t.Errorf("%s: consume peak (%d) should be below borrow peak (%d)", backend, on, off)
	}
	if on*100 > off*60 {
		t.Errorf("%s: consume peak (%d) should be ~half the borrow peak (%d); want on <= 0.6*off", backend, on, off)
	}
}

func TestX86_64TrmcConsumePeakHalved(t *testing.T) {
	src := trmcConsumePeakSrc("2000", "1024")
	prev := ast.OwnedByDefault
	ast.OwnedByDefault = true
	on := mustRunX86_64FreeOn(t, src)
	ast.OwnedByDefault = false
	off := mustRunX86_64FreeOn(t, src)
	ast.OwnedByDefault = prev
	assertConsumeHalves(t, "x86_64", on, off)
	if _, code := compileAndRunX86_64FreeOn(t, trmcConsumeSoundSrc); code != 0 {
		t.Errorf("x86_64 consume soundness: got %d, want 0 (100=value, >0=over-release)", code)
	}
}

func TestArm64TrmcConsumePeakHalved(t *testing.T) {
	src := trmcConsumePeakSrc("2000", "1024")
	prev := ast.OwnedByDefault
	ast.OwnedByDefault = true
	on := mustRunArm64FreeOn(t, src)
	ast.OwnedByDefault = false
	off := mustRunArm64FreeOn(t, src)
	ast.OwnedByDefault = prev
	assertConsumeHalves(t, "arm64-linux", on, off)
	if _, code := compileAndRunArm64FreeOn(t, trmcConsumeSoundSrc); code != 0 {
		t.Errorf("arm64 consume soundness: got %d, want 0", code)
	}
}

func TestWASMTrmcConsumePeakHalved(t *testing.T) {
	src := trmcConsumePeakSrc("2000", "1024")
	prc := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	prev := ast.OwnedByDefault
	ast.OwnedByDefault = true
	on := runWasm(t, src)
	ast.OwnedByDefault = false
	off := runWasm(t, src)
	ast.OwnedByDefault = prev
	ast.RcFreeEnabled = prc
	assertConsumeHalves(t, "wasm32-wasi", on, off)

	ast.RcFreeEnabled = true
	if got := runWasm(t, trmcConsumeSoundSrc); got != 0 {
		t.Errorf("wasm consume soundness: got %d, want 0", got)
	}
	ast.RcFreeEnabled = prc
}
