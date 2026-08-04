// RC of PRIMITIVE/STRING payloads behind `dyn Trait` (#4351). A primitive
// or string coerced to `dyn Trait` is heap-boxed into a VALUE CELL
// (boxPrimitiveDynValue) that the fat pointer's `data` word points at.
// Before this slice the vtable's trailing drop slot for a primitive
// concrete was the null sentinel, so the cell leaked on every coercion —
// a churn loop grew the bump high-water linearly on every backend. The
// generated __drop_dynprim_<prim> destructor now frees exactly that cell
// (the string BUFFER behind a string payload deliberately stays leak-mode:
// the coercion takes no retain, so an aliased source must never be freed
// from the dyn drop; literals are static sentinels and cost nothing).
//
// Also pinned here: move-on-return for a swept `dyn` local. The exit
// sweep's dyn arm drops unconditionally, so `var d: dyn T = ...; return d;`
// handed the CALLER a freed cell — a segfault on the natives and a garbage
// dispatch on wasm. The Return lowering now excludes a returned bare dyn
// local from the sweep (a pure move — dyn cells carry no rc header, so
// there is no transfer inc to cancel).
package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// dynPrimBumpGrowthSrc churns a `dyn Show` over a plain i32: per iteration
// the coercion allocates the prim value cell (+ the boxed dyn cell on the
// natives); with the drop-slot fix both free, so the high-water is bounded.
func dynPrimBumpGrowthSrc(n string) string {
	return `import "std/i32";
trait Show {
    function show(self: Self): i32;
}
impl Show for i32 {
    function show(self: Self): i32 { return self + 1; }
}
function go(k: i32): i32 { var d: dyn Show = k; return d.show(); }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < ` + n + `) {
        sum = (sum + go(41)) % 251;
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// dynPrimStringBumpGrowthSrc churns a `dyn Show` over a string LITERAL:
// the value cell frees per iteration and the literal is a static sentinel,
// so the high-water is bounded.
func dynPrimStringBumpGrowthSrc(n string) string {
	return `import "std/i32";
trait Show {
    function show(self: Self): i32;
}
impl Show for string {
    function show(self: Self): i32 { return self.len(); }
}
function go(k: i32): i32 { var d: dyn Show = "hello"; return d.show() + k; }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < ` + n + `) {
        sum = (sum + go(3)) % 251;
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// dynLocalReturnMoveSrc returns a bare dyn LOCAL from mk() and dispatches
// in the caller — the move-on-return regression (pre-fix: the callee's
// exit sweep freed the returned cell; the caller's dispatch then read
// reclaimed memory). Covers both a prim and a struct concrete, plus the
// no-underflow detector. Want 0.
const dynLocalReturnMoveSrc = `import "std/i32";
trait Show {
    function show(self: Self): i32;
}
struct Dot { r: i32 }
impl Show for i32 {
    function show(self: Self): i32 { return self + 1; }
}
impl Show for Dot {
    function show(self: Self): i32 { return self.r + 1; }
}
function mkprim(): dyn Show { var d: dyn Show = 41; return d; }
function mkstruct(): dyn Show { var d: dyn Show = Dot { r: 41 }; return d; }
function main(): i32 {
    var i: i32 = 0;
    while (i < 500) {
        var e = mkprim();
        if (e.show() != 42) { return 1; }
        var f = mkstruct();
        if (f.show() != 42) { return 2; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`

func TestWASMDynTraitPrimHeapBumpBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, dynPrimBumpGrowthSrc("50"))
	large := runWasm(t, dynPrimBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("dyn prim value-cell growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d (a leak would grow with N)", small, large)
	}
}

func TestWASMDynTraitPrimStringHeapBumpBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, dynPrimStringBumpGrowthSrc("50"))
	large := runWasm(t, dynPrimStringBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("dyn string-payload value-cell growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestWASMDynTraitLocalReturnMove(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, dynLocalReturnMoveSrc); got != 0 {
		t.Errorf("returned dyn local dispatch = %d, want 0 (1/2 = wrong dispatch value — freed cell; 99 = rc underflow)", got)
	}
}

func TestX86_64DynTraitPrimHeapBumpBounded(t *testing.T) {
	small := mustRunX86_64FreeOn(t, dynPrimBumpGrowthSrc("50"))
	large := mustRunX86_64FreeOn(t, dynPrimBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("dyn prim value-cell growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestX86_64DynTraitPrimStringHeapBumpBounded(t *testing.T) {
	small := mustRunX86_64FreeOn(t, dynPrimStringBumpGrowthSrc("50"))
	large := mustRunX86_64FreeOn(t, dynPrimStringBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("dyn string-payload value-cell growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestX86_64DynTraitLocalReturnMove(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, dynLocalReturnMoveSrc); code != 0 {
		t.Errorf("returned dyn local dispatch exited %d, want 0 (pre-fix: segfault — the exit sweep freed the returned cell)", code)
	}
}

// dynEnumPayloadBumpGrowthSrc churns a `dyn Show` over a variant
// construction — the enum concrete reclaims through its generated
// __drop_enum_<C> vtable drop (plus the boxed cell on the natives), so the
// high-water is bounded. Pins the enum-concrete arm of the dyn drop
// (#4351 slice 4's native counterpart, which already existed — this is
// the missing coverage).
func dynEnumPayloadBumpGrowthSrc(n string) string {
	return `import "std/i32";
trait Show {
    function show(self: Self): i32;
}
enum Op { Add(i32), Neg }
impl Show for Op {
    function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } }
}
function go(k: i32): i32 { var d: dyn Show = Add(41); return d.show() + k; }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < ` + n + `) {
        sum = (sum + go(3)) % 251;
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// The wasm sibling: #4786 — a struct/enum-behind-dyn local dropped at a
// helper function's exit sweep trapped (memory fault at 0xffffffff), because
// the Inline pass mis-spliced the two-word inline `dyn` value's exit-sweep
// reclaim, running it on the slot's pre-init value and stranding the fresh
// box un-swept → freelist corruption. Fixed by excluding dyn-slot callees
// from inlining (internal/ir/inline.go). Now bounded like the natives.
func TestWASMDynTraitEnumPayloadHeapBumpBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, dynEnumPayloadBumpGrowthSrc("50"))
	large := runWasm(t, dynEnumPayloadBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("dyn enum-payload growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestX86_64DynTraitEnumPayloadHeapBumpBounded(t *testing.T) {
	small := mustRunX86_64FreeOn(t, dynEnumPayloadBumpGrowthSrc("50"))
	large := mustRunX86_64FreeOn(t, dynEnumPayloadBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("dyn enum-payload growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

// dynStructFnExitBumpGrowthSrc churns a `dyn Show` over a STRUCT concrete
// through a helper FUNCTION (the dyn local dies at the helper's exit sweep).
// This is the general #4786 shape — a struct behind dyn dropped at a
// non-main function exit also tripped the inliner two-word mis-splice on
// wasm; not enum-specific.
func dynStructFnExitBumpGrowthSrc(n string) string {
	return `import "std/i32";
trait Show {
    function show(self: Self): i32;
}
struct Dot { r: i32 }
impl Show for Dot {
    function show(self: Self): i32 { return self.r * 2; }
}
function go(k: i32): i32 { var d: dyn Show = Dot { r: 41 }; return d.show() + k; }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < ` + n + `) {
        sum = (sum + go(3)) % 251;
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func TestWASMDynTraitStructFnExitHeapBumpBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, dynStructFnExitBumpGrowthSrc("50"))
	large := runWasm(t, dynStructFnExitBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("fn-exit dyn struct growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestX86_64DynTraitStructFnExitHeapBumpBounded(t *testing.T) {
	small := mustRunX86_64FreeOn(t, dynStructFnExitBumpGrowthSrc("50"))
	large := mustRunX86_64FreeOn(t, dynStructFnExitBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("fn-exit dyn struct growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestArm64DynTraitEnumPayloadHeapBumpBounded(t *testing.T) {
	small := mustRunArm64FreeOn(t, dynEnumPayloadBumpGrowthSrc("50"))
	large := mustRunArm64FreeOn(t, dynEnumPayloadBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("dyn enum-payload growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

// dynArrFnExitBumpGrowthSrc churns a `dyn Show[]` local inside a helper
// FUNCTION — the exit-sweep path. The loop-var reinit path (covered by the
// container suite) already walked elements via __drop_arr_dyn_<set>, but the
// exit sweep's array arm gated on arrElemIsRcTracked (false for dyn) and fell
// to the buffer-only dec, leaking every element per call (#4351). Bounded now.
func dynArrFnExitBumpGrowthSrc(n string) string {
	return `import "std/i32";
trait Show {
    function show(self: Self): i32;
}
struct Dot { r: i32 }
impl Show for i32 {
    function show(self: Self): i32 { return self + 1; }
}
impl Show for Dot {
    function show(self: Self): i32 { return self.r * 2; }
}
function go(k: i32): i32 { var xs: dyn Show[] = [k, Dot { r: k }, 7]; return xs[0].show() + xs[1].show() + xs[2].show(); }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < ` + n + `) {
        sum = (sum + go(3)) % 251;
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// TestWASMDynTraitArrFnExitCorrect: wasm deliberately KEEPS the buffer-only
// dec for function-exit dyn arrays (its inline two-word elements double-drop
// when an element was bound out — see the exit-sweep arm's wasm caveat in
// rc_insert.go), so there is no bounded-growth assertion here. This pins the
// correctness side only: values stay right across the churn. If wasm later
// gains a sound element walk, promote this to the bounded shape the native
// siblings use.
func TestWASMDynTraitArrFnExitCorrect(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	src := `import "std/i32";
trait Show {
    function show(self: Self): i32;
}
struct Dot { r: i32 }
impl Show for i32 {
    function show(self: Self): i32 { return self + 1; }
}
impl Show for Dot {
    function show(self: Self): i32 { return self.r * 2; }
}
function go(k: i32): i32 { var xs: dyn Show[] = [k, Dot { r: k }, 7]; return xs[0].show() + xs[1].show() + xs[2].show(); }
function main(): i32 {
    var i: i32 = 0;
    var bad: i32 = 0;
    while (i < 500) {
        if (go(3) != 18) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return bad;
}`
	if got := runWasm(t, src); got != 0 {
		t.Errorf("wasm function-exit dyn array churn = %d, want 0 (1 = wrong dispatch value; 99 = over-release)", got)
	}
}

func TestX86_64DynTraitArrFnExitHeapBumpBounded(t *testing.T) {
	small := mustRunX86_64FreeOn(t, dynArrFnExitBumpGrowthSrc("50"))
	large := mustRunX86_64FreeOn(t, dynArrFnExitBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("function-exit dyn array growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestArm64DynTraitArrFnExitHeapBumpBounded(t *testing.T) {
	small := mustRunArm64FreeOn(t, dynArrFnExitBumpGrowthSrc("50"))
	large := mustRunArm64FreeOn(t, dynArrFnExitBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("function-exit dyn array growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestArm64DynTraitPrimHeapBumpBounded(t *testing.T) {
	small := mustRunArm64FreeOn(t, dynPrimBumpGrowthSrc("50"))
	large := mustRunArm64FreeOn(t, dynPrimBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("dyn prim value-cell growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestArm64DynTraitLocalReturnMove(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, dynLocalReturnMoveSrc); code != 0 {
		t.Errorf("returned dyn local dispatch exited %d, want 0 (pre-fix: segfault — the exit sweep freed the returned cell)", code)
	}
}
