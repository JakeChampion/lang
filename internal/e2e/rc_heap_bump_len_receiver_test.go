package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Statement-temporary reclamation, stage (c): a value-consuming op whose
// RECEIVER is a fresh owned rc temporary — the canonical `(a + b).len()` —
// now stashes the receiver and DECs it after the op. Before this slice the
// length load (OpStrLen) consumed the concat's (data,len) and returned an
// i32, dropping the buffer on the floor with nothing to dec it. Measured on
// wasm: linear 1600 → 160000 → 1600000, no plateau (docs/RC-PERCEUS-PLAN.md
// "Value-consuming ops"). The receiver is created solely for the call and is
// dead after it (the i32 can't alias it), so reclaiming it is as safe as a
// discarded stage-(a) temp.
//
// Backend split (matching the shipped string-reclaim tests): wasm two-word
// strings always heap-allocate, so the bump high-water is the meaningful
// bounded-growth gate there (flat 64512 with the dec, linear 240000 →
// 2400000 without). Native single-word strings (x86_64) / deferred-reclaim
// arm64 (slice 5g) report a flat bump regardless, so they assert
// value-correctness + 0-over-release over a long-concat loop instead — which
// drives the real str_dec/rc_dec path and would surface any double-free.

func lenRecvBumpSrc(n string) string {
	return `function main(): i32 {
    var before: i32 = __heap_bump_bytes();
    var a: string = "hello there friend, ";
    var b: string = "general kenobi!!!";
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) {
        acc = acc + (a + b).len();
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    return __heap_bump_bytes() - before;
}`
}

// Value-correct + no over-release: the consumed concat must be reclaimed
// without disturbing `a` / `b`, reused every iteration. The operands total
// 37 chars (20 + 17), so acc == 37*200 == 7400; an over-release of a / b /
// the concat shows up as a wrong sum (999) or non-zero underflow count.
const lenRecvUnderflowSrc = `function main(): i32 {
    var a: string = "hello there friend, ";
    var b: string = "general kenobi!!!";
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        acc = acc + (a + b).len();
        i = i + 1;
    }
    if (acc != 7400) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64LenReceiverReclaim(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, lenRecvUnderflowSrc); code != 0 {
		t.Errorf("len-receiver reclaim: code=%d (999=value mismatch, >0=over-release)", code)
	}
}

func TestArm64LenReceiverReclaim(t *testing.T) {
	// arm64 heap-string reclaim is deferred (RC-Perceus slice 5g), so the
	// receiver str_dec is a safe no-op there — codegen stays byte-identical
	// to main. Only value-correctness + 0-over-release are checked, matching
	// the other string-reclaim tests' arm64 stance.
	if _, code := compileAndRunArm64FreeOn(t, lenRecvUnderflowSrc); code != 0 {
		t.Errorf("len-receiver reclaim: code=%d", code)
	}
}

func TestWASMLenReceiverReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, lenRecvBumpSrc("5000"))
	large := runWasm(t, lenRecvBumpSrc("50000"))
	if small != large {
		t.Errorf("len-receiver bump should be bounded: N=5000 -> %d, N=50000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("wasm two-word strings heap-allocate; expected non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, lenRecvUnderflowSrc); got != 0 {
		t.Errorf("len-receiver reclaim: got %d", got)
	}
}
