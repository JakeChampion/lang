package arm64

import (
	"strings"
	"testing"
)

// #4402 opt 2b (arm64): the dedicated rc ops (OpRcInc / OpRcDec /
// OpRcIsUnique) lower to an inline fast path — the null / SSO-tag /
// below-heap / static-sentinel guards and the refcount RMW happen in
// straight-line code at the op site, with no `bl __fern_rc_inc` /
// `bl __fern_rc_dec` call and no caller-save spill. The runtime helper
// is still DEFINED (RcFreeDebug builds call it, and string/array
// retain-copy helpers tail-call it), but a non-debug op site never
// branches to it. Behaviour parity with the call form is covered by the
// rc-balance / underflow / differential e2e suites; these pin the shape.

func TestRcIncInlinesAtSite(t *testing.T) {
	// `var b = a; return b` retains the borrowed param before transfer,
	// so g carries an rc inc.
	asm := compile(t, `@noinline function g(a: i32[]): i32[] { var b: i32[] = a; return b; }
function main(): i32 { var x: i32[] = [1, 2, 3]; var y: i32[] = g(x); return y[0]; }`, Options{})
	body := fnBody(t, asm, "g")
	if strings.Contains(body, "bl __fern_rc_inc") {
		t.Errorf("rc inc must inline, not `bl __fern_rc_inc`, in g:\n%s", body)
	}
	// The inline guard + RMW markers: the below-heap base materialise and
	// the +1 store to [ptr-8].
	for _, want := range []string{"lsl x1, x1, #28", "ldur w1, [x0, #-8]", "add w1, w1, #1", "stur w1, [x0, #-8]"} {
		if !strings.Contains(body, want) {
			t.Errorf("inline rc inc missing %q in g:\n%s", want, body)
		}
	}
	// The helper is still defined for the debug / tail-call paths.
	if !strings.Contains(asm, "__fern_rc_inc:") {
		t.Errorf("runtime helper __fern_rc_inc must still be emitted")
	}
}

func TestRcDecInlinesAtSite(t *testing.T) {
	// Copying a struct that owns a heap field (`var q = p`) retains its
	// pointer field, and the copies are dropped at scope exit — a bare
	// rc dec (distinct from the container-walking `__fern_arr_dec` a
	// top-level array drop uses).
	asm := compile(t, `struct P { xs: i32[] }
function h(p: P): i32 { var q: P = p; return q.xs[0]; }
function main(): i32 { return h(P { xs: [7, 8] }); }`, Options{})
	if strings.Contains(asm, "bl __fern_rc_dec") {
		t.Errorf("rc dec must inline, not `bl __fern_rc_dec`:\n%s", asm)
	}
	// The dec-specific inline markers: the underflow-guarded decrement
	// and the write-back to [ptr-8].
	for _, want := range []string{"ldur w1, [x0, #-8]", "sub w1, w1, #1", "stur w1, [x0, #-8]"} {
		if !strings.Contains(asm, want) {
			t.Errorf("inline rc dec missing %q:\n%s", want, asm)
		}
	}
}

// TestRcOpsFallBackToCallInLargeFn pins the opt-2b fall-back: a function
// whose IR-op count exceeds rcInlineMaxOps drops the inline sequence and
// calls the (behaviour-identical) runtime helper instead. On arm64 this is
// load-bearing — inlining the ~1.66M rc ops of the self-host compiler's
// lowering monster (irlower__lower_expr) pushes its body past aarch64's
// ±128 MB unconditional-branch reach and the epilogue `b .Lret_…` jumps
// overflow ("branch out of range"). The threshold is lowered here so a tiny
// function trips it; production keeps the 1M default.
func TestRcOpsFallBackToCallInLargeFn(t *testing.T) {
	saved := rcInlineMaxOps
	rcInlineMaxOps = 0 // any function with rc ops now exceeds the ceiling
	defer func() { rcInlineMaxOps = saved }()

	// Same retain shape as TestRcIncInlinesAtSite; with the ceiling at 0, g's
	// rc inc must lower to the `bl` call form, not the inline RMW.
	asm := compile(t, `@noinline function g(a: i32[]): i32[] { var b: i32[] = a; return b; }
function main(): i32 { var x: i32[] = [1, 2, 3]; var y: i32[] = g(x); return y[0]; }`, Options{})
	body := fnBody(t, asm, "g")
	if !strings.Contains(body, "bl __fern_rc_inc") {
		t.Errorf("over-threshold function must call the rc helper, not inline it, in g:\n%s", body)
	}
	// The inline-only marker must be absent from g's body — the below-heap
	// base materialise is unique to the inline sequence.
	if strings.Contains(body, "lsl x1, x1, #28") {
		t.Errorf("over-threshold function must not emit the inline rc guard in g:\n%s", body)
	}
	// The helper is still defined (it always is when any rc op is present).
	if !strings.Contains(asm, "__fern_rc_inc:") {
		t.Errorf("runtime helper __fern_rc_inc must still be emitted")
	}
}
