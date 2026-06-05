package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
)

// boxFreeCount counts __fern_box_free calls — the consuming-match SHALLOW free
// (C1). Under C2 the Cons arm reuses the scrutinee box instead, so this drops.
func boxFreeCount(fn *ir.Func) int {
	n := 0
	for _, op := range fn.Ops {
		if op.Kind == ir.OpCallDirect && op.Str == "__fern_box_free" {
			n++
		}
	}
	return n
}

const c2MapInc = `enum List { Cons(i32, List), Nil }
function map_inc(own xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, map_inc(t)); },
        Nil => { return Nil; },
    }
}
function main(): i32 { return 0; }`

// C2 (true zero-alloc FBIP): the Cons arm of a consuming match constructs a
// same-enum variant, so the consumed scrutinee box is handed straight to that
// construction via the reuse token — one __alloc_reuse, and the Cons arm's C1
// shallow free is gone. (The Nil arm matches a payloadless sentinel and builds
// no node, so it keeps its sentinel-dec free — one __fern_box_free remains.)
func TestConsumingMatchReuseFires(t *testing.T) {
	f := funcByName(lowerForTest(t, c2MapInc), "map_inc")
	if f == nil {
		t.Fatal("no func map_inc")
	}
	if got := allocReuseCount(f); got != 1 {
		t.Errorf("C2: Cons arm should reuse the scrutinee box (one __alloc_reuse), got %d", got)
	}
	// Cons arm reuse replaces its free; only the Nil arm's free remains.
	if got := boxFreeCount(f); got != 1 {
		t.Errorf("C2: only the Nil arm should free (one __fern_box_free), got %d", got)
	}
}

// With the reuse layer off, the same function falls back to C1: no
// __alloc_reuse, and BOTH arms shallow-free their scrutinee box (two
// __fern_box_free). Pins that C2 is a pure optimisation gated on
// RcReuseEnabled — the byte-identical baseline the differential e2e gate
// compares against.
func TestConsumingMatchReuseDisabledByFlag(t *testing.T) {
	prev := ast.RcReuseEnabled
	ast.RcReuseEnabled = false
	defer func() { ast.RcReuseEnabled = prev }()
	f := funcByName(lowerForTest(t, c2MapInc), "map_inc")
	if got := allocReuseCount(f); got != 0 {
		t.Errorf("reuse off: no __alloc_reuse expected, got %d", got)
	}
	if got := boxFreeCount(f); got != 2 {
		t.Errorf("reuse off: both arms shallow-free (two __fern_box_free), got %d", got)
	}
}
