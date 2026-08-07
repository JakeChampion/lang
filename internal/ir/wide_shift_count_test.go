package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// wasm's `i64.shl` / `i64.shr_s` / `i64.shr_u` require BOTH operands to be
// i64. A shift count is an independent expression the checker does not force to
// the value's width, so `s << i` with an i64 `s` and a 32-bit `i` left an
// (i64, i32) pair on the stack and produced a module that failed validation —
// "type mismatch: expected i64, found i32". The compiler reported success, so
// the failure surfaced only when something tried to load the result.
//
// Native targets take the count from a register regardless of its declared
// width, which is why the interpreter, x86-64 and arm64 all agreed on the
// right answer while only wasm broke.
//
// The gap was narrow: a parameter or an ordinary local reaches the shift
// through a checker-inserted cast and was already extended, and a constant
// count takes emitShlByConst, fixed earlier for exactly this reason. A LOOP
// VARIABLE goes through neither — it arrives as a bare 32-bit local.
//
// Asserted on the op stream rather than on wasm bytes because the invariant is
// "the count reaches the shift at the shift's width" — one target happens to
// reject the violation and the others silently tolerate it, and the invariant
// is what should be pinned.

// extendPrecedesWideShift reports whether every 64-bit shift in fn is
// immediately preceded by a widening extend.
func extendPrecedesWideShift(fn *ir.Func) bool {
	seen := false
	for i, op := range fn.Ops {
		if op.Kind != ir.OpShl && op.Kind != ir.OpShrS {
			continue
		}
		if op.Width != 64 {
			continue
		}
		seen = true
		if i == 0 {
			return false
		}
		prev := fn.Ops[i-1].Kind
		if prev != ir.OpExtendI32S && prev != ir.OpExtendI32U && prev != ir.OpConstI64 {
			return false
		}
	}
	return seen
}

func TestWideShiftCountIsWidened(t *testing.T) {
	// Every case here uses a LOOP VARIABLE as the count, because that is the
	// shape that actually reproduced. A parameter or an ordinary local reaches
	// the shift through a checker-inserted cast and was already extended; a
	// count the folder resolves to a constant takes emitShlByConst, which has
	// emitted an i64 const all along. Only the loop variable arrives as a bare
	// 32-bit local with a 64-bit shift above it — confirmed by building each
	// spelling for wasm against a pre-fix compiler and validating the module.
	for _, tc := range []struct{ name, src string }{
		{"left shift", `
function main(): i32 {
    var s: i64 = 1;
    var t: i64 = 0;
    for i in 0..4 { t = t + (s << i); }
    return t as i32;
}`},
		{"right shift", `
function main(): i32 {
    var s: i64 = 1024;
    var t: i64 = 0;
    for i in 0..3 { t = t + (s >> i); }
    return t as i32;
}`},
		{"unsigned", `
function main(): i32 {
    var u: u64 = 1;
    var t: u64 = 0;
    for i in 0..4 { t = t + (u << i); }
    return t as i32;
}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ip := lowerForTest(t, tc.src+"\n")
			if !extendPrecedesWideShift(funcByName(ip, "main")) {
				t.Error("a 64-bit shift takes its count at 32 bits — wasm rejects the module it produces, and the natives only work by ignoring the width")
			}
		})
	}
}

// A count that is ALREADY 64-bit must not be extended again: the extra op
// would consume an i64 as though it were an i32.
func TestWideShiftCountAlreadyWideIsNotExtended(t *testing.T) {
	ip := lowerForTest(t, `
function main(): i32 {
    var s: i64 = 1;
    var k: i64 = 3;
    return (s << k) as i32;
}
`)
	for i, op := range funcByName(ip, "main").Ops {
		if op.Kind != ir.OpShl || op.Width != 64 || i == 0 {
			continue
		}
		if prev := funcByName(ip, "main").Ops[i-1].Kind; prev == ir.OpExtendI32S || prev == ir.OpExtendI32U {
			t.Error("an i64 shift count was extended as though it were an i32")
		}
	}
}

// A 32-bit shift is unaffected: both operands are already i32, and an extend
// here would make the shift 64-bit on a value that is not.
func TestNarrowShiftCountIsNotWidened(t *testing.T) {
	ip := lowerForTest(t, `
function main(): i32 {
    var s: i32 = 1;
    var k: i32 = 3;
    return s << k;
}
`)
	ops := funcByName(ip, "main").Ops
	for i, op := range ops {
		if op.Kind != ir.OpShl || i == 0 {
			continue
		}
		if op.Width == 64 {
			t.Fatal("an i32 shift lowered at width 64; this test no longer covers what it says")
		}
		if prev := ops[i-1].Kind; prev == ir.OpExtendI32S || prev == ir.OpExtendI32U {
			t.Error("a 32-bit shift widened its count")
		}
	}
}
