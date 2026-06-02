package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// String-concat operand reclamation (statement-temp stage-(c) sibling). A
// chained `a + b + c` lowers left-associatively to `(a + b) + c`: the outer
// concat COPIES both operands into a fresh buffer, but the inner `(a + b)`
// temp was dropped on the floor — nothing dec'd it, leaking every
// intermediate (nested-concat probe: 276928 → 2436928, linear; an N-operand
// chain leaks N-2 intermediates per evaluation). This also underlies the
// f-string desugar, which builds a `+`-chain of `.to_string()` pieces.
//
// emitStringConcat stashes each fresh-owned-string operand
// (concatOperandReclaimableString — a nested concat / string slice, or a
// fresh-returning user-function string call) in a scratch slot, runs
// OpStrConcat off the reloads, then dec's it via the rc-gated str_dec / rc_dec
// (so an aliased call operand, rc>=2 via the return-transfer inc, is only
// dec'd, never freed). Idents / field / index reads are borrowed views and
// lower in place. Native single-word x86_64 + wasm reclaim; arm64 heap-string
// reclaim is deferred (slice 5g), so its operand str_dec is a safe no-op.
//
// wasm two-word strings always heap-allocate, so the bump high-water is the
// bounded-growth gate; all three backends run an in-program value +
// over-release check (the chained result's length is verified IN FERN, so it
// survives the 8-bit native exit code).

func concatChainBump(n string) string {
	return `function main(): i32 {
    var before: i32 = __heap_bump_bytes();
    var a: string = "hello there "; var b: string = "friend general ";
    var c: string = "kenobi hi!! "; var d: string = "more text here ";
    var i: i32 = 0; var acc: i32 = 0;
    while (i < ` + n + `) {
        var s: string = a + b + c + d + a + b;
        acc = acc + s.len();
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    return __heap_bump_bytes() - before;
}`
}

// Value + over-release: the chained concat must produce the right bytes
// (length checked in Fern) without over-releasing the reused operands
// `a` / `b`. a(12)+b(15)+c(12)+d(15)+a(12)+b(15) == 81 per iter.
const concatChainCheck = `function main(): i32 {
    var a: string = "hello there "; var b: string = "friend general ";
    var c: string = "kenobi hi!! "; var d: string = "more text here ";
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) {
        var s: string = a + b + c + d + a + b;
        acc = acc + s.len();
        i = i + 1;
    }
    if (acc != 16200) { return 99; }
    return __rc_underflow_count();
}`

// Fresh-returning user-function string call as a concat operand, reused
// alongside a live `base`: tag() builds a fresh string each call; the
// operand dec must not disturb `base`. base(10)+tag(2)=12 per iter.
const concatCallOperandCheck = `function tag(n: i32): string { return "v" + "x"; }
function main(): i32 {
    var base: string = "prefix is ";
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) {
        var s: string = base + tag(i);
        acc = acc + s.len() + base.len();
        i = i + 1;
    }
    if (acc != 4400) { return 99; }
    return __rc_underflow_count();
}`

func checkConcatOperand(t *testing.T, run func(*testing.T, string) (string, int)) {
	t.Helper()
	if _, code := run(t, concatChainCheck); code != 0 {
		t.Errorf("concat-chain value/over-release: code=%d (99=value, >0=over-release)", code)
	}
	if _, code := run(t, concatCallOperandCheck); code != 0 {
		t.Errorf("concat call-operand value/over-release: code=%d", code)
	}
}

func TestX86_64ConcatOperandReclaim(t *testing.T) {
	checkConcatOperand(t, compileAndRunX86_64FreeOn)
}

func TestArm64ConcatOperandReclaim(t *testing.T) {
	checkConcatOperand(t, compileAndRunArm64FreeOn)
}

func TestWASMConcatOperandReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, concatChainBump("5000"))
	large := runWasm(t, concatChainBump("50000"))
	if small != large {
		t.Errorf("concat-chain bump should be bounded: N=5000 -> %d, N=50000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	checkConcatOperand(t, func(t *testing.T, src string) (string, int) {
		return "", runWasm(t, src)
	})
}
