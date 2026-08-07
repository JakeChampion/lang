package arm64ssa_test

import (
	"math"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// Every ordered comparison against a NaN is false; only `!=` is true. The
// renderer must not emit the UNSIGNED integer condition codes (lo/ls/hi/hs):
// they agree with the IEEE ones on ordered operands but read TRUE on
// unordered, since AArch64 `fcmp` marks NaN with N=0 Z=0 C=1 V=1, so `hi`
// (C && !Z) and `hs` (C) both fire. Sweeping the fernsmith printable corpus through
// `-target arm64-ssa` caught it as a stdout divergence — `0.0/0.0 <= x`
// printing "T" where the interpreter and both native backends print "F".
//
// ssa.Eval is the oracle here as everywhere else in this package; its Go
// comparisons already have IEEE NaN semantics. Neither operand is folded away
// because runMatchesEval does not run ssa.Optimize.
func TestArmRunFloatCompareNaN(t *testing.T) {
	nan := math.NaN()
	build := func(k ssa.OpKind, a, b float64) *ssa.Func {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		f.SetRet(e, f.AddOp(e, k, constFloat(f, e, a), constFloat(f, e, b)))
		return f
	}
	ops := []struct {
		name string
		k    ssa.OpKind
	}{
		{"FEq", ssa.OpFEq},
		{"FNe", ssa.OpFNe},
		{"FLt", ssa.OpFLt},
		{"FLe", ssa.OpFLe},
		{"FGt", ssa.OpFGt},
		{"FGe", ssa.OpFGe},
	}
	// Both operand orders, plus NaN-vs-NaN: the canonicaliser in ssa/canon.go
	// flips `a < b` to `b > a` when an operand commutes, so a mapping that is
	// wrong in only one direction still has to be caught.
	pairs := [][2]float64{{nan, 1.0}, {1.0, nan}, {nan, nan}}
	for _, op := range ops {
		for _, p := range pairs {
			op, p := op, p
			t.Run(op.name, func(t *testing.T) {
				for _, n := range []int{1, 2, 8} {
					runMatchesEval(t, build(op.k, p[0], p[1]), n)
				}
			})
		}
	}
}
