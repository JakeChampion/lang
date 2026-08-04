// RC of `dyn Trait` values on the arm64 backend (Perceus slice 4c — see
// docs/DYN-TRAITS.md §4.4). The structural mirror of the x86-64 slice 4b
// (rc_heap_bump_dyn_trait_x86_64_test.go): both natives share the BOXED
// representation — a `dyn` value is a single pointer to a 16-byte
// {data, vtable} heap cell — so reclaim must both run the erased concrete
// destructor (through the vtable's trailing drop slot) AND free the cell.
//
// These mirror the wasm 4a proofs (rc_heap_bump_dyn_trait_test.go) and the
// x86-64 4b proofs on arm64: a bounded create+drop loop whose heap
// high-water stays FLAT across N (cell + concrete + the concrete's
// transitively-owned String all reclaim), a no-over-release check, a merged
// multi-trait drop, and a borrowed dispatched-only `dyn` param that must NOT
// be dropped by the callee (no double-free, no premature free of the
// caller's cell). Run under qemu-aarch64.
package e2e

import (
	"testing"
)

// dynTraitBumpGrowthSrcArm64 creates + drops a `dyn Shape` over a struct
// that transitively owns a heap String each iteration. With reclaim the
// 16-byte cell + the String box both free per iteration, so the high-water
// is bounded.
func dynTraitBumpGrowthSrcArm64(n string) string {
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

// TestArm64DynTraitHeapBumpBounded: the core leak/reclaim proof. A loop
// creating + dropping a `dyn Shape` over a String-bearing struct must
// report the SAME bump growth at N=50 and N=5000 — a leak would grow with
// N. Proves the boxed cell, the concrete struct box, AND the transitively-
// owned String all reclaim (otherwise the per-iteration String buffer +
// cell would grow the high-water unboundedly). The natives reuse the
// freelist, so the bounded value may even be 0 — boundedness (small ==
// large) is the assertion, not a specific non-zero plateau.
func TestArm64DynTraitHeapBumpBounded(t *testing.T) {
	small := mustRunArm64FreeOn(t, dynTraitBumpGrowthSrcArm64("50"))
	large := mustRunArm64FreeOn(t, dynTraitBumpGrowthSrcArm64("5000"))
	if small != large {
		t.Errorf("dyn-trait bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d (a leak would grow with N)", small, large)
	}
}

// TestArm64DynTraitNoUnderflow: the same loop must not over-release — no
// double-free of the cell, the concrete box, or the inner String between
// the dyn drop and any other reference. __rc_underflow_count() must be 0.
func TestArm64DynTraitNoUnderflow(t *testing.T) {
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
	if _, code := compileAndRunArm64FreeOn(t, src); code != 0 {
		t.Errorf("dyn-trait over-releases = %d, want 0", code)
	}
}

// TestArm64DynTraitMultiTraitDrop: a merged `dyn A + B` value reclaims
// through the merged-vtable drop slot (at index = the MERGED method count,
// 3 here: a1, b1, b2). The loop must stay bump-bounded, proving the drop
// slot is read at the right merged offset on the boxed representation
// (vtable + methodCount*8 absolute pointer).
func TestArm64DynTraitMultiTraitDrop(t *testing.T) {
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
	small := mustRunArm64FreeOn(t, src("50"))
	large := mustRunArm64FreeOn(t, src("5000"))
	if small != large {
		t.Errorf("multi-trait dyn bump growth should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
}

// TestArm64DynTraitBorrowedParamNoDrop: a `dyn` parameter that is only
// dispatched on (never stored or returned) stays BORROWED — the callee
// must NOT drop it (the caller owns the cell). A double-free of the cell or
// the inner String would surface as a non-zero __rc_underflow_count(); a
// premature free of the cell would corrupt the caller's value (and the
// loop's bump would diverge). The caller still reclaims its own `dyn`
// local each iteration, so the loop stays bounded.
func TestArm64DynTraitBorrowedParamNoDrop(t *testing.T) {
	mk := func(tail string) string {
		return `import "std/i32";
trait Shape {
    function area(self: Self): i32;
}
struct Boxed { tag: string }
impl Shape for Boxed {
    function area(self: Self): i32 { return 7; }
}
// s is dispatched-only -> borrowed; the callee must not drop it.
function use_it(s: dyn Shape): i32 {
    return s.area();
}
function main(): i32 {
    ` + tail + `
}`
	}
	// Underflow check: a borrowed param dropped (the cell freed) by the
	// callee, then again by the caller, would underflow / double-free.
	underflow := mk(`var i: i32 = 0;
    var sum: i32 = 0;
    while (i < 200) {
        var d: dyn Shape = Boxed { tag: "borrowed dyn param string value" };
        sum = sum + use_it(d);
        i = i + 1;
    }
    return __rc_underflow_count();`)
	if _, code := compileAndRunArm64FreeOn(t, underflow); code != 0 {
		t.Errorf("borrowed dyn param over-releases = %d, want 0", code)
	}
	// Bounded check: the caller's local still reclaims; no leak from the
	// borrow (and no double-free corrupting the freelist).
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
	small := mustRunArm64FreeOn(t, bumped("50"))
	large := mustRunArm64FreeOn(t, bumped("5000"))
	if small != large {
		t.Errorf("borrowed-dyn-param bump growth should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
}
