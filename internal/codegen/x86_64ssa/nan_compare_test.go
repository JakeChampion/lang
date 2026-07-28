package x86_64ssa

import (
	"math"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// Every ordered comparison against a NaN is false; only `!=` is true. The
// renderer used to read ZF/CF alone (sete/setne/setb/setbe), which `ucomisd`
// sets identically for "unordered" and for "equal"/"below" — so four of the
// six predicates were wrong on NaN. The sibling arm64ssa backend had the same
// class of bug with the unsigned AArch64 condition codes, caught by sweeping
// the fernsmith printable corpus through `-target arm64-ssa`; this backend has
// no CLI target to sweep, so the model-vs-assembly oracle stands in.
//
// ssa.Eval's Go comparisons already have IEEE NaN semantics, and
// runMatchesEval does not run ssa.Optimize, so neither operand is folded away.
func TestAsmRunFloatCompareNaN(t *testing.T) {
	nan := math.NaN()
	build := func(k ssa.OpKind, a, b float64) *ssa.Func {
		f := ssa.NewFunc("f")
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
	// Both operand orders, plus NaN-vs-NaN: ssa/canon.go's flipDirectionalCmp
	// rewrites `a < b` to `b > a` when an operand commutes, so a mapping that
	// is wrong in only one direction still has to be caught.
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
