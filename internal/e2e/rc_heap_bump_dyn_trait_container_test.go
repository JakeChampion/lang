// RC of `dyn Trait` values NESTED INSIDE A CONTAINER (docs/DYN-TRAITS.md
// §7.8 — the follow-up after standalone trait-object RC shipped, slices
// 4a/4b/4c). The headline container is `dyn Shape[]` — a heterogeneous
// array of trait objects (the §2 motivating example). Before this slice a
// `dyn` value held inside a container LEAKED on the natives: the
// container's recursive-drop path declined a `DynTraitType` element, so
// the buffer freed but every element's box (the boxed {data,vtable} cell
// on the natives), its concrete object, and that concrete's transitively-
// owned String all leaked.
//
// This slice adds a dedicated `__drop_arr_dyn_<set>` array drop that walks
// each element and runs the per-set `__drop_dyn_<set>` destructor on it
// (representation-aware: a one-word boxed cell ptr on the natives, a
// two-word inline `[data, vtable]` on wasm), then frees the outer buffer.
//
// Proof shape mirrors the standalone dyn proofs
// (rc_heap_bump_dyn_trait_{test,x86_64_test,aarch64_test}.go): a bounded
// loop that creates + drops a `dyn Shape[]` of String-owning concretes
// each iteration and asserts the heap high-water stays FLAT across N (a
// leak would grow with N), plus a no-over-release check. x86-64 + arm64
// (qemu) + wasm.
package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// dynShapeArraySrc builds + drops a heterogeneous `dyn Shape[]` of two
// concretes that EACH transitively own a heap String, n times. With
// per-element reclaim every element cell + concrete + String frees per
// iteration, so the high-water is bounded; a leak grows it with n.
func dynShapeArraySrc(n string) string {
	return `import "std/i32";
trait Shape {
    function area(self: Self): i32;
}
struct Circle { tag: string }
struct Rect   { label: string }
impl Shape for Circle { function area(self: Self): i32 { return 1; } }
impl Shape for Rect   { function area(self: Self): i32 { return 2; } }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < ` + n + `) {
        var shapes: dyn Shape[] = [
            Circle { tag: "a heap string owned by a circle behind dyn" },
            Rect   { label: "another heap string owned by a rect behind dyn" }
        ];
        sum = sum + shapes[0].area() + shapes[1].area();
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// dynShapeArrayUnderflowSrc: the same loop, returning the over-release
// count. The dedicated array-of-dyn drop must not double-free any
// element's cell, concrete box, or inner String.
func dynShapeArrayUnderflowSrc(n string) string {
	return `import "std/i32";
trait Shape {
    function area(self: Self): i32;
}
struct Circle { tag: string }
struct Rect   { label: string }
impl Shape for Circle { function area(self: Self): i32 { return 1; } }
impl Shape for Rect   { function area(self: Self): i32 { return 2; } }
function main(): i32 {
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < ` + n + `) {
        var shapes: dyn Shape[] = [
            Circle { tag: "a heap string owned by a circle behind dyn" },
            Rect   { label: "another heap string owned by a rect behind dyn" }
        ];
        sum = sum + shapes[0].area() + shapes[1].area();
        i = i + 1;
    }
    return __rc_underflow_count();
}`
}

// --- x86-64 ---------------------------------------------------------------

// TestX86_64DynShapeArrayHeapBumpBounded: the headline container proof on
// x86-64. A loop creating + dropping a `dyn Shape[]` of String-owning
// concretes must report the SAME bump growth at N=50 and N=5000 — a leak
// (every element cell + concrete + String per iteration) would grow with N.
func TestX86_64DynShapeArrayHeapBumpBounded(t *testing.T) {
	small := mustRunX86_64FreeOn(t, dynShapeArraySrc("50"))
	large := mustRunX86_64FreeOn(t, dynShapeArraySrc("5000"))
	if small != large {
		t.Errorf("dyn Shape[] bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d (a leak would grow with N)", small, large)
	}
}

// TestX86_64DynShapeArrayNoUnderflow: the array-of-dyn drop must not
// over-release. __rc_underflow_count() must be 0.
func TestX86_64DynShapeArrayNoUnderflow(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, dynShapeArrayUnderflowSrc("200")); code != 0 {
		t.Errorf("dyn Shape[] over-releases = %d, want 0", code)
	}
}

// --- arm64 (qemu) ---------------------------------------------------------

// TestArm64DynShapeArrayHeapBumpBounded: the structural mirror on arm64 —
// same boxed representation, same dedicated array-of-dyn drop.
func TestArm64DynShapeArrayHeapBumpBounded(t *testing.T) {
	small := mustRunArm64FreeOn(t, dynShapeArraySrc("50"))
	large := mustRunArm64FreeOn(t, dynShapeArraySrc("5000"))
	if small != large {
		t.Errorf("dyn Shape[] bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d (a leak would grow with N)", small, large)
	}
}

// TestArm64DynShapeArrayNoUnderflow: no over-release on arm64.
func TestArm64DynShapeArrayNoUnderflow(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, dynShapeArrayUnderflowSrc("200")); code != 0 {
		t.Errorf("dyn Shape[] over-releases = %d, want 0", code)
	}
}

// dynMultiTraitArraySrc: a `dyn A + B[]` array of multi-trait objects.
// Each element's MERGED vtable carries the drop slot at the merged method
// count; the array-of-dyn drop must read it at that merged offset. Bounded
// loop proves the per-element merged-drop reclaims the concrete + String.
func dynMultiTraitArraySrc(n string) string {
	return `import "std/i32";
trait A { function a1(self: Self): i32; }
trait B { function b1(self: Self): i32; function b2(self: Self): i32; }
struct Both  { tag: string }
struct Other { name: string }
impl A for Both  { function a1(self: Self): i32 { return 1; } }
impl B for Both  { function b1(self: Self): i32 { return 2; } function b2(self: Self): i32 { return 3; } }
impl A for Other { function a1(self: Self): i32 { return 4; } }
impl B for Other { function b1(self: Self): i32 { return 5; } function b2(self: Self): i32 { return 6; } }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < ` + n + `) {
        var xs: dyn A + B[] = [
            Both  { tag: "a string owned by Both behind a multi-trait dyn" },
            Other { name: "a string owned by Other behind a multi-trait dyn" }
        ];
        sum = sum + xs[0].a1() + xs[1].b2();
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// TestX86_64DynMultiTraitArrayHeapBumpBounded: the merged-vtable array case
// on x86-64 — the array-of-dyn drop reads each element's drop slot at the
// MERGED method count (3 here), reclaiming the concrete + String.
func TestX86_64DynMultiTraitArrayHeapBumpBounded(t *testing.T) {
	small := mustRunX86_64FreeOn(t, dynMultiTraitArraySrc("50"))
	large := mustRunX86_64FreeOn(t, dynMultiTraitArraySrc("5000"))
	if small != large {
		t.Errorf("dyn A + B[] bump growth should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
}

// TestArm64DynMultiTraitArrayHeapBumpBounded: the structural mirror on arm64.
func TestArm64DynMultiTraitArrayHeapBumpBounded(t *testing.T) {
	small := mustRunArm64FreeOn(t, dynMultiTraitArraySrc("50"))
	large := mustRunArm64FreeOn(t, dynMultiTraitArraySrc("5000"))
	if small != large {
		t.Errorf("dyn A + B[] bump growth should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
}

// --- wasm -----------------------------------------------------------------

// TestWASMDynShapeArrayHeapBumpBounded: the same proof on wasm (inline
// two-word `dyn` elements). Falling to the buffer-only __fern_arr_dec leaks
// the array case on wasm exactly as on the natives; the dedicated
// array-of-dyn drop covers both, and this guards the wasm side.
func TestWASMDynShapeArrayHeapBumpBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, dynShapeArraySrc("50"))
	large := runWasm(t, dynShapeArraySrc("5000"))
	if small != large {
		t.Errorf("dyn Shape[] bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d (a leak would grow with N)", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
}

// TestWASMDynShapeArrayNoUnderflow: no over-release on wasm.
func TestWASMDynShapeArrayNoUnderflow(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, dynShapeArrayUnderflowSrc("200")); got != 0 {
		t.Errorf("dyn Shape[] over-releases = %d, want 0", got)
	}
}

// TestWASMDynMultiTraitArrayHeapBumpBounded: the merged-vtable array case on
// wasm (inline two-word elements; the drop slot is a function-table index
// at the merged method count).
func TestWASMDynMultiTraitArrayHeapBumpBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, dynMultiTraitArraySrc("50"))
	large := runWasm(t, dynMultiTraitArraySrc("5000"))
	if small != large {
		t.Errorf("dyn A + B[] bump growth should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
}
