// RC of a `dyn Trait` value CAPTURED BY A CLOSURE (docs/DYN-TRAITS.md
// §7.8 — the closure-capture kind, the last nested-`dyn` container after
// array element, enum payload, struct field, and tuple element).
//
// A captured `dyn` is move-only (needsRcIncOnAlias declines it, so there
// is NO inc at MakeEnv). The standalone-`dyn` reclaim (slices 4b/4c)
// sweeps a `dyn` LOCAL at scope exit through emitDec's DynTraitType arm —
// but a `dyn` captured by an ESCAPING closure (`return function () { …
// d.m() … }`) was NOT excluded from that sweep, so the source local's
// drop freed the boxed cell while the returned closure still held (and
// later dereferenced) it: a use-after-free that SEGFAULTED on the natives.
//
// This slice closes both halves on the natives (x86-64 + arm64):
//   - markConstructionMoves' MakeClosure arm now marks a `dyn` capture
//     MOVED (suppressing the source local's exit-sweep drop), and
//   - genClosureDropThunk reclaims the captured `dyn` via the per-set
//     __drop_dyn_<set> destructor (boxed one-word cell ptr → the vtable's
//     trailing drop slot → the concrete dtor → __free the cell; argc 1,
//     VOID return, so NO trailing OpDrop).
//
// Net: no MakeEnv inc + suppressed source drop + one thunk reclaim =
// exactly one reclaim of the cell + concrete + any String the concrete
// transitively owns. NATIVES ONLY (boxed one-word cell, single owner
// after the move); wasm's INLINE two-word `dyn` capture keeps its prior
// correct-but-leaking behaviour (its env copy isn't reclaimed, the thunk
// declines it, and the source local stays swept — gated on dynRcSupported,
// not dynReclaim). See §7.8.
//
// Proof shape mirrors the other nested-`dyn` proofs
// (rc_heap_bump_dyn_trait_nested_test.go): a bounded loop that builds a
// closure capturing a String-owning `dyn` (the closure ESCAPES its
// constructor — the use-after-free-sensitive shape), calls it, and drops
// it each iteration; the heap high-water must stay FLAT across N (a leak
// or the old UAF would grow it / crash), plus a no-over-release check.
package e2e

import "testing"

// dynClosureCaptureSrc builds + drops, n times, a closure that CAPTURES a
// String-owning Circle behind `dyn Shape` and ESCAPES its constructor
// (returned to the caller). The captured `dyn` is moved into the env; with
// per-capture reclaim the cell + concrete + String free when the closure
// dies, so the high-water is bounded. A leak grows it with n; the old
// (pre-fix) double-drop UAF segfaulted.
func dynClosureCaptureSrc(n string) string {
	return `trait Shape {
    function area(self: Self): i32;
}
struct Circle { tag: string }
impl Shape for Circle { function area(self: Self): i32 { return 1; } }
function make(seed: i32): () => i32 {
    var dc: dyn Shape = Circle { tag: "a heap string owned by a circle behind a captured dyn" };
    return function (): i32 { return dc.area(); };
}
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < ` + n + `) {
        var f: () => i32 = make(i);
        sum = sum + f();
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// dynClosureCaptureUnderflowSrc: the same loop, returning the over-release
// count. The capture reclaim must not double-free the cell, concrete box,
// or inner String (the source-local drop is suppressed, so the thunk is
// the SOLE owner).
func dynClosureCaptureUnderflowSrc(n string) string {
	return `trait Shape {
    function area(self: Self): i32;
}
struct Circle { tag: string }
impl Shape for Circle { function area(self: Self): i32 { return 1; } }
function make(seed: i32): () => i32 {
    var dc: dyn Shape = Circle { tag: "a heap string owned by a circle behind a captured dyn" };
    return function (): i32 { return dc.area(); };
}
function main(): i32 {
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < ` + n + `) {
        var f: () => i32 = make(i);
        sum = sum + f();
        i = i + 1;
    }
    return __rc_underflow_count();
}`
}

// --- x86-64 ---------------------------------------------------------------

// TestX86_64DynClosureCaptureHeapBumpBounded: the closure-capture proof on
// x86-64. A loop building + dropping an escaping closure that captures a
// String-owning Circle behind `dyn Shape` must report the SAME bump growth
// at N=5000 and N=50000 — a leak (every cell + concrete + String per
// iteration) would grow with N, and the pre-fix UAF crashed outright.
func TestX86_64DynClosureCaptureHeapBumpBounded(t *testing.T) {
	small := mustRunX86_64FreeOn(t, dynClosureCaptureSrc("5000"))
	large := mustRunX86_64FreeOn(t, dynClosureCaptureSrc("50000"))
	// Bounded high-water: the bump at 10x the iterations must NOT exceed the
	// bump at fewer (a leak grows monotonically with N — at N=50000 it would
	// be ~10x N=5000). The freelist warms in differently between the two runs
	// (fewer iterations may not recycle every block), so `large <= small`,
	// not strict equality, is the leak-free invariant.
	if large > small {
		t.Errorf("closure-captured-dyn bump growth should be bounded (reclaim): N=5000 -> %d, N=50000 -> %d (a leak would grow with N)", small, large)
	}
}

// TestX86_64DynClosureCaptureNoUnderflow: the capture reclaim must not
// over-release. __rc_underflow_count() must be 0.
func TestX86_64DynClosureCaptureNoUnderflow(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, dynClosureCaptureUnderflowSrc("200")); code != 0 {
		t.Errorf("closure-captured-dyn over-releases = %d, want 0", code)
	}
}

// --- arm64 (qemu) ---------------------------------------------------------

// TestArm64DynClosureCaptureHeapBumpBounded: the structural mirror on arm64
// — same boxed representation, same per-capture __drop_dyn_<set> reclaim in
// the closure-drop thunk.
func TestArm64DynClosureCaptureHeapBumpBounded(t *testing.T) {
	small := mustRunArm64FreeOn(t, dynClosureCaptureSrc("5000"))
	large := mustRunArm64FreeOn(t, dynClosureCaptureSrc("50000"))
	// `large <= small` is the leak-free invariant (see the x86-64 sibling).
	if large > small {
		t.Errorf("closure-captured-dyn bump growth should be bounded (reclaim): N=5000 -> %d, N=50000 -> %d (a leak would grow with N)", small, large)
	}
}

// TestArm64DynClosureCaptureNoUnderflow: no over-release on arm64.
func TestArm64DynClosureCaptureNoUnderflow(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, dynClosureCaptureUnderflowSrc("200")); code != 0 {
		t.Errorf("closure-captured-dyn over-releases = %d, want 0", code)
	}
}
