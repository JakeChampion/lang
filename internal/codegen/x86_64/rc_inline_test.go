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

// isUniqueBranchSrc drops a struct holding an array at scope exit, which
// lowers to an `is_unique`-gated free: `OpRcIsUnique; OpIf`.
const isUniqueBranchSrc = `struct Holder { n: i32, items: i32[] }
function mk(k: i32): Holder { return Holder{ n: k, items: [k, k + 1] }; }
function main(): i32 { var h: Holder = mk(3); return h.items[1]; }`

// TestRcIsUniqueFusesWithBranch pins the fused form of an inline is_unique
// whose result the next op branches on: the guard's two compares jump
// straight to the else-label, with no 0/1 materialisation and no sentinel
// sign test (a negative count is never 1).
func TestRcIsUniqueFusesWithBranch(t *testing.T) {
	asm := compile(t, isUniqueBranchSrc)
	for _, want := range []string{"cmp rax, 0x10000", "cmp dword ptr [rax - 8], 1", "jne .LifElse_"} {
		if !strings.Contains(asm, want) {
			t.Errorf("fused is_unique branch missing %q:\n%s", want, asm)
		}
	}
	for _, bad := range []string{"sete cl", "call __fern_rc_is_unique", "test edx, edx"} {
		if strings.Contains(asm, bad) {
			t.Errorf("fused is_unique branch must not emit %q:\n%s", bad, asm)
		}
	}
	// The `jb` guard and the `jne` land on the same else-label: a
	// below-bound pointer and a shared one take the same arm.
	jb := strings.Index(asm, "jb .LifElse_")
	if jb < 0 {
		t.Fatalf("fused is_unique branch must jump below-bound to the else-label:\n%s", asm)
	}
	elseL := asm[jb+len("jb "):]
	elseL = elseL[:strings.IndexByte(elseL, '\n')]
	if !strings.Contains(asm, "jne "+elseL+"\n") {
		t.Errorf("jb and jne must share the else-label %s:\n%s", elseL, asm)
	}
}

// TestRcIsUniqueKeepsBoolFormWhenStored pins the fallback: an is_unique
// whose result is stored (the reuse token, read again later to gate the
// old-field release) still materialises the 0/1 value — the fusion fires
// only when the very next op is the branch.
func TestRcIsUniqueKeepsBoolFormWhenStored(t *testing.T) {
	asm := compile(t, `struct Holder { n: i32, items: i32[] }
function main(): i32 {
  var h: Holder = Holder{ n: 0, items: [1, 2] };
  var i: i32 = 0;
  while (i < 3) { h = Holder{ n: h.n + 1, items: h.items }; i = i + 1; }
  return h.n;
}`)
	for _, want := range []string{"sete cl", "test edx, edx", "cmp dword ptr [rax - 8], 1"} {
		if !strings.Contains(asm, want) {
			t.Errorf("stored is_unique must keep the bool form (and the exit drop its fused form); missing %q:\n%s", want, asm)
		}
	}
}

// TestRcIsUniqueFallsBackToCallInLargeFn: over the inline ceiling the
// fusion must not fire either — the helper call is the whole guard.
func TestRcIsUniqueFallsBackToCallInLargeFn(t *testing.T) {
	saved := rcInlineMaxOps
	rcInlineMaxOps = 0
	defer func() { rcInlineMaxOps = saved }()
	asm := compile(t, isUniqueBranchSrc)
	if !strings.Contains(asm, "call __fern_rc_is_unique") {
		t.Errorf("over-threshold function must call the is_unique helper:\n%s", asm)
	}
	if strings.Contains(asm, "cmp dword ptr [rax - 8], 1") {
		t.Errorf("over-threshold function must not emit the fused guard:\n%s", asm)
	}
}
