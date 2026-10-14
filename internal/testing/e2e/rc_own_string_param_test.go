package e2e

import (
	"testing"
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
//   - fresh heap bytes stay within a small multiple of what the accumulator
//     holds. The eligibility entry the fix restores is also
//     selfStrAppendLocalSpine's gate, so the accumulator grows in place, and
//     past its capacity into a block twice its length (#8770), instead of
//     allocating a fresh buffer per append.
//
// The 8x bound is a wide fence around a settled measurement. 200 rounds
// (1600 bytes) took 2 560 fresh bytes and 800 rounds (6400 bytes) 15 360 on
// x86-64 and arm64, 2 048 and 12 288 on wasm: 1.6x to 2.4x, since the
// blocks a doubling accumulator leaves behind sum to at most twice its last.
// Pre-fix x86-64 took 2 801 632 bytes for the 800 rounds, 437x, so the fence
// sits fifty times clear of the failure it is there to catch.
//
// Self-checking: 0 == both hold, 97 == a wrong length, 98 == the bytes
// outgrow the string, 99 == over-release.
const ownStringParamSrc = `import "std/i64";
function grow(own a: string, s: string): string { a = a + s; return a; }
function rounds(n: i32): i32 {
    let acc: string = "";
    let i: i32 = 0;
    while (i < n) { acc = grow(acc, "12345678"); i = i + 1; }
    return acc.len();
}
function main(): i32 {
    if (rounds(64) != 512) { return 97; }          // warm-up: first block of each size class
    let b0: i64 = __heap_bump_bytes();
    if (rounds(200) != 1600) { return 97; }
    let b1: i64 = __heap_bump_bytes();
    if (rounds(800) != 6400) { return 97; }        // 4x the appends
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (b1 - b0 > 8 * 1600 || b2 - b1 > 8 * 6400) { return 98; }    // see the margin above
    return 0;
}`

func TestX86_64OwnStringParamBalanced(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, ownStringParamSrc); code != 0 {
		t.Errorf("own string param: got %d, want 0 (97=length, 98=bytes outgrow the string, 99=over-release)", code)
	}
}

func TestArm64OwnStringParamBalanced(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, ownStringParamSrc); code != 0 {
		t.Errorf("own string param: got %d, want 0 (97=length, 98=bytes outgrow the string, 99=over-release)", code)
	}
}

func TestWASMOwnStringParamBalanced(t *testing.T) {
	if got := runWasm(t, ownStringParamSrc); got != 0 {
		t.Errorf("own string param: got %d, want 0 (97=length, 98=bytes outgrow the string, 99=over-release)", got)
	}
}
