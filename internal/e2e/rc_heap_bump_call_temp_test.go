package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Call-result reclamation at BORROWING string consumers (RC-Perceus,
// statement-temporary mechanism). A call returning string hands back an
// OWNED (+1) value, and the general drop machinery already reclaims one
// that is discarded as a statement or consumed as an argument. But a
// borrowing consumer — OpStrConcat, OpStrEq, __str_idx, __str_slice — only
// READS its operand's bytes and leaves the buffer alone, so a call result
// used directly there was reclaimed by nobody: `"n = " + f(x)`,
// `f(x) == "…"`, `f(x)[0]` and `f(x)[a:b]` each leaked one buffer per
// evaluation, unbounded in a loop. isOwnedStringTemp now admits a
// string-returning call alongside the sub-concat / string-slice shapes,
// and all four consumers stash-and-dec through the shared
// stashOwnedStringOperand / decStashedStringTemps pair.
//
// The leak hid behind short strings: a result of 7 bytes or fewer is
// inline-tagged (SSO) and never allocates, so `f.to_string()` on a small
// number showed nothing. These fixtures stay above the threshold.
//
// The measurement is self-contained rather than two runs at different N
// (as its nested-concat sibling does) because the native legs report
// through an EXIT CODE: a growth that happens to be a multiple of 256 is
// invisible there, and 192256 vs 1632256 — the wasm figures for this
// shape — are both ≡ 0 mod 256. Measuring the delta INSIDE the program
// sidesteps the truncation entirely: once the freelist is warm a
// reclaiming loop leaves the bump high-water exactly flat, so the check
// is `delta == 0` over a span that a leak would grow by 20000 buffers.
// The warm-up burn is separate because wasm's first couple of thousand
// iterations still bump (512 B, settling well before 2000) as the size
// classes fill; native reaches steady state immediately.

// Exit code 0 = every consumer bounded; 1..4 names the one that grew.
const callTempConsumerBumpSrc = `function mk(s: string): string { return s[0:20] + "!"; }
function burn(a: string, n: i32, mode: i32): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < n) {
        if (mode == 0) { acc = acc + ("<" + mk(a)).len(); }
        if (mode == 1) { if (mk(a) == "zzz") { acc = acc + 1; } }
        if (mode == 2) { var c: u8 = mk(a)[0]; acc = acc + (c as i32); }
        if (mode == 3) { acc = acc + (mk(a)[0:3] + "").len(); }
        i = i + 1;
    }
    return acc;
}
function grew(a: string, mode: i32): i32 {
    burn(a, 2000, mode);
    var b1: i32 = (__heap_bump_bytes() as i32);
    burn(a, 20000, mode);
    return (__heap_bump_bytes() as i32) - b1;
}
function main(): i32 {
    var a: string = "longer_string_one_here";
    var m: i32 = 0;
    while (m < 4) {
        if (grew(a, m) != 0) { return m + 1; }
        m = m + 1;
    }
    return 0;
}`

// Value-correctness + 0 over-release. `pick` returns a BORROWED operand —
// the caller's own `a` — which is exactly the shape a too-eager dec would
// corrupt: freeing it hands its block back to the freelist, the next
// iteration reuses it, and the final content checks see garbage. It stays
// correct because a Fern return is owned at the call site, so the borrowed
// return was inc'd on the way out. Each borrowing consumer gets a turn.
const callTempConsumerUnderflowSrc = `function mk(s: string): string { return s[0:2] + "!"; }
function pick(a: string, b: string): string {
    if (a.len() > 3) { return a; }
    return b;
}
function main(): i32 {
    var seed: string = "abcdefghij";
    var a: string = seed[0:8] + "";
    var b: string = "xy";
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        var s: string = "<" + mk(a) + ">";      // "<ab!>",      len 5
        var t: string = "[" + pick(a, b) + "]"; // "[abcdefgh]", len 10
        acc = acc + s.len() + t.len();
        if (pick(a, b) == "abcdefgh") { acc = acc + 1; }
        var c: u8 = pick(a, b)[0];              // 'a'
        acc = acc + (c as i32);
        acc = acc + (pick(a, b)[0:3] + "").len();
        i = i + 1;
    }
    if (acc != 23200) { return 999; } // 200 * (5 + 10 + 1 + 97 + 3)
    if (a != "abcdefgh") { return 888; }
    if (b != "xy") { return 887; }
    if (("<" + mk(a) + ">") != "<ab!>") { return 777; }
    return __rc_underflow_count();
}`

func TestX86_64CallTempConsumerReclaim(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, callTempConsumerBumpSrc); code != 0 {
		t.Errorf("call-temp bump should be flat after warm-up: code=%d (1=concat 2=compare 3=index 4=slice)", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, callTempConsumerUnderflowSrc); code != 0 {
		t.Errorf("call-temp reclaim: code=%d (999=acc, 888/887=borrowed operand freed, 777=content, >0=over-release)", code)
	}
}

func TestArm64CallTempConsumerReclaim(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, callTempConsumerBumpSrc); code != 0 {
		t.Errorf("call-temp bump should be flat after warm-up: code=%d (1=concat 2=compare 3=index 4=slice)", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, callTempConsumerUnderflowSrc); code != 0 {
		t.Errorf("call-temp reclaim: code=%d", code)
	}
}

func TestWASMCallTempConsumerReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, callTempConsumerBumpSrc); got != 0 {
		t.Errorf("call-temp bump should be flat after warm-up: got %d (1=concat 2=compare 3=index 4=slice)", got)
	}
	if got := runWasm(t, callTempConsumerUnderflowSrc); got != 0 {
		t.Errorf("call-temp reclaim: got %d", got)
	}
}
