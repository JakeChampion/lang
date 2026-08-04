package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Statement-temporary reclamation, stage (a): a discarded bare-ExprStmt
// whose value is a FRESH owned rc temporary (a literal / string concat /
// string slice) is now DEC'd at the statement boundary instead of being
// OpDrop'd on the floor. Before this slice nothing reclaimed it — a bare
// `[i, i + 1];` or `a + b;` in a loop leaked its box every iteration
// (unbounded), since emitVarReinitDropOld only sees DECLARED vars.
// See docs/RC-PERCEUS-PLAN.md "Statement-temporary reclamation".
//
// The probe discards an array literal (arrays heap-allocate on every
// backend, so the bump high-water is non-zero and meaningfully bounded on
// x86_64 / arm64 / wasm alike). With the dec in place each iteration's box
// returns to the freelist, so N=50 and N=5000 report the SAME growth.

func stmtTempArrBumpSrc(n string) string {
	return `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) {
        [i, i + 1, i + 2];
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// Value-correct + no over-release: a discarded owned array temp must
// reclaim its OWN box without touching the live `xs` built from the same
// loop-variable operands — a wrong "owned" verdict that freed a shared
// buffer would over-release (>0) or corrupt the sum (999).
const stmtTempArrUnderflowSrc = `function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        [i, i + 1];
        var xs: i32[] = [i, i + 1, i + 2];
        acc = acc + xs[0] + xs[1] + xs[2];
        i = i + 1;
    }
    // sum over i=0..199 of (i + (i+1) + (i+2)) = sum(3i + 3)
    //   = 3*(199*200/2) + 3*200 = 3*19900 + 600 = 60300
    if (acc != 60300) { return 999; }
    return __rc_underflow_count();
}`

// String-concat discard (wasm two-word heap path is the meaningful one;
// natives keep short concats SSO-inline so str_dec no-ops). The discarded
// `a + b;` must reclaim its buffer without over-releasing `a` / `b`, which
// are reused in the bound `s` concat that drives `acc`.
const stmtTempStrUnderflowSrc = `function main(): i32 {
    var a: string = "hello";
    var b: string = "world";
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        a + b;
        var s: string = a + b;
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
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
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
