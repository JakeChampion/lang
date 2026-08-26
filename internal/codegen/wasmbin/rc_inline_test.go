package wasmbin

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
)

// #4402 opt 2b (wasm): the dedicated rc ops lower to an inline fast path
// rather than a call to __fern_rc_inc / _dec / _is_unique, and fall back to
// the helper call above the per-function op ceiling.
//
// Assertions stay spelling-independent, as everywhere in this package: the
// inline form is pinned by the code it adds (a module carrying rc traffic is
// strictly bigger inline than as calls) and by the gate function itself, not
// by matching emitted opcodes. Behaviour equivalence between the two forms is
// the e2e suite's job — internal/e2e's WASM rc / reuse / leak tests run both
// (the helper stays emitted and other runtime bodies still call it).

const rcTrafficSrc = `struct Holder { n: i32, items: i32[] }
function churn(k: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < k) {
        var a: Holder = Holder { n: i, items: [i, i + 1] };
        var b: Holder = a;
        acc = acc + b.n + b.items[0];
        i = i + 1;
    }
    return acc;
}
function main(): i32 { return churn(4); }`

func TestWasmRcOpsInlineAddsCode(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	inlined := mustBuild(t, rcTrafficSrc, BuildOptions{})

	saved := rcInlineMaxOps
	rcInlineMaxOps = 0 // every function with rc ops now exceeds the ceiling
	called := mustBuild(t, rcTrafficSrc, BuildOptions{})
	rcInlineMaxOps = saved

	if len(inlined) <= len(called) {
		t.Errorf("inline rc ops should emit more code than the call form: inline=%d, calls=%d", len(inlined), len(called))
	}
	// The fallback must still be a valid module — the helper is emitted
	// either way, so nothing is left dangling by declining to inline.
	if !hasWasmMagic(called) {
		t.Error("call-form module missing the wasm preamble")
	}
}

// The ceiling gate and the local-vector reservation must agree: emitBody reads
// three scratch locals that localValtypes only appends when fnInlinesRcOps
// says so, and a disagreement is a malformed local vector rather than a
// wrong answer.
func TestWasmFnInlinesRcOpsRespectsCeiling(t *testing.T) {
	fn := &ir.Func{Name: "f", Ops: []ir.Op{
		{Kind: ir.OpLoadLocal},
		{Kind: ir.OpRcInc, Str: "__fern_rc_inc", I32: 1},
	}}
	if !fnInlinesRcOps(fn) {
		t.Error("a small function with an rc op should inline")
	}
	saved := rcInlineMaxOps
	rcInlineMaxOps = 1
	defer func() { rcInlineMaxOps = saved }()
	if fnInlinesRcOps(fn) {
		t.Error("a function over the op ceiling must fall back to the helper call")
	}
}

// A function with no rc ops reserves no rc scratch, whatever its size.
func TestWasmFnInlinesRcOpsSkipsRcFreeFunctions(t *testing.T) {
	fn := &ir.Func{Name: "f", Ops: []ir.Op{{Kind: ir.OpLoadLocal}, {Kind: ir.OpReturn}}}
	if fnInlinesRcOps(fn) {
		t.Error("a function without rc ops must not reserve rc scratch")
	}
}
