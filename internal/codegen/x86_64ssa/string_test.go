package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

func constStr(f *ssa.Func, b *ssa.Block, s string) ssa.Value {
	v := f.AddOp(b, ssa.OpConstString)
	b.Ops[len(b.Ops)-1].Str = s
	return v
}

// String literal materialisation + length + byte reads, validated
// RunModule(EmitModule) == EvalIn across register counts.
func TestModuleConstString(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("s")
		e := f.NewBlock()
		s := constStr(f, e, "Hello")
		l := f.AddOp(e, ssa.OpConstStringLen, s)
		b0 := loadNOp(f, e, s, 0, ssa.OpLoad8U)
		b1 := loadNOp(f, e, s, 1, ssa.OpLoad8U)
		sum := f.AddOp(e, ssa.OpAdd, f.AddOp(e, ssa.OpAdd, l, b0), b1)
		f.SetRet(e, sum)
		return f
	}
	moduleMatchesEval(t, map[string]*ssa.Func{"s": build()}, "s", [][]int64{{}})
}

// A string passed to a callee (pointer + length) that sums its bytes — proves
// the string pointer survives a call and its bytes are readable there.
func TestModuleStringAcrossCall(t *testing.T) {
	build := func() map[string]*ssa.Func {
		// sumBytes(ptr, len): add heap[ptr+0] + heap[ptr+1] (len assumed >= 2).
		sum := ssa.NewFunc("sumBytes")
		ptr := sum.AddParam()
		_ = sum.AddParam() // len, unused in this fixed-size probe
		se := sum.NewBlock()
		b0 := loadNOp(sum, se, ptr, 0, ssa.OpLoad8U)
		b1 := loadNOp(sum, se, ptr, 1, ssa.OpLoad8U)
		sum.SetRet(se, sum.AddOp(se, ssa.OpAdd, b0, b1))

		main := ssa.NewFunc("main")
		me := main.NewBlock()
		s := constStr(main, me, "AB") // 'A'=65, 'B'=66 -> 131
		l := main.AddOp(me, ssa.OpConstStringLen, s)
		main.SetRet(me, callOp(main, me, "sumBytes", s, l))
		return map[string]*ssa.Func{"sumBytes": sum, "main": main}
	}
	moduleMatchesEval(t, build(), "main", [][]int64{{}})
}
