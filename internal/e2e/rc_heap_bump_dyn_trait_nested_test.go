// RC of a `dyn Trait` value held as an ENUM PAYLOAD (docs/DYN-TRAITS.md
// §7.8 — the nested-container follow-up after the array-element kind
// shipped). An enum variant carrying a `dyn` payload — `enum Box {
// Wrap(dyn Shape), Empty }` — leaks the boxed `dyn` (its {data,vtable}
// cell, the concrete object, and any String the concrete transitively owns)
// on the natives if the enum's recursive drop declines
// a `DynTraitType` payload (`enumVariantDropPlan` / `appendChildDrop` /
// `dropStructField` gated `DynTraitType` out).
//
// This slice threads the backend's dyn-RC capability into those drop
// sites so an enum-with-`dyn`-payload routes its payload through the
// per-set `__drop_dyn_<set>` destructor (boxed one-word cell ptr → the
// vtable's trailing drop slot → the concrete dtor → __free the cell).
// NATIVES ONLY (x86-64 + arm64): wasm's INLINE two-word `dyn` double-
// drops when the payload is matched-and-bound (`match (b) { Wrap(s) =>
// … }` binds `s`, which reclaims the same `data` the container drop
// does), so wasm keeps its prior correct-but-leaking behaviour for this
// kind — the array-element kind still reclaims on wasm via the separate
// genArrDynDropFn path. See §7.8.
//
// Proof shape mirrors the array-element proofs
// (rc_heap_bump_dyn_trait_container_test.go): a bounded loop that
// creates + drops an enum-of-`dyn` of a String-owning concrete each
// iteration and asserts the heap high-water stays FLAT across N (a leak
// would grow with N), plus a no-over-release check.
package e2e

import "testing"

// dynEnumPayloadSrc builds + drops an `enum Box { Wrap(dyn Shape), Empty }`
// whose `dyn` wraps a String-owning Circle, n times, matching it each
// iteration (the double-free-sensitive shape — the bound `s` and the
// enum drop both see the payload). With per-payload reclaim the cell +
// concrete + String free per iteration, so the high-water is bounded; a
// leak grows it with n.
func dynEnumPayloadSrc(n string) string {
	return `trait Shape {
    function area(self: Self): i32;
}
struct Circle { tag: string }
impl Shape for Circle { function area(self: Self): i32 { return 1; } }
enum Box { Wrap(dyn Shape), Empty }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < ` + n + `) {
        var dc: dyn Shape = Circle { tag: "a heap string owned by a circle behind a dyn enum payload" };
        var b: Box = Box.Wrap(dc);
        match (b) {
            Wrap(s) => { sum = sum + s.area(); },
            Empty => { sum = sum + 0; },
        }
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// dynEnumPayloadUnderflowSrc: the same loop, returning the over-release
// count. The enum payload drop must not double-free the cell, concrete
// box, or inner String (matched-and-bound is the risky path).
func dynEnumPayloadUnderflowSrc(n string) string {
	return `trait Shape {
    function area(self: Self): i32;
}
struct Circle { tag: string }
impl Shape for Circle { function area(self: Self): i32 { return 1; } }
enum Box { Wrap(dyn Shape), Empty }
function main(): i32 {
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < ` + n + `) {
        var dc: dyn Shape = Circle { tag: "a heap string owned by a circle behind a dyn enum payload" };
        var b: Box = Box.Wrap(dc);
        match (b) {
            Wrap(s) => { sum = sum + s.area(); },
            Empty => { sum = sum + 0; },
        }
        i = i + 1;
    }
    return __rc_underflow_count();
}`
}

// --- x86-64 ---------------------------------------------------------------

// TestX86_64DynEnumPayloadHeapBumpBounded: the enum-payload proof on
// x86-64. A loop creating + dropping an enum-of-`dyn` of a String-owning
// Circle must report the SAME bump growth at N=50 and N=5000 — a leak
// (every cell + concrete + String per iteration) would grow with N.
func TestX86_64DynEnumPayloadHeapBumpBounded(t *testing.T) {
	small := mustRunX86_64FreeOn(t, dynEnumPayloadSrc("50"))
	large := mustRunX86_64FreeOn(t, dynEnumPayloadSrc("5000"))
	if small != large {
		t.Errorf("enum-of-dyn bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d (a leak would grow with N)", small, large)
	}
}

// TestX86_64DynEnumPayloadNoUnderflow: the enum payload drop must not
// over-release. __rc_underflow_count() must be 0.
func TestX86_64DynEnumPayloadNoUnderflow(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, dynEnumPayloadUnderflowSrc("200")); code != 0 {
		t.Errorf("enum-of-dyn over-releases = %d, want 0", code)
	}
}

// --- arm64 (qemu) ---------------------------------------------------------

// TestArm64DynEnumPayloadHeapBumpBounded: the structural mirror on arm64 —
// same boxed representation, same per-payload __drop_dyn_<set> reclaim.
func TestArm64DynEnumPayloadHeapBumpBounded(t *testing.T) {
	small := mustRunArm64FreeOn(t, dynEnumPayloadSrc("50"))
	large := mustRunArm64FreeOn(t, dynEnumPayloadSrc("5000"))
	if small != large {
		t.Errorf("enum-of-dyn bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d (a leak would grow with N)", small, large)
	}
}

// TestArm64DynEnumPayloadNoUnderflow: no over-release on arm64.
func TestArm64DynEnumPayloadNoUnderflow(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, dynEnumPayloadUnderflowSrc("200")); code != 0 {
		t.Errorf("enum-of-dyn over-releases = %d, want 0", code)
	}
}
