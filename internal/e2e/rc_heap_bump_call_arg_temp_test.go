package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Statement-temporary reclamation, stage (b): a FRESH owned rc temporary
// passed as a BORROWED arg to a normal direct call (`foo(a + b)`) is now
// stashed and DEC'd after the call. Before this slice nothing reclaimed it
// — the callee borrows the arg (no callee-side dec under the Phase-2d borrow
// model) and the caller dropped the operand on the floor, so a
// `sum3([i, i+1, i+2])` or `slen(a + b)` in a loop leaked its box every
// iteration (unbounded). See docs/RC-PERCEUS-PLAN.md "Statement-temporary
// reclamation" — this is the dominant real-code source ("the real win").
//
// Safety mirror: dec'ing the inline arg is exactly the shipped exit-sweep
// dec of `var t = <shape>; foo(t)` (computeFreeEligible marks the bound temp
// freeEligible). Retain-sink callees (Map_set / Array_push MOVE a fresh arg
// into a container) are excluded — their bound-equivalent is escape-tainted.
//
// The bump probe passes an array literal (heap-allocated on every backend,
// so the high-water is non-zero and meaningfully bounded on x86_64 / arm64 /
// wasm). With the dec each iteration's box returns to the freelist, so N=50
// and N=5000 report the SAME growth.

const callArgTempProlog = `function sum3(xs: i32[]): i32 { return xs[0] + xs[1] + xs[2]; }
function slen(s: string): i32 { return s.len(); }
`

func callArgTempBumpSrc(n string) string {
	return callArgTempProlog + `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) {
        acc = acc + sum3([i, i + 1, i + 2]);
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// Value-correct + no over-release: the borrowed array-literal arg must be
// read correctly by the callee and then reclaimed without over-releasing
// (the call's result drives `acc`, so an early free / corruption shows up
// as a wrong sum (999) or a non-zero underflow count).
const callArgTempArrUnderflowSrc = callArgTempProlog + `function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        acc = acc + sum3([i, i + 1, i + 2]);
        i = i + 1;
    }
    // sum over i=0..199 of (i + (i+1) + (i+2)) = 3*19900 + 600 = 60300
    if (acc != 60300) { return 999; }
    return __rc_underflow_count();
}`

// String-concat arg (wasm two-word heap path is the meaningful one; natives
// keep short concats SSO-inline). The borrowed `a + b` must be reclaimed
// after the call without disturbing `a` / `b`, reused every iteration.
const callArgTempStrUnderflowSrc = callArgTempProlog + `function main(): i32 {
    var a: string = "hello";
    var b: string = "world";
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        acc = acc + slen(a + b);
        i = i + 1;
    }
    if (acc != 2000) { return 999; }
    return __rc_underflow_count();
}`

func callArgTempStrBumpSrc(n string) string {
	return callArgTempProlog + `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var a: string = "hello there friend";
    var b: string = "general kenobi!!!";
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) {
        acc = acc + slen(a + b);
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func TestX86_64CallArgTempReclaim(t *testing.T) {
	small := mustRunX86_64FreeOn(t, callArgTempBumpSrc("50"))
	large := mustRunX86_64FreeOn(t, callArgTempBumpSrc("5000"))
	if small != large {
		t.Errorf("call-arg-temp bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if _, code := compileAndRunX86_64FreeOn(t, callArgTempArrUnderflowSrc); code != 0 {
		t.Errorf("call-arg array-temp reclaim: code=%d (999=value mismatch, >0=over-release)", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, callArgTempStrUnderflowSrc); code != 0 {
		t.Errorf("call-arg string-temp reclaim: code=%d", code)
	}
}

func TestArm64CallArgTempReclaim(t *testing.T) {
	small := mustRunArm64FreeOn(t, callArgTempBumpSrc("50"))
	large := mustRunArm64FreeOn(t, callArgTempBumpSrc("5000"))
	if small != large {
		t.Errorf("call-arg-temp bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if _, code := compileAndRunArm64FreeOn(t, callArgTempArrUnderflowSrc); code != 0 {
		t.Errorf("call-arg array-temp reclaim: code=%d", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, callArgTempStrUnderflowSrc); code != 0 {
		t.Errorf("call-arg string-temp reclaim: code=%d", code)
	}
}

func TestWASMCallArgTempReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, callArgTempBumpSrc("50"))
	large := runWasm(t, callArgTempBumpSrc("5000"))
	if small != large {
		t.Errorf("call-arg-temp bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	// String-concat arg: wasm two-word strings always heap-allocate, so this
	// is the backend where the string-arg dec path matters most. Long
	// operands defeat SSO so the concat is a real heap buffer. Compare two
	// counts PAST the freelist warmup plateau (matching the shipped
	// rc_heap_bump_string_test), so a bounded reuse reads small == large.
	sSmall := runWasm(t, callArgTempStrBumpSrc("5000"))
	sLarge := runWasm(t, callArgTempStrBumpSrc("50000"))
	if sSmall != sLarge {
		t.Errorf("call-arg string-temp bump should be bounded: N=5000 -> %d, N=50000 -> %d", sSmall, sLarge)
	}
	if sSmall == 0 {
		t.Errorf("wasm two-word strings heap-allocate; expected non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, callArgTempArrUnderflowSrc); got != 0 {
		t.Errorf("call-arg array-temp reclaim: got %d", got)
	}
	if got := runWasm(t, callArgTempStrUnderflowSrc); got != 0 {
		t.Errorf("call-arg string-temp reclaim: got %d", got)
	}
}
