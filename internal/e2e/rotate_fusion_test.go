package e2e

// `(x >> n) | (x << (W-n))` with a constant n — the way every Fern program
// spells a rotate, since there is no rotate operator — fuses to a single
// ir.OpRotr, and each backend issues its own rotate instruction for it:
// `ror` on x86-64 and arm64, `i32.rotr` / `i64.rotr` on wasm.
//
// The values below are chosen so a WRONG lowering cannot pass. A right shift
// that stayed arithmetic gives the correct answer for every operand with a
// clear top bit, so the cases here have bit 31 (or bit 63) set. A rotate that
// took the 64-bit register form on a 32-bit operand, or masked its count at
// the wrong width, drags the other half of the register through — so the
// counts sweep the whole range including 1 and W-1.
//
// The mirror spelling `(x << n) | (x >> (W-n))` is a LEFT rotate; the IR
// normalises it to a right rotate by W-n, so both directions are exercised.
// The count boundaries the fusion refuses — 0 and W — are checked here too:
// they are not rotates by this pattern and must keep computing what the
// unfused shifts computed.
//
// The interpreter runs the same source without ever seeing the IR, so it is an
// independent oracle for every number below.

import "testing"

const rotateFusionProg = `
function rotr32(x: u32, n: u32): u32 {
    return (x >> n) | (x << ((32 as u32) - n));
}

function rotl32(x: u32, n: u32): u32 {
    return (x << n) | (x >> ((32 as u32) - n));
}

function rotr64(x: u64, n: u64): u64 {
    return (x >> n) | (x << ((64 as u64) - n));
}

function main(): i32 {
    // 32-bit, top bit set: an arithmetic right shift would fill with ones.
    var d: u32 = 0xdeadbeef as u32;
    if (((d >> (8 as u32)) | (d << (24 as u32))) != (0xefdeadbe as u32)) { return 1; }
    if (((d >> (1 as u32)) | (d << (31 as u32))) != (0xef56df77 as u32)) { return 2; }
    if (((d >> (31 as u32)) | (d << (1 as u32))) != (0xbd5b7ddf as u32)) { return 3; }

    // The left spelling of the same rotate: rotl 24 == rotr 8.
    if (((d << (24 as u32)) | (d >> (8 as u32))) != (0xefdeadbe as u32)) { return 4; }

    // 64-bit.
    var p: u64 = 0x0123456789abcdef as u64;
    var q: u64 = 0xfedcba9876543210 as u64;
    if (((p >> (8 as u64)) | (p << (56 as u64))) != (0xef0123456789abcd as u64)) { return 5; }
    if (((q >> (63 as u64)) | (q << (1 as u64))) != (0xfdb97530eca86421 as u64)) { return 6; }
    if (((q << (63 as u64)) | (q >> (1 as u64))) != (0x7f6e5d4c3b2a1908 as u64)) { return 7; }

    // A repeated compound operand — the shape std/crypto's digests write, and
    // the one the fusion has to recognise as a single value on both sides.
    var a: u64 = 0xa5a5a5a5a5a5a5a5 as u64;
    var b: u64 = 0x5a5a5a5a5a5a5a5a as u64;
    if ((((a ^ b) >> (32 as u64)) | ((a ^ b) << (32 as u64))) != (0xffffffffffffffff as u64)) { return 8; }
    var c: u64 = 0xffffffff00000000 as u64;
    if ((((a ^ c) >> (16 as u64)) | ((a ^ c) << (48 as u64))) != (0xa5a55a5a5a5aa5a5 as u64)) { return 9; }

    // Non-constant counts: not fusable, and must keep working.
    var i: u32 = 1 as u32;
    while (i < (32 as u32)) {
        if (rotr32(0x80000001 as u32, i) != rotl32(0x80000001 as u32, (32 as u32) - i)) { return 20; }
        i = i + (1 as u32);
    }
    if (rotr32(0x80000001 as u32, 1 as u32) != (0xc0000000 as u32)) { return 21; }
    if (rotr64(0x8000000000000001 as u64, 1 as u64) != (0xc000000000000000 as u64)) { return 22; }

    // The two counts the fusion refuses. A shift count masks to the operand
    // width, so both of these are the value or-ed with itself.
    if (((d >> (0 as u32)) | (d << (32 as u32))) != (0xdeadbeef as u32)) { return 30; }
    if (((d >> (32 as u32)) | (d << (0 as u32))) != (0xdeadbeef as u32)) { return 31; }

    // A SIGNED right shift is not a rotate: i32 keeps the sign bit, so
    // -2 shifted right by 1 is -1 and the or below is all-ones.
    var s: i32 = -2;
    if (((s >> 1) | (s << 31)) != -1) { return 40; }

    return 42;
}
`

func TestRotateFusionInterp(t *testing.T) {
	if got := runInterpExit(t, rotateFusionProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestRotateFusionX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, rotateFusionProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestRotateFusionWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, rotateFusionProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestRotateFusionArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, rotateFusionProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
