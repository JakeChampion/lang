package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// The string-element sibling of TestX86_64ArrayWithPtrElemRecycles (#6407).
//
// `arr.with(i, v)` on a `string[]` did no element bookkeeping at all: strings
// were absent from the counted-element set, so the CoW copy shared the
// receiver's element buffers at unchanged rc, the overwritten element was
// never released, and — because the escape analysis keys the receiver's
// reclaimability on that store being counted — the whole receiver was tainted
// out of freeEligible. One `.with` therefore stranded N+1 blocks per round,
// which is what made every `sort` of a `string[]` grow without bound.
//
// The probe is the shape from the issue: build an 8-element `string[]` of
// heap strings, do one `.with` whose value is read out of the same array
// (the projection the escape walk follows to the receiver), discard, repeat.
// Nothing survives a round, so the bump cursor must stay flat: at 100k rounds
// the leak was ~350 B/round, tens of MiB. The 1 MiB bound is the same one the
// pointer-element probe uses.
//
// Run on all three compiled backends, because the retain the copy branch owes
// is spelled differently on each: native single-word x86-64 walks the buffer
// with __fern_rc_inc (__fern_arr_cow_inplace_ptr), while arm64 and wasm carry
// two-word (data, len) elements whose inline tag lives in `len` — they need
// __fern_arr_cow_inplace_str's __fern_str_inc walk, and the single-word helper
// would dereference an inline string's characters as a pointer.
const arrWithStrElemChurnSrc = `import "std/i32";
import "std/string";
function mks(): string[] {
    var out: string[] = [];
    var i: i32 = 0;
    while (i < 8) { out = out.append("kkkkkkkkkkkkkkkkkkkk" + i.to_string()); i = i + 1; }
    return out;
}
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var a: string[] = mks();
        a = a.with(3, a[5]);
        t = t + a.len() + a[3].len();
        i = i + 1;
    }
    if (t != n * 29) { return 99; }
    if ((__heap_bump_bytes() as i32) < 1048576) { return 0; }
    return 1;
}
function main(): i32 { return churn(20000); }`

func TestX86_64ArrayWithStrElemRecycles(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, arrWithStrElemChurnSrc); code != 0 {
		t.Errorf("string-element .with churn: got exit %d, want 0 (heap bump < 1 MiB — the receiver and the replaced element must recycle)", code)
	}
}

func TestArm64ArrayWithStrElemRecycles(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, arrWithStrElemChurnSrc); code != 0 {
		t.Errorf("string-element .with churn: got exit %d, want 0", code)
	}
}

func TestWASMArrayWithStrElemRecycles(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, arrWithStrElemChurnSrc); got != 0 {
		t.Errorf("string-element .with churn: got exit %d, want 0", got)
	}
}
