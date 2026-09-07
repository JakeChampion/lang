package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// #8804: `own` on a STRING parameter was balanced on no backend, and the two
// halves of the bug hid each other. The callee's exit release was admitted
// only under the two-word string ABI, so a single-word x86-64 `own` string
// param was never released — one buffer per call. The caller's overwrite-dec
// fired whatever the callee did, so on the two-word ABIs (arm64, wasm) the
// caller and the callee both released the same buffer: an over-release that
// segfaulted arm64 and trapped wasm from the third call.
//
// `acc = grow(acc, piece)` is the ONE shape E051 admits for a plain local in
// an `own` position, so it is the shape every consuming accumulator is
// written in. It is a move: the callee reclaims the reference.
//
// Two observables, and the first is what makes this a memory-safety fix
// rather than a leak fix:
//
//   - `__rc_underflow_count() == 0`. The pre-fix arm64 / wasm builds reach 1
//     at three appends and die at five hundred.
//   - fresh heap bytes do not scale with the round count. The eligibility
//     entry the fix restores is also isSelfStrAppendLocal's gate, so the
//     accumulator grows in place instead of allocating a fresh buffer per
//     append.
//
// The 2x bound is a wide fence around a settled measurement, not a guess.
// The two SEGMENT deltas the check compares — b1-b0 over 200 rounds, then
// b2-b1 over 800, four times the appends — came out at 73 968 / 83 040 on
// x86-64, 145 792 / 164 464 on arm64 and 73 440 / 81 424 on wasm: a ratio
// of 1.11x to 1.13x everywhere, because an in-place append costs bytes only
// when it crosses a size class. Pre-fix x86-64 was 164 800 / 2 801 632 —
// 17.0x — so the fence sits an order of magnitude clear of the failure it
// is there to catch, with room for allocator noise it must not trip on.
//
// Self-checking: 0 == both hold, 97 == a wrong length, 98 == the bytes scale,
// 99 == over-release.
const ownStringParamSrc = `import "std/i64";
function grow(own a: string, s: string): string { a = a + s; return a; }
function rounds(n: i32): i32 {
    var acc: string = "";
    var i: i32 = 0;
    while (i < n) { acc = grow(acc, "12345678"); i = i + 1; }
    return acc.len();
}
function main(): i32 {
    if (rounds(64) != 512) { return 97; }          // warm-up: first block of each size class
    var b0: i64 = __heap_bump_bytes();
    if (rounds(200) != 1600) { return 97; }
    var b1: i64 = __heap_bump_bytes();
    if (rounds(800) != 6400) { return 97; }        // 4x the appends
    var b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 > (b1 - b0) * 2) { return 98; }    // see the margin above
    return 0;
}`

func TestX86_64OwnStringParamBalanced(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, ownStringParamSrc); code != 0 {
		t.Errorf("own string param: got %d, want 0 (97=length, 98=bytes scale with rounds, 99=over-release)", code)
	}
}

func TestArm64OwnStringParamBalanced(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, ownStringParamSrc); code != 0 {
		t.Errorf("own string param: got %d, want 0 (97=length, 98=bytes scale with rounds, 99=over-release)", code)
	}
}

func TestWASMOwnStringParamBalanced(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, ownStringParamSrc); got != 0 {
		t.Errorf("own string param: got %d, want 0 (97=length, 98=bytes scale with rounds, 99=over-release)", got)
	}
}
