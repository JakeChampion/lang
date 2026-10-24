package e2e

import (
	"testing"
)

func stmtTempArrBumpSrc(n string) string {
	return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < ` + n + `) {
        [i, i + 1, i + 2];
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const stmtTempArrUnderflowSrc = `function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) {
        [i, i + 1];
        let xs: i32[] = [i, i + 1, i + 2];
        acc = acc + xs[0] + xs[1] + xs[2];
        i = i + 1;
    }
    // sum over i=0..199 of (i + (i+1) + (i+2)) = sum(3i + 3)
    //   = 3*(199*200/2) + 3*200 = 3*19900 + 600 = 60300
    if (acc != 60300) { return 999; }
    return __rc_underflow_count();
}`

const stmtTempStrUnderflowSrc = `function main(): i32 {
    let a: string = "hello";
    let b: string = "world";
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) {
        a + b;
        let s: string = a + b;
        acc = acc + s.len();
        i = i + 1;
    }
    if (acc != 2000) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64StmtTempReclaim(t *testing.T) {
	small := mustRunX86_64FreeOn(t, stmtTempArrBumpSrc("50"))
	large := mustRunX86_64FreeOn(t, stmtTempArrBumpSrc("5000"))
	if small != large {
		t.Errorf("discarded-temp bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if _, code := compileAndRunX86_64FreeOn(t, stmtTempArrUnderflowSrc); code != 0 {
		t.Errorf("discarded-array-temp reclaim: code=%d (999=value mismatch, >0=over-release)", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, stmtTempStrUnderflowSrc); code != 0 {
		t.Errorf("discarded-string-temp reclaim: code=%d", code)
	}
}

func TestArm64StmtTempReclaim(t *testing.T) {
	small := mustRunArm64FreeOn(t, stmtTempArrBumpSrc("50"))
	large := mustRunArm64FreeOn(t, stmtTempArrBumpSrc("5000"))
	if small != large {
		t.Errorf("discarded-temp bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if _, code := compileAndRunArm64FreeOn(t, stmtTempArrUnderflowSrc); code != 0 {
		t.Errorf("discarded-array-temp reclaim: code=%d", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, stmtTempStrUnderflowSrc); code != 0 {
		t.Errorf("discarded-string-temp reclaim: code=%d", code)
	}
}

func TestWASMStmtTempReclaim(t *testing.T) {
	small := runWasm(t, stmtTempArrBumpSrc("50"))
	large := runWasm(t, stmtTempArrBumpSrc("5000"))
	if small != large {
		t.Errorf("discarded-temp bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, stmtTempArrUnderflowSrc); got != 0 {
		t.Errorf("discarded-array-temp reclaim: got %d", got)
	}
	if got := runWasm(t, stmtTempStrUnderflowSrc); got != 0 {
		t.Errorf("discarded-string-temp reclaim: got %d", got)
	}
}
