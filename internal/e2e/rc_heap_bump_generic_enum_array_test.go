package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Generic-enum-array (`Option[T][]` / `Result[T,E][]`) element reclamation
// (RC-Perceus) — regression guard. The concrete `E[]` slice left generic
// instantiations like `Option[i32[]][]` on the flat __fern_drop_arr_ptr,
// leaking each Some's heap payload (2464 B → 240064 B); arrElemStructDropName
// now routes a generic enum element through dropFnNameFor (which substitutes
// the type args, registers the substituted decl, and returns the per-element
// __drop_enum_<mangled>) into the array deep-drop loop. This was already
// shipped on main but lacked an explicit test — these pin the bounded
// high-water + value-correctness + 0-over-release contract (impl-agnostic),
// including the adversarial shared-payload UAF shape.

func genEnumArrBumpSrc(n string) string {
	return `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) {
        var xs: Option[i32[]][] = [Some([i, i + 1, i + 2]), None, Some([i + 3])];
        match (xs[0]) {
            Some(a) => { acc = acc + a[0]; },
            None => {},
        }
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// Shares a payload with a live local `a` (adversarial double-free / UAF
// shape) and uses it after building the array — 0 iff value-correct AND no
// over-release.
const genEnumArrUnderflowSrc = `function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        var a: i32[] = [i, i + 1, i + 2];
        var xs: Option[i32[]][] = [Some(a), None, Some([i + 5])];
        acc = acc + a[0] + a[2];
        match (xs[2]) { Some(b) => { acc = acc + b[0]; }, None => {} }
        i = i + 1;
    }
    // per iter: i + (i+2) + (i+5) = 3i+7; sum i=0..199 = 3*19900 + 1400 = 61100
    if (acc != 61100) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64GenericEnumArrayReclaim(t *testing.T) {
	small := mustRunX86_64FreeOn(t, genEnumArrBumpSrc("50"))
	large := mustRunX86_64FreeOn(t, genEnumArrBumpSrc("5000"))
	if small != large {
		t.Errorf("Option[T][] bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if _, code := compileAndRunX86_64FreeOn(t, genEnumArrUnderflowSrc); code != 0 {
		t.Errorf("Option[T][] reclaim: code=%d (999=value/UAF, >0=over-release)", code)
	}
}

func TestArm64GenericEnumArrayReclaim(t *testing.T) {
	small := mustRunArm64FreeOn(t, genEnumArrBumpSrc("50"))
	large := mustRunArm64FreeOn(t, genEnumArrBumpSrc("5000"))
	if small != large {
		t.Errorf("Option[T][] bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if _, code := compileAndRunArm64FreeOn(t, genEnumArrUnderflowSrc); code != 0 {
		t.Errorf("Option[T][] reclaim: code=%d", code)
	}
}

func TestWASMGenericEnumArrayReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, genEnumArrBumpSrc("50"))
	large := runWasm(t, genEnumArrBumpSrc("5000"))
	if small != large {
		t.Errorf("Option[T][] bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("wasm heap-allocates; expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, genEnumArrUnderflowSrc); got != 0 {
		t.Errorf("Option[T][] reclaim: got %d", got)
	}
}
