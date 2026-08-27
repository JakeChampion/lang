package arm64ssa_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// bcopyProbe is a 48-byte source whose bytes are all distinct, so a weighted
// sum over a copy detects a byte landing at the wrong index as well as one
// that never arrived.
const bcopyProbe = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKL"

// weightedSum folds bytes[0:n] of s into sum((i+1) * byte) — the same value the
// SSA module below computes over the copy, so a mismatch names a length rather
// than just "wrong".
func weightedSum(s string, n int) int {
	total := 0
	for i := 0; i < n; i++ {
		total += (i + 1) * int(s[i])
	}
	return total
}

// weighByIndex adds sum((i+1) * p[i]) for i in [0, n) over a heap byte buffer.
func weighByIndex(f *ssa.Func, b *ssa.Block, p ssa.Value, n int) ssa.Value {
	acc := constOp(f, b, 0)
	for i := 0; i < n; i++ {
		w := f.AddOp(b, ssa.OpMul, load8u(f, b, p, int64(i)), constOp(f, b, int64(i+1)))
		acc = f.AddOp(b, ssa.OpAdd, acc, w)
	}
	return acc
}

// __ssa_bcopy moves 16 bytes per iteration, then one 8-byte step, then up to 7
// single bytes. Every length from 0 to 40 crosses those boundaries in a
// different combination, and a residue that copies one byte too few or too many
// is invisible at the lengths a hand-picked case would use. __str_slice is the
// shortest path to a copy of an exactly chosen length.
func TestArmRunStrSliceCopiesEveryLengthClass(t *testing.T) {
	for n := 0; n <= 40; n++ {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		sl := addrCallOp(f, e, "__str_slice", constStr(f, e, bcopyProbe), constOp(f, e, 0), constOp(f, e, int64(n)))
		f.SetRet(e, weighByIndex(f, e, sl, n))
		want := weightedSum(bcopyProbe, n) & 0xff
		if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 22); got != want {
			t.Errorf("__str_slice(probe, 0, %d) weighted sum = %d, want %d", n, got, want)
		}
	}
}

// The second operand of a concat starts at data+la, an offset __ssa_bcopy never
// sees, so an unaligned destination is only exercised when la is not a multiple
// of 8. Both operands are sliced to a chosen length first; the pairs walk la
// across the alignment classes with an lb on each side of the 16-byte bulk step.
func TestArmRunStrConcatCopiesBothOperandsAtEveryAlignment(t *testing.T) {
	for _, p := range [][2]int{{0, 0}, {0, 9}, {1, 1}, {3, 17}, {7, 8}, {8, 7}, {9, 23}, {15, 16}, {16, 1}, {17, 33}} {
		la, lb := p[0], p[1]
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		src := constStr(f, e, bcopyProbe)
		a := addrCallOp(f, e, "__str_slice", src, constOp(f, e, 0), constOp(f, e, int64(la)))
		b := addrCallOp(f, e, "__str_slice", src, constOp(f, e, 0), constOp(f, e, int64(lb)))
		f.SetRet(e, weighByIndex(f, e, addrCallOp(f, e, "__str_concat", a, b), la+lb))
		want := (weightedSum(bcopyProbe, la) + weightedSumShifted(bcopyProbe, lb, la)) & 0xff
		if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 22); got != want {
			t.Errorf("__str_concat(probe[:%d], probe[:%d]) weighted sum = %d, want %d", la, lb, got, want)
		}
	}
}

// weightedSumShifted is weightedSum for bytes that land at offset base in the
// result, so the concat's second operand carries the weights of its final
// positions.
func weightedSumShifted(s string, n, base int) int {
	total := 0
	for i := 0; i < n; i++ {
		total += (base + i + 1) * int(s[i])
	}
	return total
}
