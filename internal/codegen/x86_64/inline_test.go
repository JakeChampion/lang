package x86_64

import (
	"strings"
	"testing"
)

// ir.Inline on the native backends (#4377 slice 2). The pass itself is covered
// in internal/ir; these pin that this backend RUNS it, and that the dead-
// function cull it depends on still takes the emptied callee away.

// The sole call site of a small helper is substituted, so neither the call nor
// the callee's own body reaches the assembly.
func TestIRInlineSubstitutesSmallCallee(t *testing.T) {
	asm := compile(t, `function dbl(x: i32): i32 { return x * 2; }
function main(): i32 { return dbl(7); }`)
	if strings.Contains(asm, "call "+AsmFnName("dbl")) {
		t.Errorf("call to an inlineable callee survived:\n%s", asm)
	}
	if strings.Contains(asm, AsmFnName("dbl")+":") {
		t.Errorf("callee body was emitted after its only call site was inlined:\n%s", asm)
	}
}

// @noinline is absolute, and it is the control for the case above: the same
// shape keeps its call, so a backend that stopped running the pass entirely
// cannot pass both.
func TestIRInlineHonoursNoinline(t *testing.T) {
	asm := compile(t, `@noinline function dbl(x: i32): i32 { return x * 2; }
function main(): i32 { return dbl(7); }`)
	if !strings.Contains(asm, "call "+AsmFnName("dbl")) {
		t.Errorf("@noinline callee was inlined anyway:\n%s", asm)
	}
}
