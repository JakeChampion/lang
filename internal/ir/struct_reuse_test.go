package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
)

// allocReuseCount counts the __alloc_reuse calls a function lowers —
// the marker that the Phase 5b constructor-reuse (FBIP) path fired.
func allocReuseCount(fn *ir.Func) int {
	n := 0
	for _, op := range fn.Ops {
		if op.Kind == ir.OpCallDirect && op.Str == "__alloc_reuse" {
			n++
		}
	}
	return n
}

// A self-overwrite of an owned, all-scalar struct local with a literal
// of the same type lowers through __alloc_reuse (reuse the old box in
// place when uniquely owned).
func TestStructReuseFiresForSelfOverwrite(t *testing.T) {
	ip := lowerForTest(t, `struct Point { x: i32, y: i32 }
function churn(n: i32): i32 {
    var p: Point = Point { x: 0, y: 0 };
    var i: i32 = 0;
    while (i < n) {
        p = Point { x: p.x + 1, y: p.y };
        i = i + 1;
    }
    return p.x;
}
function main(): i32 { return churn(3); }`)
	f := funcByName(ip, "churn")
	if f == nil {
		t.Fatal("no func churn")
	}
	if got := allocReuseCount(f); got != 1 {
		t.Errorf("self-overwrite struct reuse should emit one __alloc_reuse, got %d", got)
	}
}

// A single-word rc-tracked pointer field (here an array) now reuses
// too (Phase 5c): the old field value is released on the reuse branch,
// the new one retained on eval.
func TestStructReuseFiresForPointerField(t *testing.T) {
	ip := lowerForTest(t, `struct Holder { id: i32, items: i32[] }
function churn(n: i32): i32 {
    var p: Holder = Holder { id: 0, items: [1, 2] };
    var i: i32 = 0;
    while (i < n) {
        p = Holder { id: p.id + 1, items: p.items };
        i = i + 1;
    }
    return p.id;
}
function main(): i32 { return churn(3); }`)
	f := funcByName(ip, "churn")
	if f == nil {
		t.Fatal("no func churn")
	}
	if got := allocReuseCount(f); got != 1 {
		t.Errorf("array-field struct should reuse, got %d __alloc_reuse", got)
	}
}

// A string field is still excluded — strings are two-word on wasm /
// boxed on arm64, which the single-word reuse temps + flat-dec release
// don't handle. Falls back to the normal fresh alloc.
func TestStructReuseSkipsStringField(t *testing.T) {
	ip := lowerForTest(t, `struct Named { id: i32, name: string }
function churn(n: i32): i32 {
    var p: Named = Named { id: 0, name: "a" };
    var i: i32 = 0;
    while (i < n) {
        p = Named { id: p.id + 1, name: p.name };
        i = i + 1;
    }
    return p.id;
}
function main(): i32 { return churn(3); }`)
	f := funcByName(ip, "churn")
	if f == nil {
		t.Fatal("no func churn")
	}
	if got := allocReuseCount(f); got != 0 {
		t.Errorf("string-field struct must not reuse (single-word temps), got %d", got)
	}
}

// Whether a struct param's box may be reused in place hinges on the ownership
// model. Under the BORROW model a param can be rc==1 while the caller still
// holds it (no caller-side inc), so reusing its box would corrupt the caller's
// value — the freeEligible gate (borrowed params are never eligible) keeps reuse
// off. Under OWNED-by-default (sub-slice 2c, default on) the caller retains an
// aliased arg with an inc, so rc==1 in the callee genuinely means sole
// ownership: reuse is emitted and its runtime is_unique gate makes it safe (an
// aliased arg is rc==2 → fresh alloc; a fresh temp is rc==1 → in-place reuse,
// the FBIP win). The byte-identical differential gate proves the safety.
func TestStructReuseSkipsBorrowedParam(t *testing.T) {
	const src = `struct Point { x: i32, y: i32 }
function bump(p: Point): Point {
    p = Point { x: p.x + 1, y: p.y };
    return p;
}
function main(): i32 { return bump(Point { x: 1, y: 2 }).x; }`
	prev := ast.OwnedByDefault
	defer func() { ast.OwnedByDefault = prev }()

	ast.OwnedByDefault = false
	off := allocReuseCount(funcByName(lowerForTest(t, src), "bump"))
	ast.OwnedByDefault = true
	on := allocReuseCount(funcByName(lowerForTest(t, src), "bump"))

	if off != 0 {
		t.Errorf("borrow model: borrowed param must not reuse in place, got %d", off)
	}
	if on != 1 {
		t.Errorf("owned-by-default: owned param reuses its box (is_unique-gated), want 1 got %d", on)
	}
}

// Wide / float scalar fields now reuse in place (#4356 divergence 1): the
// self-overwrite temps carry a scratchType stamp so the backends size them
// 8 bytes and payloadStoreOpFor picks the width-matched store — the reason
// for the original exclusion ("the reuse temps are i32") is gone.
func TestStructReuseFiresForWideScalarField(t *testing.T) {
	ip := lowerForTest(t, `struct Wide { x: i64, y: i32 }
function churn(n: i32): i64 {
    var p: Wide = Wide { x: 0, y: 0 };
    var i: i32 = 0;
    while (i < n) {
        p = Wide { x: p.x + 1, y: p.y };
        i = i + 1;
    }
    return p.x;
}
function main(): i32 { return 0; }`)
	f := funcByName(ip, "churn")
	if f == nil {
		t.Fatal("no func churn")
	}
	if got := allocReuseCount(f); got != 1 {
		t.Errorf("wide-scalar (i64) field self-overwrite should reuse in place, got %d", got)
	}
}
