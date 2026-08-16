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

// A `Drop` implementor must never reuse. Reuse hands the dying value's box
// shell to the next same-shaped constructor instead of freeing it, so the
// drop glue — and with it the user finalizer — never runs on the value
// being displaced: a destructor silently skipped on a value that really
// did die, which is worse than leaking it. `reuseClassOf` declines the
// type outright (#2705).
//
// The trait is declared locally rather than imported from `core/mem`
// because the gate matches on the trait's SIMPLE name after demangling,
// so a local `trait Drop` exercises the same path without needing module
// resolution in this test.
func TestStructReuseSkipsDropImplementors(t *testing.T) {
	const body = `function churn(n: i32): i32 {
    var w: W = W { v: [0, 0] };
    var i: i32 = 0;
    while (i < n) {
        w = W { v: [i, i] };
        i = i + 1;
    }
    return 0;
}
function main(): i32 { return churn(3); }`

	// Baseline: without the impl, this exact shape reuses.
	base := lowerForTest(t, `struct W { v: i32[] }
`+body)
	bf := funcByName(base, "churn")
	if bf == nil {
		t.Fatal("no func churn in baseline")
	}
	if got := allocReuseCount(bf); got == 0 {
		t.Fatalf("baseline should reuse (the test is meaningless otherwise), got %d", got)
	}

	withDrop := lowerForTest(t, `trait Drop { function drop(self: Self): void; }
struct W { v: i32[] }
impl Drop for W { function drop(self: Self): void { } }
`+body)
	df := funcByName(withDrop, "churn")
	if df == nil {
		t.Fatal("no func churn with the Drop impl")
	}
	if got := allocReuseCount(df); got != 0 {
		t.Errorf("a Drop implementor must not reuse its box, got %d __alloc_reuse", got)
	}
}

// The finalizer call lands in the generated glue, so every container that
// releases a W through `__drop_struct_W` inherits it.
func TestStructDropGlueCallsUserFinalizer(t *testing.T) {
	ip := lowerForTest(t, `trait Drop { function drop(self: Self): void; }
struct W { v: i32[] }
impl Drop for W { function drop(self: Self): void { } }
function main(): i32 {
    var w: W = W { v: [1] };
    return w.v.len();
}`)
	glue := funcByName(ip, "__drop_struct_W")
	if glue == nil {
		t.Fatal("no __drop_struct_W generated")
	}
	var calls int
	for _, op := range glue.Ops {
		if op.Kind == ir.OpCallDirect && op.Str == "__method_W_drop" {
			calls++
		}
	}
	if calls != 1 {
		t.Errorf("__drop_struct_W should call __method_W_drop exactly once, got %d", calls)
	}
	// It must precede the box free — the body reads `self`.
	freeAt, dropAt := -1, -1
	for i, op := range glue.Ops {
		if op.Kind == ir.OpCallDirect && op.Str == "__method_W_drop" && dropAt < 0 {
			dropAt = i
		}
		if op.Kind == ir.OpCallDirect && op.Str == "__fern_box_free" && freeAt < 0 {
			freeAt = i
		}
	}
	if dropAt < 0 || freeAt < 0 || dropAt > freeAt {
		t.Errorf("finalizer must run before __fern_box_free (drop at %d, free at %d)", dropAt, freeAt)
	}
}

// `ast` is used by the sibling tests in this file; keep the import live if
// this file is ever trimmed to only the tests above.
var _ = ast.NumberType{}
