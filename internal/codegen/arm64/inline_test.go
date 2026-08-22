package arm64

import (
	"strings"
	"testing"
)

// ir.Inline on the native backends (#4377 slice 2), the x86-64 backend's twin —
// see its inline_test.go for what each case is guarding.

func TestIRInlineSubstitutesSmallCallee(t *testing.T) {
	asm := compile(t, `function dbl(x: i32): i32 { return x * 2; }
function main(): i32 { return dbl(7); }`, Options{})
	if strings.Contains(asm, "bl "+AsmFnName("dbl")) {
		t.Errorf("call to an inlineable callee survived:\n%s", asm)
	}
	if strings.Contains(asm, AsmFnName("dbl")+":") {
		t.Errorf("callee body was emitted after its only call site was inlined:\n%s", asm)
	}
}

func TestIRInlineHonoursNoinline(t *testing.T) {
	asm := compile(t, `@noinline function dbl(x: i32): i32 { return x * 2; }
function main(): i32 { return dbl(7); }`, Options{})
	if !strings.Contains(asm, "bl "+AsmFnName("dbl")) {
		t.Errorf("@noinline callee was inlined anyway:\n%s", asm)
	}
}
