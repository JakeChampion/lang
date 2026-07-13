package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// E2' — `fbip` verify-and-enable, end to end (docs/NICHE-BORROWS-PLAN.md;
// IR side in internal/ir/fip_verify.go). The canonical fbip function — a
// consuming map over an `own` list, the R4 shape of docs/REUSE-CONTRACT.md —
// passes the checker's relaxed E053 walk AND the IR's E068 verification
// (every Cons construction is reuse-paired with the consumed scrutinee box),
// then executes correctly on every backend. On the unique-input path it
// allocates NOTHING: the bump cursor must not move across 100 whole-list
// maps (5000 Cons rebuilds), which is the enabled guarantee the annotation
// now makes visible. The interpreter leg pins value-correctness only (its
// __heap_bump_bytes stub reads 0, so the same program runs unchanged).

const fbipMapSrc = `enum List { Cons(i32, List), Nil }
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
    var before: i32 = __heap_bump_bytes();
    var r: List = loop_map(xs, 100);
    var grew: i32 = __heap_bump_bytes() - before;
    if (sum(r) != 6275) { return 998; }
    if (grew != 0) { return 999; }
    return 0;
}
function main(): i32 { return run(build(50)); }`

func TestInterpFbipMapValueCorrect(t *testing.T) {
	if got := runInterpExit(t, fbipMapSrc); got != 0 {
		t.Errorf("fbip map on interp: got %d, want 0 (998 = wrong value, 999 = heap grew)", got)
	}
}

func TestX86_64FbipMapZeroAlloc(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, fbipMapSrc); code != 0 {
		t.Errorf("fbip map on x86-64: got %d, want 0 (998 = wrong value, 999 = heap grew)", code)
	}
}

func TestArm64FbipMapZeroAlloc(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, fbipMapSrc); code != 0 {
		t.Errorf("fbip map on arm64: got %d, want 0 (998 = wrong value, 999 = heap grew)", code)
	}
}

func TestWASMFbipMapZeroAlloc(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, fbipMapSrc); got != 0 {
		t.Errorf("fbip map on wasm: got %d, want 0 (998 = wrong value, 999 = heap grew)", got)
	}
}
