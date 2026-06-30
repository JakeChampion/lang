package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// Each unary op validated RunModule(EmitModule) == EvalIn over a no-param
// function returning op(const).
func TestModuleUnaryOps(t *testing.T) {
	cases := []struct {
		name string
		kind ssa.OpKind
		in   int64
	}{
		{"not0", ssa.OpNot, 0},
		{"not5", ssa.OpNot, 5},
		{"extendS", ssa.OpExtendS, 0x80000000},
		{"extendU", ssa.OpExtendU, 0x80000000},
		{"extend8S", ssa.OpExtend8S, 200},
		{"extend16S", ssa.OpExtend16S, 0xFFFF},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			build := func() *ssa.Func {
				f := ssa.NewFunc("u")
				e := f.NewBlock()
				f.SetRet(e, f.AddOp(e, tc.kind, constOp(f, e, tc.in)))
				return f
			}
			moduleMatchesEval(t, map[string]*ssa.Func{"u": build()}, "u", [][]int64{{}})
		})
	}
}

// Real assembled+run check for Not: not(0) -> 1, not(7) -> 0.
func TestAsmRunNot(t *testing.T) {
	for _, tc := range []struct{ in, want int64 }{{0, 1}, {7, 0}} {
		f := ssa.NewFunc("not")
		e := f.NewBlock()
		f.SetRet(e, f.AddOp(e, ssa.OpNot, constOp(f, e, tc.in)))
		runMatchesEval(t, f, 4)
	}
}
