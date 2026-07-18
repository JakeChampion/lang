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

// A USER-CALL receiver — `f(i).len()` — is the same dead-after-consume temp,
// but it never matched freshOwnedRcTempType (that classifier covers concat /
// slice / literal shapes only), and the len site had no ownedCallResultType
// fallback like the discarded-stmt / index-of-fresh / field-access / call-arg
// sites, so the callee's returned heap value leaked every call: ~32-128 B for
// strings above the SSO inline threshold (sub-threshold results return inline
// and masked the leak in short-chain probes), one buffer per call for arrays.
// This is the shape that fell out of #4357's SSA-emit RSS localization.
//
// The fix adds the fallback; safety is the same is_unique-gated argument as
// every other ownedCallResultType site — an aliased return carries the
// return-transfer inc, so the drop only dec's it (the alias negative below).
// The `tail` closes main() after `g` holds the bump growth: native runners
// read the process EXIT CODE (mod-256), so they scale + guard-cap the value;
// the wasm runner reads main's full i32 result via `--invoke`, so it returns
// raw growth (wasm's two-word strings put the bounded high-water at ~65 KB
// after a few-thousand-iteration freelist warm-up — far past any exit code).
const lenCallRecvNativeTail = `    if (g > 900) { return 119; }
    return g / 8;
}`
const lenCallRecvRawTail = `    return g;
}`

func lenCallRecvStrBumpSrc(n, tail string) string {
	return `function f(k: i32): string {
    var p: string = "abcdefgh";
    return "(func $x" + p + " (param i32) (result i32)" + p + " local.get 0" + p + " i32.const 1" + p + " i32.add)" + p;
}
function main(): i32 {
    var before: i32 = __heap_bump_bytes();
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + f(i).len(); i = i + 1; }
    if (acc < 0) { return 121; }
    var g: i32 = __heap_bump_bytes() - before;
` + tail
}

func lenCallRecvArrBumpSrc(n, tail string) string {
	return `function f(k: i32): i32[] {
    return [k, k + 1, k + 2, k + 3, k + 4, k + 5, k + 6, k + 7, k + 8, k + 9];
}
function main(): i32 {
    var before: i32 = __heap_bump_bytes();
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + f(i).len(); i = i + 1; }
    if (acc != ` + n + ` * 10) { return 121; }
    var g: i32 = __heap_bump_bytes() - before;
` + tail
}

// SOUNDNESS NEGATIVE — identity callees hand back the CALLER's value at
// rc >= 2 (return-transfer inc), so the len-receiver drop must only dec it:
// base / arr survive every iteration value-intact, over-release detector 0.
const lenCallRecvAliasSrc = `function id(s: string): string { return s; }
function idarr(xs: i32[]): i32[] { return xs; }
function main(): i32 {
    var base: string = "0123456789abcdef" + "-suffix-to-force-heap";
    var arr: i32[] = [10, 20, 30, 40, 50, 60, 70, 80, 90, 100];
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 5000) {
        acc = acc + id(base).len() + idarr(arr).len();
        i = i + 1;
    }
    if (acc != 5000 * (37 + 10)) { return 121; }
    if (base.len() != 37) { return 122; }
    if (arr[9] != 100) { return 123; }
    return __rc_underflow_count();
}`

func runLenCallReceiverChecks(t *testing.T, run func(*testing.T, string) int, nSmall, nLarge, tail string) {
	t.Helper()
	for _, tc := range []struct {
		name string
		src  func(string, string) string
	}{
		{"str-chain", lenCallRecvStrBumpSrc},
		{"arr", lenCallRecvArrBumpSrc},
	} {
		small := run(t, tc.src(nSmall, tail))
		large := run(t, tc.src(nLarge, tail))
		if small != large {
			t.Errorf("%s call-receiver bump should be bounded: N=%s -> %d, N=%s -> %d", tc.name, nSmall, small, nLarge, large)
		}
		if small == 0 {
			t.Errorf("%s: expected a non-zero bounded high-water, got 0", tc.name)
		}
		if tail == lenCallRecvNativeTail && small >= 119 {
			t.Errorf("%s: growth guard tripped (%d) — the call receiver is leaking again", tc.name, small)
		}
	}
	if code := run(t, lenCallRecvAliasSrc); code != 0 {
		t.Errorf("alias-negative: code=%d (121-123=value corruption, >0=over-release)", code)
	}
}

func TestX86_64LenCallReceiverReclaim(t *testing.T) {
	runLenCallReceiverChecks(t, mustRunX86_64FreeOn, "50", "5000", lenCallRecvNativeTail)
}

func TestArm64LenCallReceiverReclaim(t *testing.T) {
	runLenCallReceiverChecks(t, mustRunArm64FreeOn, "50", "5000", lenCallRecvNativeTail)
}

func TestWASMLenCallReceiverReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	// The wasm high-water plateaus only after a few thousand iterations
	// (freelist size-class warm-up; verified flat 5000 -> 200000, vs the
	// pre-fix ~131 B/iter linear growth), so the fixpoint compares
	// N=5000/50000 on the raw growth `--invoke main` hands back.
	runLenCallReceiverChecks(t, func(t *testing.T, src string) int {
		return runWasm(t, src)
	}, "5000", "50000", lenCallRecvRawTail)
}
