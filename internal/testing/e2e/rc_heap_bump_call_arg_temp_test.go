package e2e

import (
	"testing"
)

const callArgTempProlog = `function sum3(xs: i32[]): i32 { return xs[0] + xs[1] + xs[2]; }
function slen(s: string): i32 { return s.len(); }
`

func callArgTempBumpSrc(n string) string {
	return callArgTempProlog + `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) {
        acc = acc + sum3([i, i + 1, i + 2]);
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const callArgTempArrUnderflowSrc = callArgTempProlog + `function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) {
        acc = acc + sum3([i, i + 1, i + 2]);
        i = i + 1;
    }
    // sum over i=0..199 of (i + (i+1) + (i+2)) = 3*19900 + 600 = 60300
    if (acc != 60300) { return 999; }
    return __rc_underflow_count();
}`

const callArgTempStrUnderflowSrc = callArgTempProlog + `function main(): i32 {
    let a: string = "hello";
    let b: string = "world";
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) {
        acc = acc + slen(a + b);
        i = i + 1;
    }
    if (acc != 2000) { return 999; }
    return __rc_underflow_count();
}`

func callArgTempStrBumpSrc(n string) string {
	return callArgTempProlog + `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let a: string = "hello there friend";
    let b: string = "general kenobi!!!";
    let i: i32 = 0;
    let acc: i32 = 0;
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
	small := runWasm(t, callArgTempBumpSrc("50"))
	large := runWasm(t, callArgTempBumpSrc("5000"))
	if small != large {
		t.Errorf("call-arg-temp bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
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
