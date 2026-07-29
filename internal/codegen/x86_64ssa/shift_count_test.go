package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// shiftCountCase is shared by the real-assembly test below and the abstract
// model test after it.
type shiftCountCase struct {
	name  string
	kind  ssa.OpKind
	val   int64
	count int64
	width int8
}

// Counts at/over the width are where the two maskings diverge; the in-range
// pair are controls; the i64 trio catches the inverse mistake, a 32-bit form
// applied to a 64-bit shift (which narrows the mask AND truncates the operand).
var shiftCountCases = []shiftCountCase{
	{"shl_i32_count_124", ssa.OpShl, 460, 124, 0},
	{"shl_i32_count_33", ssa.OpShl, 460, 33, 0},
	{"shl_i32_count_32", ssa.OpShl, 1, 32, 0},
	{"shl_i32_count_neg1", ssa.OpShl, 1, -1, 0},
	{"shr_i32_count_33", ssa.OpShr, -8, 33, 0},
	{"shr_i32_count_40", ssa.OpShr, 0x1234, 40, 0},
	{"shru_i32_count_33", ssa.OpShrU, -8, 33, 0},
	{"shl_i32_count_1", ssa.OpShl, 460, 1, 0},
	{"shr_i32_count_1", ssa.OpShr, -8, 1, 0},
	{"shl_i64_count_33", ssa.OpShl, 1, 33, 64},
	{"shl_i64_count_96", ssa.OpShl, 1, 96, 64},
	{"shr_i64_count_33", ssa.OpShr, 1 << 40, 33, 64},
}

// A shift masks its count to the WIDTH OF THE OPERATION — 5 bits at i32, 6 at
// i64 — and x86 masks to the width of the shift's destination register. So a
// 32-bit shift rendered on the full 64-bit register silently becomes a 6-bit
// mask: `460 << 124` is `460 << 28` (= -1073741824) at i32, but `460 << 60` on
// the full register, whose low 32 bits are 0.
//
// shl/sar used to take the 64-bit register on the grounds that maskFix trims
// shl's excess bits and sar wants the sign-extended operand. Both are true of
// the VALUE and say nothing about the COUNT, which is what this pins. The
// arm64-ssa sibling is TestArmRunShiftCountMasking.
func TestX86RunShiftCountMasking(t *testing.T) {
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
	for _, c := range shiftCountCases {
		t.Run(c.name, func(t *testing.T) {
			for _, n := range []int{1, 2, 8} {
				runMatchesEval(t, build(c.kind, c.val, c.count, c.width), n)
			}
		})
	}
}

// The abstract model (Run) documents itself as mirroring ssa.Eval's integer
// semantics "including i32 width masking", and it did not: binInt took no
// width at all, so it neither masked shift counts nor reinterpreted a 32-bit
// operand as u32 for the unsigned ops. A model that repeats the emitter's
// mistake reports agreement, which is the one thing it must never do.
func TestModelShiftCountMasking(t *testing.T) {
	build := func(k ssa.OpKind, val, count int64, width int8) *ssa.Func {
		f := ssa.NewFunc("s")
		e := f.NewBlock()
		konst := func(v int64) ssa.Value {
			c := constOp(f, e, v)
			e.Ops[len(e.Ops)-1].Width = width
			return c
		}
		r := f.AddOp(e, k, konst(val), konst(count))
		e.Ops[len(e.Ops)-1].Width = width
		f.SetRet(e, r)
		return f
	}
	for _, c := range shiftCountCases {
		t.Run(c.name, func(t *testing.T) {
			funcs := map[string]*ssa.Func{"s": build(c.kind, c.val, c.count, c.width)}
			moduleMatchesEval(t, funcs, "s", [][]int64{{}})
		})
	}
}

// The other half of binInt's missing width: at 32-bit width an operand with
// bit 31 set is stored sign-extended, so div_u / rem_u / shr_u must read it as
// u32 rather than dragging the high 1s into a 64-bit unsigned operation.
func TestModelUnsigned32Operands(t *testing.T) {
	cases := []struct {
		name string
		kind ssa.OpKind
		a, b int64
	}{
		{"divU_high_bit", ssa.OpDivU, -8, 3}, // 0xfffffff8 /u 3
		{"remU_high_bit", ssa.OpRemU, -8, 3}, // 0xfffffff8 %u 3
		{"shrU_high_bit", ssa.OpShrU, -8, 4}, // 0xfffffff8 >>u 4
		{"divU_high_divisor", ssa.OpDivU, -1, -2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := ssa.NewFunc("u")
			e := f.NewBlock()
			f.SetRet(e, f.AddOp(e, c.kind, constOp(f, e, c.a), constOp(f, e, c.b)))
			moduleMatchesEval(t, map[string]*ssa.Func{"u": f}, "u", [][]int64{{}})
		})
	}
}
