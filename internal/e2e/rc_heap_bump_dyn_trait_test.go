// RC of `dyn Trait` values on the wasm backend (Perceus slice 4a — see
// docs/DYN-TRAITS.md §4.4). Before this slice every compiled `dyn Trait`
// value LEAKED: the concrete `data` object behind the fat pointer was
// never dec'd. This slice teaches the Perceus dec/drop sweep to treat a
// `dyn` value as owning and reclaim it at scope exit through the
// per-trait-set `__drop_dyn_<set>` helper, which reads the vtable's
// trailing drop slot and dispatches the erased concrete destructor.
//
// The proof shape mirrors TestWASMDestructureHeapBumpBounded: a loop that
// creates + drops many `dyn` values and asserts the bump high-water stays
// FLAT (reclaim works) rather than growing with the iteration count (a
// leak). wasm only — the natives still leak `dyn` (slices 4b/4c), so no
// native counterpart here.
package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// dynTraitBumpGrowthSrc creates + drops a `dyn Shape` over a struct that
// transitively owns a heap String each iteration. With reclaim the box +
// the String both free per iteration, so the high-water is bounded.
func dynTraitBumpGrowthSrc(n string) string {
	return `import "std/i32";
trait Shape {
    function area(self: Self): i32;
}
struct Boxed { tag: string }
impl Shape for Boxed {
    function area(self: Self): i32 { return 1; }
}
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < ` + n + `) {
        var d: dyn Shape = Boxed { tag: "a heap-allocated string behind dyn" };
        sum = sum + d.area();
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// TestWASMDynTraitHeapBumpBounded: the core leak/reclaim proof. A loop
// creating + dropping a `dyn Shape` over a String-bearing struct must
// report the SAME bump growth at N=50 and N=5000 — a leak would grow with
// N. Proves BOTH the box and the transitively-owned String reclaim
// (otherwise the per-iteration String buffer alone would grow the
// high-water unboundedly).
func TestWASMDynTraitHeapBumpBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, dynTraitBumpGrowthSrc("50"))
	large := runWasm(t, dynTraitBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("dyn-trait bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d (a leak would grow with N)", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
}

// TestWASMDynTraitNoUnderflow: the same loop must not over-release (no
// double-free of the box or the inner String between the dyn drop and any
// other reference). __rc_underflow_count() must be 0.
func TestWASMDynTraitNoUnderflow(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	src := `import "std/i32";
trait Shape {
    function area(self: Self): i32;
}
struct Boxed { tag: string }
impl Shape for Boxed {
    function area(self: Self): i32 { return 1; }
}
function main(): i32 {
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < 200) {
        var d: dyn Shape = Boxed { tag: "another heap string for the dyn box" };
        sum = sum + d.area();
        i = i + 1;
    }
    return __rc_underflow_count();
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("dyn-trait over-releases = %d, want 0", got)
	}
}

// TestWASMDynTraitMultiTraitDrop: a merged `dyn A + B` value reclaims
// through the merged-vtable drop slot (at index = the MERGED method count,
// 3 here: a1, b1, b2). The loop must stay bump-bounded, proving the drop
// slot is read at the right merged offset.
func TestWASMDynTraitMultiTraitDrop(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	src := func(n string) string {
		return `import "std/i32";
trait A { function a1(self: Self): i32; }
trait B { function b1(self: Self): i32; function b2(self: Self): i32; }
struct Both { tag: string }
impl A for Both { function a1(self: Self): i32 { return 1; } }
impl B for Both {
    function b1(self: Self): i32 { return 2; }
    function b2(self: Self): i32 { return 3; }
}
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < ` + n + `) {
        var d: dyn A + B = Both { tag: "string owned behind a multi-trait dyn" };
        sum = sum + d.a1() + d.b1() + d.b2();
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
	}
	small := runWasm(t, src("50"))
	large := runWasm(t, src("5000"))
	if small != large {
		t.Errorf("multi-trait dyn bump growth should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
}

// TestWASMDynTraitBorrowedParamNoDrop: a `dyn` parameter that is only
// dispatched on (never stored or returned) stays BORROWED — the callee
// must NOT drop it (the caller owns it). A double-free would surface as a
// non-zero __rc_underflow_count(); a premature drop would corrupt the
// caller's value (and the loop's bump would diverge). The caller still
// reclaims its own `dyn` local each iteration, so the loop stays bounded.
func TestWASMDynTraitBorrowedParamNoDrop(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	mk := func(tail string) string {
		return `import "std/i32";
trait Shape {
    function area(self: Self): i32;
}
struct Boxed { tag: string }
impl Shape for Boxed {
    function area(self: Self): i32 { return 7; }
}
// s is dispatched-only → borrowed; the callee must not drop it.
function use_it(s: dyn Shape): i32 {
    return s.area();
}
function main(): i32 {
    ` + tail + `
}`
	}
	// Underflow check: borrowed param dropped twice would underflow.
	underflow := mk(`var i: i32 = 0;
    var sum: i32 = 0;
    while (i < 200) {
        var d: dyn Shape = Boxed { tag: "borrowed dyn param string value" };
        sum = sum + use_it(d);
        i = i + 1;
    }
    return __rc_underflow_count();`)
	if got := runWasm(t, underflow); got != 0 {
		t.Errorf("borrowed dyn param over-releases = %d, want 0", got)
	}
	// Bounded check: the caller's local still reclaims; no leak from the
	// borrow.
	bumped := func(n string) string {
		return mk(`var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < ` + n + `) {
        var d: dyn Shape = Boxed { tag: "borrowed dyn param string value" };
        sum = sum + use_it(d);
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;`)
	}
	small := runWasm(t, bumped("50"))
	large := runWasm(t, bumped("5000"))
	if small != large {
		t.Errorf("borrowed-dyn-param bump growth should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
}
