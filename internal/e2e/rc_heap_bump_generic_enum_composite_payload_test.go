package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Composite-of-ParamType generic-enum payload reclamation (#2704 class 2
// residual). A payload like `W(T[])` slipped past enumVariantDropPlan's
// top-level-ParamType bail and was classified from the still-generic
// shape, so a `Wrap[string]`'s drop glue freed the box and the OUTER
// buffer only (`__fern_arr_dec`) and leaked every element string —
// pre-fix this bump grows ~32 B/iteration. substituteEnumDecl now
// substitutes ParamTypes recursively (substituteTypeParamsDeep), so the
// plan sees `string[]` and routes the payload through the element-aware
// `__fern_drop_arr_str`. These pin the bounded high-water +
// value-correctness + 0-over-release contract on all three backends.
//
// The payload is a FRESH array literal (moved into the box, sole owner
// rc==1) so the glue's unique path actually reaches the elements. A
// NAMED local stored as a payload is a different, pre-existing
// conservatism: the construction inc makes the box co-own it, but
// computeFreeEligible's escape taint keeps the local from ever freeing
// its own reference (a safe leak, deliberately over-conservative) —
// that shape can't observe this fix.

func genEnumCompositeBumpSrc(n string) string {
	return `enum Wrap[T] { W(T[]), Empty }
function main(): i32 {
    var before: i32 = __heap_bump_bytes();
    var i: i32 = 0;
    var acc: i32 = 0;
    var stem: string = "alpha";
    while (i < ` + n + `) {
        var w: Wrap[string] = W([stem + "x", stem + "yy", stem + "zzz"]);
        match (w) {
            W(xs) => { acc = acc + xs.len(); },
            Empty => {},
        }
        i = i + 1;
    }
    if (acc < 0) { return acc; }
    return __heap_bump_bytes() - before;
}`
}

// Shares the payload array with a live local `a` (adversarial double-free /
// UAF shape) and uses it after the enum is built — 0 iff value-correct AND
// no over-release. This shape leaks by design (escape taint), so only the
// value + underflow contracts are asserted, not the bump.
const genEnumCompositeUnderflowSrc = `enum Wrap[T] { W(T[]), Empty }
function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        var stem: string = "s";
        var a: string[] = [stem + "x", stem + "yy", stem + "zzz"];
        var w: Wrap[string] = W(a);
        acc = acc + a[0].len() + a[2].len();
        match (w) { W(xs) => { acc = acc + xs[1].len(); }, Empty => {} }
        i = i + 1;
    }
    // per iter: len(sx)+len(szzz)+len(syy) = 2+4+3 = 9; sum over 200 = 1800
    if (acc != 1800) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64GenericEnumCompositePayloadReclaim(t *testing.T) {
	small := mustRunX86_64FreeOn(t, genEnumCompositeBumpSrc("50"))
	large := mustRunX86_64FreeOn(t, genEnumCompositeBumpSrc("5000"))
	if small != large {
		t.Errorf("Wrap[string] bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if _, code := compileAndRunX86_64FreeOn(t, genEnumCompositeUnderflowSrc); code != 0 {
		t.Errorf("Wrap[string] reclaim: code=%d (999=value/UAF, >0=over-release)", code)
	}
}

func TestArm64GenericEnumCompositePayloadReclaim(t *testing.T) {
	small := mustRunArm64FreeOn(t, genEnumCompositeBumpSrc("50"))
	large := mustRunArm64FreeOn(t, genEnumCompositeBumpSrc("5000"))
	if small != large {
		t.Errorf("Wrap[string] bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if _, code := compileAndRunArm64FreeOn(t, genEnumCompositeUnderflowSrc); code != 0 {
		t.Errorf("Wrap[string] reclaim: code=%d", code)
	}
}

func TestWASMGenericEnumCompositePayloadReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	// The wasm allocator's high-water stabilises late (~a page), so
	// compare N=5000 vs N=50000 like the field-of-fresh wasm leg — the
	// pre-fix leak (~96 B/iter) still separates them by megabytes.
	small := runWasm(t, genEnumCompositeBumpSrc("5000"))
	large := runWasm(t, genEnumCompositeBumpSrc("50000"))
	if small != large {
		t.Errorf("Wrap[string] bump should be bounded: N=5000 -> %d, N=50000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("wasm heap-allocates; expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, genEnumCompositeUnderflowSrc); got != 0 {
		t.Errorf("Wrap[string] reclaim: got %d", got)
	}
}
