package x86_64

import (
	"strings"
	"testing"
)

// #4402 opt 2b (x86-64): the dedicated rc ops (OpRcInc / OpRcDec /
// OpRcIsUnique) lower to an inline fast path — the null / SSO-tag /
// below-heap / static-sentinel guards and the refcount RMW happen in
// straight-line code at the op site, with no `call __fern_rc_inc` /
// `call __fern_rc_dec` and no caller-save spill. The runtime helper is
// still DEFINED (RcFreeDebug builds call it, and the string/array
// retain-copy helpers tail-call it), but a non-debug op site in a
// normal-sized function never branches to it. Mirrors the arm64
// backend's rc_inline_test.go.

func TestRcIncInlinesAtSite(t *testing.T) {
	// `var b = a; return b` retains the borrowed param before transfer,
	// so g carries an rc inc.
	asm := compile(t, `function g(a: i32[]): i32[] { var b: i32[] = a; return b; }
function main(): i32 { var x: i32[] = [1, 2, 3]; var y: i32[] = g(x); return y[0]; }`)
	if strings.Contains(asm, "call __fern_rc_inc") {
		t.Errorf("rc inc must inline, not `call __fern_rc_inc`:\n%s", asm)
	}
	// The inline guard + RMW markers: the below-heap base compare and the
	// +1 store to [ptr-8].
	for _, want := range []string{"cmp rax, 0x10000000", "mov ecx, dword ptr [rax - 8]", "add ecx, 1", "mov dword ptr [rax - 8], ecx"} {
		if !strings.Contains(asm, want) {
			t.Errorf("inline rc inc missing %q:\n%s", want, asm)
		}
	}
	// The helper is still defined for the debug / tail-call / large-fn paths.
	if !strings.Contains(asm, "__fern_rc_inc:") {
		t.Errorf("runtime helper __fern_rc_inc must still be emitted")
	}
}

// TestRcOpsFallBackToCallInLargeFn pins the opt-2b fall-back: a function
// whose IR-op count exceeds rcInlineMaxOps drops the inline sequence and
// calls the (behaviour-identical) runtime helper instead, so the emitted
// `.s` for the self-host compiler's ~9.75M-op lowering monster
// (irlower__lower_expr) stays assemblable without ballooning `as`'s RSS.
// The threshold is lowered here so a tiny function trips it — production
// keeps the 1M default.
func TestRcOpsFallBackToCallInLargeFn(t *testing.T) {
	saved := rcInlineMaxOps
	rcInlineMaxOps = 0 // any function with rc ops now exceeds the ceiling
	defer func() { rcInlineMaxOps = saved }()

	// Same program as the inline test; with the ceiling at 0, g's rc inc
	// must lower to the call form, not the inline RMW.
	asm := compile(t, `function g(a: i32[]): i32[] { var b: i32[] = a; return b; }
function main(): i32 { var x: i32[] = [1, 2, 3]; var y: i32[] = g(x); return y[0]; }`)
	if !strings.Contains(asm, "call __fern_rc_inc") {
		t.Errorf("over-threshold function must call the rc helper, not inline it:\n%s", asm)
	}
	// The inline-only marker must be absent from the rc op site — the
	// below-heap base compare is unique to the inline sequence.
	if strings.Contains(asm, "cmp rax, 0x10000000") {
		t.Errorf("over-threshold function must not emit the inline rc guard:\n%s", asm)
	}
	// The helper is still defined (it always is when any rc op is present).
	if !strings.Contains(asm, "__fern_rc_inc:") {
		t.Errorf("runtime helper __fern_rc_inc must still be emitted")
	}
}
