package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
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
// A loop variable used as the count takes the shift's width itself
// (#10123), so it reaches the shift as a 64-bit local. The extend is still
// what carries a count a use has already fixed at i32.
//
// Asserted on the op stream rather than on wasm bytes because the invariant is
// "the count reaches the shift at the shift's width" — one target happens to
// reject the violation and the others silently tolerate it, and the invariant
// is what should be pinned.

// countIsWide reports whether every 64-bit shift in fn takes a 64-bit count:
// a widening extend, an i64 constant, or a load of a 64-bit local.
func countIsWide(fn *ir.Func) bool {
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
		prev := fn.Ops[i-1]
		switch prev.Kind {
		case ir.OpExtendI32S, ir.OpExtendI32U, ir.OpConstI64:
		case ir.OpLoadLocal:
			if !wideLocal(fn, prev.I32) {
				return false
			}
		default:
			return false
		}
	}
	return seen
}

func wideLocal(fn *ir.Func, idx int32) bool {
	i := int(idx)
	var t ast.Type
	switch {
	case i < len(fn.Params):
		t = fn.Params[i].Type
	case i-len(fn.Params) < len(fn.Locals):
		t = fn.Locals[i-len(fn.Params)].Type
	default:
		return false
	}
	nt, ok := t.(ast.NumberType)
	return ok && nt.Width == 64
}

func TestWideShiftCountIsWidened(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"left shift", `
function main(): i32 {
    let s: i64 = 1;
    let t: i64 = 0;
    for i in 0..4 { t = t + (s << i); }
    return t as i32;
}`},
		{"right shift", `
function main(): i32 {
    let s: i64 = 1024;
    let t: i64 = 0;
    for i in 0..3 { t = t + (s >> i); }
    return t as i32;
}`},
		{"unsigned", `
function main(): i32 {
    let u: u64 = 1;
    let t: u64 = 0;
    for i in 0..4 { t = t + (u << i); }
    return t as i32;
}`},
		{"count fixed at i32 first", `
function main(): i32 {
    let s: i64 = 1;
    let t: i64 = 0;
    for i in 0..4 { let j: i32 = i; t = t + (s << i); }
    return t as i32;
}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ip := lowerForTest(t, tc.src+"\n")
			if !countIsWide(funcByName(ip, "main")) {
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
    let s: i64 = 1;
    let k: i64 = 3;
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
    let s: i32 = 1;
    let k: i32 = 3;
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
