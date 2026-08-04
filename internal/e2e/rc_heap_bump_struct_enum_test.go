package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Struct / enum loop-body deep reclamation (RC-Perceus). Before this
// slice emitVarReinitDropOld flat-dec'd a struct / enum loop var on
// re-declaration — which neither frees the box (rc_dec has no free path)
// nor recurses into rc-tracked fields / payloads. So a `var b = Box{
// data: [...] }` or `var e = Arr([...])` re-declared in a loop leaked
// its box AND its nested heap field every iteration but the last. The
// fix routes the reinit drop through the generated __drop_struct_<N> /
// __drop_enum_<N> fn (is_unique-gated deep drop + box_free), matching
// the exit sweep, so the bump high-water stays FLAT regardless of N.

func structFieldBumpSrc(n string) string {
	return `struct Box { data: i32[], tag: i32 }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < ` + n + `) {
        var b: Box = Box { data: [i, i + 1, i + 2], tag: i };
        sum = sum + b.data[0] + b.tag;
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func enumPayloadBumpSrc(n string) string {
	return `enum E { Arr(i32[]), Empty }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < ` + n + `) {
        var e: E = Arr([i, i + 1, i + 2]);
        match (e) {
            Arr(xs) => { sum = sum + xs[0]; },
            Empty => { sum = sum + 1; },
        }
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// structEnumUnderflowSrc exercises both shapes across many iterations and
// returns the over-release counter (0 expected) after a value check —
// guards against a double-free of a box / nested buffer.
const structEnumUnderflowSrc = `struct Box { data: i32[], tag: i32 }
enum E { Arr(i32[]), Empty }
function main(): i32 {
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < 200) {
        var b: Box = Box { data: [i, i + 1, i + 2], tag: i };
        var e: E = Arr([i, i + 5]);
        match (e) {
            Arr(xs) => { sum = sum + b.data[0] + b.tag + xs[1]; },
            Empty => { sum = sum + 1; },
        }
        i = i + 1;
    }
    if (sum != 60700) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64StructEnumHeapBumpBounded(t *testing.T) {
	for _, src := range []func(string) string{structFieldBumpSrc, enumPayloadBumpSrc} {
		small := mustRunX86_64FreeOn(t, src("50"))
		large := mustRunX86_64FreeOn(t, src("5000"))
		if small != large {
			t.Errorf("bump growth should be bounded (deep reclaim): N=50 -> %d, N=5000 -> %d", small, large)
		}
		if small == 0 {
			t.Errorf("expected a non-zero bounded high-water, got 0")
		}
	}
	if _, code := compileAndRunX86_64FreeOn(t, structEnumUnderflowSrc); code != 0 {
		t.Errorf("struct/enum reinit over-releases or value mismatch: code=%d (999=value, >0=over-release)", code)
	}
}

func TestArm64StructEnumHeapBumpBounded(t *testing.T) {
	for _, src := range []func(string) string{structFieldBumpSrc, enumPayloadBumpSrc} {
		small := mustRunArm64FreeOn(t, src("50"))
		large := mustRunArm64FreeOn(t, src("5000"))
		if small != large {
			t.Errorf("bump growth should be bounded (deep reclaim): N=50 -> %d, N=5000 -> %d", small, large)
		}
		if small == 0 {
			t.Errorf("expected a non-zero bounded high-water, got 0")
		}
	}
	if _, code := compileAndRunArm64FreeOn(t, structEnumUnderflowSrc); code != 0 {
		t.Errorf("struct/enum reinit over-releases or value mismatch: code=%d", code)
	}
}

func TestWASMStructEnumHeapBumpBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	for _, src := range []func(string) string{structFieldBumpSrc, enumPayloadBumpSrc} {
		small := runWasm(t, src("50"))
		large := runWasm(t, src("5000"))
		if small != large {
			t.Errorf("bump growth should be bounded (deep reclaim): N=50 -> %d, N=5000 -> %d", small, large)
		}
		if small == 0 {
			t.Errorf("expected a non-zero bounded high-water, got 0")
		}
	}
	if got := runWasm(t, structEnumUnderflowSrc); got != 0 {
		t.Errorf("struct/enum reinit over-releases or value mismatch: got %d", got)
	}
}
