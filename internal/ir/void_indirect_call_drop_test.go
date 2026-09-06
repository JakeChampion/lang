// A void call through a function VALUE must not be followed by a drop (#8504).
//
// A discarded call statement drops its result to keep the operand stack
// balanced, and whether there IS a result comes from the callee. For a named
// function that is its FuncSigs entry; for a closure — a function-typed
// parameter, local or field — there is none, and assuming a value emitted a
// drop after a void call_indirect. On the register backends the extra pop is
// invisible; on wasm the stack underflows and the module fails validation, so
// `run(log, 4)` over `function log(x: i32): void` produced a binary wasmtime
// refused to compile.
package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

func TestVoidCallThroughFunctionValueLeavesNothingToDrop(t *testing.T) {
	for _, tc := range []struct {
		name string
		// wantDrop is whether the indirect call's result is dropped: true for
		// a callee that yields a value, false for a void one.
		wantDrop bool
		src      string
	}{
		{
			name: "void closure parameter", wantDrop: false,
			src: `
function run(f: (i32) => void, v: i32): void { f(v); }
function main(): i32 {
    var seen: i32 = 0;
    var g = (x: i32) => { seen = seen + x; };
    run(g, 4);
    return seen - 4;
}`,
		},
		{
			name: "named void function passed as a value", wantDrop: false,
			src: `
function run(f: (i32) => void, v: i32): void { f(v); }
function log(x: i32): void { }
function main(): i32 {
    run(log, 4);
    return 0;
}`,
		},
		{
			name: "void closure held in a local", wantDrop: false,
			src: `
function main(): i32 {
    var seen: i32 = 0;
    var g = (x: i32) => { seen = seen + x; };
    g(4);
    return seen - 4;
}`,
		},
		{
			// A closure literal called where it is written has no FuncSigs
			// entry under its own name either; its result type comes from
			// the hoisted signature closureconv stamped (#8551).
			name: "void closure literal called inline", wantDrop: false,
			src: `
function main(): i32 {
    var seen: i32 = 0;
    ((x: i32) => { seen = seen + x; })(4);
    return seen - 4;
}`,
		},
		{
			name: "value-returning closure literal called inline", wantDrop: true,
			src: `
function main(): i32 {
    ((x: i32) => x * 2)(4);
    return 0;
}`,
		},
		{
			// The other direction: a discarded value-yielding call through the
			// same seam still needs its drop, or the stack grows unbalanced.
			name: "value-returning closure parameter", wantDrop: true,
			src: `
function run(f: (i32) => i32, v: i32): void { f(v); }
function main(): i32 {
    run((x: i32) => x * 2, 4);
    return 0;
}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ip := lowerForTest(t, tc.src+"\n")
			found, got := dropFollowsIndirectCall(ip)
			if !found {
				t.Fatal("no indirect call in the lowered program — the shape this " +
					"case reaches it through has moved, so it no longer covers it")
			}
			if got != tc.wantDrop {
				t.Errorf("drop after the indirect call = %v, want %v", got, tc.wantDrop)
			}
		})
	}
}

// dropFollowsIndirectCall reports whether the program contains an indirect call
// and whether an OpDrop immediately follows the first one.
func dropFollowsIndirectCall(ip *ir.Program) (found, dropped bool) {
	for _, fn := range ip.Funcs {
		for i, op := range fn.Ops {
			if op.Kind != ir.OpCallIndirect && op.Kind != ir.OpCallClosureDirect {
				continue
			}
			return true, i+1 < len(fn.Ops) && fn.Ops[i+1].Kind == ir.OpDrop
		}
	}
	return false, false
}
