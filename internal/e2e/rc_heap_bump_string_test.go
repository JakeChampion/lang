package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// String-concat loop-var bounded-growth guard (RC-Perceus). The shipped
// string-reclaim tests (rc_freelist_test) only assert 0 over-release —
// they never checked that a string-concat build-and-discard loop holds a
// BOUNDED bump high-water. This pins that: a `var s = a + b` re-declared
// in a loop reclaims each iteration's buffer (emitVarReinitDropOld's
// str_dec → __fern_box_free at rc==1, freeing the `len`-byte payload back
// to the freelist), so N=5000 and N=50000 report the SAME bump growth.
//
// Backend note: native single-word strings (x86_64 / arm64) keep a short
// concat result INLINE (SSO), so no heap buffer is allocated and the
// probe reads 0 — trivially bounded. wasm's two-word strings always
// heap-allocate, so the probe plateaus at a non-zero bounded high-water
// once the freelist warms up. Both satisfy small == large.
//
// (The companion fix in this slice corrects __fern_str_dec's rc==1 free
// to release the actual `len`-byte payload instead of an uninitialised
// header word read from `data-4` — __fern_alloc_rc1 writes only rc at
// base+0, so `data-4` held garbage that misrouted the freelist class.)

func stringConcatBumpSrc(n string) string {
	return `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var a: string = "hello";
    var b: string = "world";
    var i: i32 = 0;
    while (i < ` + n + `) {
        var s: string = a + b;
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// Value-correct + no over-release across many concat iterations.
const stringConcatUnderflowSrc = `function main(): i32 {
    var a: string = "ab";
    var b: string = "cd";
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        var s: string = a + b;
        acc = acc + s.len();
        i = i + 1;
    }
    if (acc != 800) { return 999; }
    return __rc_underflow_count();
}`

// longStringReinitBumpSrc builds a HEAP string (>15 B, so no SSO on any
// backend) into a loop-body `var s` each iteration. This pins the slice-5g
// follow-up: arm64 two-word heap strings now reclaim on loop-var REINIT
// (emitOwnedSlotDrop's str_dec, mirroring the exit sweep). A safe-leak here
// makes N=5000 and N=50000 diverge; all three backends must report the SAME
// bump high-water.
func longStringReinitBumpSrc(n string) string {
	return `function tag(i: i32): string {
    if (i % 2 == 0) { return "even"; }
    return "odd";
}
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) {
        var s: string = "a-fairly-long-heap-string-prefix-" + tag(i);
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// Value-correct + no over-release for the long-heap-string reinit loop.
const longStringReinitUnderflowSrc = `function tag(i: i32): string {
    if (i % 2 == 0) { return "even"; }
    return "odd";
}
function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        var s: string = "a-fairly-long-heap-string-prefix-" + tag(i);
        acc = acc + s.len();
        i = i + 1;
    }
    // 33-char prefix + "even"(4)/"odd"(3): even i -> 37, odd i -> 36.
    // i=0..199: 100 even (37) + 100 odd (36) = 3700 + 3600 = 7300.
    if (acc != 7300) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64LongStringReinitBounded(t *testing.T) {
	// x86_64's small heap allocations come from the segregated freelist arena,
	// which __heap_bump_bytes() does NOT measure (same caveat as closure[]), so
	// the bump probe is unreliable here — assert only value + no over-release.
	// The bump-bound win is pinned on arm64 (where my fix landed) and wasm.
	if _, code := compileAndRunX86_64FreeOn(t, longStringReinitUnderflowSrc); code != 0 {
		t.Errorf("long-string reinit reclaim: code=%d", code)
	}
}

func TestArm64LongStringReinitBounded(t *testing.T) {
	small := mustRunArm64FreeOn(t, longStringReinitBumpSrc("5000"))
	large := mustRunArm64FreeOn(t, longStringReinitBumpSrc("50000"))
	if small != large {
		t.Errorf("arm64 long-string reinit bump should be bounded (slice 5g follow-up): N=5000 -> %d, N=50000 -> %d", small, large)
	}
	if _, code := compileAndRunArm64FreeOn(t, longStringReinitUnderflowSrc); code != 0 {
		t.Errorf("arm64 long-string reinit reclaim: code=%d", code)
	}
}

func TestWASMLongStringReinitBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, longStringReinitBumpSrc("5000"))
	large := runWasm(t, longStringReinitBumpSrc("50000"))
	if small != large {
		t.Errorf("long-string reinit bump should be bounded: N=5000 -> %d, N=50000 -> %d", small, large)
	}
	if got := runWasm(t, longStringReinitUnderflowSrc); got != 0 {
		t.Errorf("long-string reinit reclaim: got %d", got)
	}
}

func TestX86_64StringConcatBounded(t *testing.T) {
	small := mustRunX86_64FreeOn(t, stringConcatBumpSrc("5000"))
	large := mustRunX86_64FreeOn(t, stringConcatBumpSrc("50000"))
	if small != large {
		t.Errorf("string-concat bump should be bounded: N=5000 -> %d, N=50000 -> %d", small, large)
	}
	if _, code := compileAndRunX86_64FreeOn(t, stringConcatUnderflowSrc); code != 0 {
		t.Errorf("string-concat reclaim: code=%d (999=value mismatch, >0=over-release)", code)
	}
}

func TestArm64StringConcatBounded(t *testing.T) {
	small := mustRunArm64FreeOn(t, stringConcatBumpSrc("5000"))
	large := mustRunArm64FreeOn(t, stringConcatBumpSrc("50000"))
	if small != large {
		t.Errorf("string-concat bump should be bounded: N=5000 -> %d, N=50000 -> %d", small, large)
	}
	if _, code := compileAndRunArm64FreeOn(t, stringConcatUnderflowSrc); code != 0 {
		t.Errorf("string-concat reclaim: code=%d", code)
	}
}

func TestWASMStringConcatBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, stringConcatBumpSrc("5000"))
	large := runWasm(t, stringConcatBumpSrc("50000"))
	if small != large {
		t.Errorf("string-concat bump should be bounded: N=5000 -> %d, N=50000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("wasm two-word strings heap-allocate; expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, stringConcatUnderflowSrc); got != 0 {
		t.Errorf("string-concat reclaim: got %d", got)
	}
}
