package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
)

// Slice 2 TRMC-consuming: a consume-safe TRMC function (owned-by-default
// scrutinee, list-shaped with SCALAR head payloads) frees its scrutinee cell as
// the hole-passing loop advances — an is_unique-gated __fern_box_free (recycled
// into the next node alloc) with a dec+stop fallback at the first shared cell.
// These pin, at the IR layer, that the consume sequence (1) fires for the
// canonical list-map, (2) is withheld under the borrow model, and (3) is
// withheld when the scrutinee head is a POINTER (shallow-freeing the box would
// lose that reference) — the soundness gate. End-to-end correctness + no
// over-release is the differential gate's job (rc_owned_by_default_test).

func boxFreeCountInFn(ip *ir.Program, fn string) int {
	f := funcByName(ip, fn)
	if f == nil {
		return -1
	}
	n := 0
	for _, op := range f.Ops {
		if op.Kind == ir.OpCallDirect && op.Str == "__fern_box_free" {
			n++
		}
	}
	return n
}

func TestTrmcConsumesScalarHeadList(t *testing.T) {
	pobd := ast.OwnedByDefault
	defer func() { ast.OwnedByDefault = pobd }()

	const listMap = `enum List { Cons(i32, List), Nil }
function inc_all(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, inc_all(t)); },
        Nil => { return Nil; },
    }
}
function main(): i32 { return 0; }`

	ast.OwnedByDefault = false
	off := boxFreeCountInFn(lowerForTest(t, listMap), "inc_all")
	ast.OwnedByDefault = true
	on := boxFreeCountInFn(lowerForTest(t, listMap), "inc_all")

	if off != 0 {
		t.Errorf("borrow model: inc_all must not free its scrutinee, got %d box_free", off)
	}
	if on != 1 {
		t.Errorf("consume model: inc_all must free its scrutinee cell once in the loop, got %d box_free", on)
	}
}

func TestTrmcWithholdsConsumeForPointerHead(t *testing.T) {
	pobd := ast.OwnedByDefault
	defer func() { ast.OwnedByDefault = pobd }()

	// `Cons(Inner, List)` is single-hole TRMC (one self-call, last arg) and an
	// owned-by-default-eligible enum, but its HEAD payload is a pointer: a
	// shallow free would drop the box without releasing or moving the head
	// reference. trmcShapeConsumeSafe must reject it.
	const ptrHead = `enum Inner { I(i32), J }
enum List { Cons(Inner, List), Nil }
function walk(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h, walk(t)); },
        Nil => { return Nil; },
    }
}
function main(): i32 { return 0; }`

	ast.OwnedByDefault = true
	on := boxFreeCountInFn(lowerForTest(t, ptrHead), "walk")
	if on != 0 {
		t.Errorf("pointer-head list is not consume-safe; walk must not free its scrutinee, got %d box_free", on)
	}
}
