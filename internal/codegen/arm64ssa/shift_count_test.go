package arm64ssa_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// A shift masks its count to the WIDTH OF THE OPERATION — 5 bits at i32, 6 at
// i64 — and AArch64 masks to the width of the register the instruction names.
// So a 32-bit shift rendered as `lsl x` silently becomes a 6-bit mask:
// `460 << 124` is `460 << 28` (= -1073741824) at i32, but `460 << 60` on the
// full register, whose low 32 bits are 0.
//
// The signed shifts used to take the x-form on the grounds that a
// sign-extended operand is already correct. That is true of the VALUE and says
// nothing about the COUNT, which is what this pins.
//
// Found by teaching fernsmith to emit `<<` / `>>`: 16 differential seeds failed
// at once, all on arm64-ssa, while every production backend agreed.
func TestArmRunShiftCountMasking(t *testing.T) {
	// runMatchesEval compares a 1-BYTE exit code, and the divergences here live
	// in the high bits: a left shift by >= 8 zeroes the low byte on the correct
	// and the incorrect path alike, so `return r` would pass against broken
	// codegen. Folding the result mod 251 (prime, < 256) carries every bit down
	// into the exit byte. Verified by reverting the fix: 6 of the 12 cases below
	// go red through the fold, only 3 without it.
	build := func(k ssa.OpKind, val, count int64, width int8) *ssa.Func {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		// The constants carry the op's width too: at the default i32 width Eval
		// masks a materialised const to its low 32 bits while the backend
		// materialises all 64, which would diverge on the i64 operands here for
		// reasons that have nothing to do with shifts.
		konst := func(v int64) ssa.Value {
			c := constOp(f, e, v)
			e.Ops[len(e.Ops)-1].Width = width
			return c
		}
		r := f.AddOp(e, k, konst(val), konst(count))
		e.Ops[len(e.Ops)-1].Width = width
		obs := f.AddOp(e, ssa.OpRemU, r, konst(251))
		e.Ops[len(e.Ops)-1].Width = width
		f.SetRet(e, obs)
		return f
	}
	cases := []struct {
		name  string
		kind  ssa.OpKind
		val   int64
		count int64
		width int8
	}{
		// Counts at/over the width are where the two maskings diverge.
		{"shl_i32_count_124", ssa.OpShl, 460, 124, 0},
		{"shl_i32_count_33", ssa.OpShl, 460, 33, 0},
		{"shl_i32_count_32", ssa.OpShl, 1, 32, 0},
		{"shl_i32_count_neg1", ssa.OpShl, 1, -1, 0},
		{"shr_i32_count_33", ssa.OpShr, -8, 33, 0},
		{"shr_i32_count_40", ssa.OpShr, 0x1234, 40, 0},
		{"shru_i32_count_33", ssa.OpShrU, -8, 33, 0},
		// In-range counts must be unaffected.
		{"shl_i32_count_1", ssa.OpShl, 460, 1, 0},
		{"shr_i32_count_1", ssa.OpShr, -8, 1, 0},
		// i64 keeps the 6-bit mask — a w-form here would truncate the operand
		// as well as narrow the mask.
		{"shl_i64_count_33", ssa.OpShl, 1, 33, 64},
		{"shl_i64_count_96", ssa.OpShl, 1, 96, 64},
		{"shr_i64_count_33", ssa.OpShr, 1 << 40, 33, 64},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, n := range []int{1, 2, 8} {
				runMatchesEval(t, build(c.kind, c.val, c.count, c.width), n)
			}
		})
	}
}
