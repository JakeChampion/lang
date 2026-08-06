package e2e

// The bit-counting intrinsics: `__clz32` / `__ctz32` / `__popcount32` and their
// 64-bit siblings. Each lowers to a single IR op (OpClz / OpCtz / OpPopcount)
// rather than a runtime-helper call, because avoiding the call is most of the
// point — measured on x86-64, 20M calls cost 0.489s through the stdlib's SWAR
// sequence against 0.099s for the same loop with a trivial callee, so the SWAR
// body is ~80% of the runtime and a call would put it straight back.
//
// WHAT EACH BACKEND ACTUALLY EMITS differs, and the differences are load-
// bearing rather than incidental:
//
//   - wasm has all six as single opcodes with exactly these zero semantics.
//   - arm64 gets a real `clz`; ctz is `rbit` + `clz`, which inherits the
//     width-at-zero definition for free (reversing zero leaves zero);
//     popcount is the SIMD `cnt` + `addv` pair, since AArch64 has no scalar
//     popcount.
//   - x86-64 uses `lzcnt` / `tzcnt` / `popcnt`. The first two are DEFINED at
//     zero — they return the operand width, exactly what the op specifies —
//     which is why they are selected over the same-opcode `bsr` / `bsf`,
//     whose destination is undefined there.
//
// So ZERO is the case that matters most here: it is the input the op
// definition pins, and the one where a wrong answer is most likely to look
// plausible (-1, or the width off by one). Every op is checked at zero first,
// then at every single-bit position across both widths, so an off-by-one in
// the width arithmetic cannot hide at one end.
//
// The interpreter is the oracle and uses math/bits rather than replicating the
// SWAR sequence — an independent implementation, so a shared bug cannot make
// both agree.

import "testing"

const bitIntrinsicsProg = `
import "std/i32";
import "std/u32";
import "std/u64";
import "std/string";

function main(): i32 {
    // Zero: the defined edge. clz/ctz of 0 is the operand width, not -1 and
    // not 0. Three of the six backend lowerings branch specially for this.
    if (__clz32(0 as u32) != 32) { return 1; }
    if (__ctz32(0 as u32) != 32) { return 2; }
    if (__popcount32(0 as u32) != 0) { return 3; }
    if (__clz64(0 as u64) != 64) { return 4; }
    if (__ctz64(0 as u64) != 64) { return 5; }
    if (__popcount64(0 as u64) != 0) { return 6; }

    // Every single-bit position, 32-bit. Catches an off-by-one in the
    // "31 - clz" arithmetic at either end of the range.
    var i: i32 = 0;
    while (i < 32) {
        var v: u32 = (1 as u32) << (i as u32);
        if (__clz32(v) != 31 - i) { return 10 + i; }
        if (__ctz32(v) != i) { return 50 + i; }
        if (__popcount32(v) != 1) { return 90 + i; }
        i = i + 1;
    }

    // Saturated and top-bit values.
    if (__popcount32(4294967295 as u32) != 32) { return 200; }
    if (__clz32(4294967295 as u32) != 0) { return 201; }
    if (__ctz32(4294967295 as u32) != 0) { return 202; }
    if (__clz32(2147483648 as u32) != 0) { return 203; }
    if (__ctz32(2147483648 as u32) != 31) { return 204; }

    // Every single-bit position, 64-bit -- the width where a 32-bit-shaped
    // constant or a w-register slip would truncate silently.
    var j: i32 = 0;
    while (j < 64) {
        var w: u64 = (1 as u64) << (j as u64);
        if (__clz64(w) != 63 - j) { return 300 + j; }
        if (__ctz64(w) != j) { return 400 + j; }
        if (__popcount64(w) != 1) { return 500 + j; }
        j = j + 1;
    }
    if (__popcount64(18446744073709551615 as u64) != 64) { return 600; }
    if (__clz64(18446744073709551615 as u64) != 0) { return 601; }
    if (__ctz64(18446744073709551615 as u64) != 0) { return 602; }

    // A mixed pattern: 0xF0F0F0F0 is 16 set bits, 0 leading zeros, 4 trailing.
    if (__popcount32(4042322160 as u32) != 16) { return 700; }
    if (__clz32(4042322160 as u32) != 0) { return 701; }
    if (__ctz32(4042322160 as u32) != 4) { return 702; }

    return 42;
}
`

func TestBitIntrinsicsInterp(t *testing.T) {
	if got := runInterpExit(t, bitIntrinsicsProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestBitIntrinsicsX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, bitIntrinsicsProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestBitIntrinsicsWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, bitIntrinsicsProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestBitIntrinsicsArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, bitIntrinsicsProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
