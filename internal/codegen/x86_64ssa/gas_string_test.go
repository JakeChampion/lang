package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// A string literal materialised in .rodata: length + byte reads, run natively.
// "Hello": len 5 + 'H'(72) + 'e'(101) = 178.
func TestAsmRunConstString(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("s")
		e := f.NewBlock()
		s := constStr(f, e, "Hello")
		l := f.AddOp(e, ssa.OpConstStringLen, s)
		b0 := loadMem(f, e, s, 0, ssa.OpLoad8U)
		b1 := loadMem(f, e, s, 1, ssa.OpLoad8U)
		sum := f.AddOp(e, ssa.OpAdd, f.AddOp(e, ssa.OpAdd, l, b0), b1)
		f.SetRet(e, sum)
		return f
	}
	for _, n := range []int{1, 2, 8} {
		runMatchesEval(t, build(), n)
	}
}

// The empty string still yields a valid pointer and length 0.
func TestAsmRunEmptyString(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("s")
		e := f.NewBlock()
		s := constStr(f, e, "")
		f.SetRet(e, f.AddOp(e, ssa.OpConstStringLen, s))
		return f
	}
	for _, n := range []int{1, 2, 8} {
		runMatchesEval(t, build(), n) // 0
	}
}

// Two distinct literals coexist and read back independently.
func TestAsmRunTwoStrings(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("s")
		e := f.NewBlock()
		a := constStr(f, e, "AB") // 'A'=65
		b := constStr(f, e, "xy") // 'x'=120
		ba := loadMem(f, e, a, 0, ssa.OpLoad8U)
		bb := loadMem(f, e, b, 0, ssa.OpLoad8U)
		f.SetRet(e, f.AddOp(e, ssa.OpAdd, ba, bb)) // 185
		return f
	}
	for _, n := range []int{1, 2, 8} {
		runMatchesEval(t, build(), n)
	}
}

// A string passed to a callee: the pointer survives the call ABI and its bytes
// are readable there. main passes "AB" to sumBytes, which returns 'A'+'B'=131.
func TestAsmRunStringAcrossCall(t *testing.T) {
	sum := ssa.NewFunc("sumBytes")
	ptr := sum.AddParam()
	_ = sum.AddParam() // len, unused
	se := sum.NewBlock()
	b0 := loadMem(sum, se, ptr, 0, ssa.OpLoad8U)
	b1 := loadMem(sum, se, ptr, 1, ssa.OpLoad8U)
	sum.SetRet(se, sum.AddOp(se, ssa.OpAdd, b0, b1))

	main := ssa.NewFunc("main")
	me := main.NewBlock()
	s := constStr(main, me, "AB")
	l := main.AddOp(me, ssa.OpConstStringLen, s)
	main.SetRet(me, callOp(main, me, "sumBytes", s, l))

	funcs := map[string]*ssa.Func{"sumBytes": sum, "main": main}
	for _, n := range []int{1, 2, 8} {
		runModuleMatchesEval(t, funcs, "main", n, nil) // 131
	}
}
