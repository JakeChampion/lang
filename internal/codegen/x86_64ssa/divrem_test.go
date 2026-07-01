package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// Integer div/rem validated RunModule(EmitModule) == EvalIn, across register
// counts, for signed/unsigned and positive/negative operands.
func TestModuleDivRem(t *testing.T) {
	cases := []struct {
		name string
		kind ssa.OpKind
		a, b int64
	}{
		{"div", ssa.OpDiv, 17, 5},
		{"div-neg", ssa.OpDiv, -17, 5},
		{"rem", ssa.OpRem, 17, 5},
		{"rem-neg", ssa.OpRem, -17, 5},
		{"divU", ssa.OpDivU, 100, 7},
		{"remU", ssa.OpRemU, 100, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			build := func() *ssa.Func {
				f := ssa.NewFunc("d")
				e := f.NewBlock()
				f.SetRet(e, f.AddOp(e, tc.kind, constOp(f, e, tc.a), constOp(f, e, tc.b)))
				return f
			}
			moduleMatchesEval(t, map[string]*ssa.Func{"d": build()}, "d", [][]int64{{}})
		})
	}
}
