package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// storeNOp appends a store of `val` to base+offset with the given kind.
func storeMem(f *ssa.Func, b *ssa.Block, base ssa.Value, offset int64, val ssa.Value, kind ssa.OpKind) {
	f.AddOpNoResult(b, kind, base, val)
	b.Ops[len(b.Ops)-1].Imm = offset
}

func allocOp(f *ssa.Func, b *ssa.Block, size int64) ssa.Value {
	return f.AddOp(b, ssa.OpAlloc, constOp(f, b, size))
}

func loadMem(f *ssa.Func, b *ssa.Block, base ssa.Value, offset int64, kind ssa.OpKind) ssa.Value {
	v := f.AddOp(b, kind, base)
	b.Ops[len(b.Ops)-1].Imm = offset
	return v
}

// Full-word store/load round-trip through the real-asm bump heap: alloc 16,
// store 42 at +0 and 7 at +8, return mem[0] + mem[8] = 49.
func TestAsmRunMemRoundTrip(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("m")
		e := f.NewBlock()
		p := allocOp(f, e, 16)
		storeMem(f, e, p, 0, constOp(f, e, 42), ssa.OpStore)
		storeMem(f, e, p, 8, constOp(f, e, 7), ssa.OpStore)
		lo := loadMem(f, e, p, 0, ssa.OpLoad)
		hi := loadMem(f, e, p, 8, ssa.OpLoad)
		f.SetRet(e, f.AddOp(e, ssa.OpAdd, lo, hi))
		return f
	}
	for _, n := range []int{1, 2, 8} {
		runMatchesEval(t, build(), n)
	}
}

// Sub-word access: store bytes, read one back zero- and sign-extended.
func TestAsmRunMemSubWord(t *testing.T) {
	// alloc 4, store byte 0xFF at +0, load8u -> 255.
	u := func() *ssa.Func {
		f := ssa.NewFunc("u")
		e := f.NewBlock()
		p := allocOp(f, e, 4)
		storeMem(f, e, p, 0, constOp(f, e, 0xFF), ssa.OpStore8)
		v := loadMem(f, e, p, 0, ssa.OpLoad8U)
		setLastWidth(e, 32)
		f.SetRet(e, v)
		return f
	}
	// store byte 0xFF, load8s -> -1 (0xFF sign-extended).
	s := func() *ssa.Func {
		f := ssa.NewFunc("s")
		e := f.NewBlock()
		p := allocOp(f, e, 4)
		storeMem(f, e, p, 0, constOp(f, e, 0xFF), ssa.OpStore8)
		v := loadMem(f, e, p, 0, ssa.OpLoad8S)
		setLastWidth(e, 32)
		f.SetRet(e, v)
		return f
	}
	// halfword store/load16u.
	h := func() *ssa.Func {
		f := ssa.NewFunc("h")
		e := f.NewBlock()
		p := allocOp(f, e, 4)
		storeMem(f, e, p, 0, constOp(f, e, 0x1234), ssa.OpStore16)
		v := loadMem(f, e, p, 0, ssa.OpLoad16U)
		setLastWidth(e, 32)
		f.SetRet(e, v)
		return f
	}
	for _, n := range []int{1, 2, 8} {
		runMatchesEval(t, u(), n)
		runMatchesEval(t, s(), n)
		runMatchesEval(t, h(), n)
	}
}

// A byte array summed via a loop: store i at buf[i] for i in 0..4, sum them.
// Exercises a computed address (base + i) with a runtime index.
func TestAsmRunMemByteArraySum(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("sum")
		e := f.NewBlock()
		p := allocOp(f, e, 8)
		// buf[0..3] = 10,20,30,40
		storeMem(f, e, p, 0, constOp(f, e, 10), ssa.OpStore8)
		storeMem(f, e, p, 1, constOp(f, e, 20), ssa.OpStore8)
		storeMem(f, e, p, 2, constOp(f, e, 30), ssa.OpStore8)
		storeMem(f, e, p, 3, constOp(f, e, 40), ssa.OpStore8)
		a := loadMem(f, e, p, 0, ssa.OpLoad8U)
		b := loadMem(f, e, p, 1, ssa.OpLoad8U)
		c := loadMem(f, e, p, 2, ssa.OpLoad8U)
		d := loadMem(f, e, p, 3, ssa.OpLoad8U)
		s1 := f.AddOp(e, ssa.OpAdd, a, b)
		s2 := f.AddOp(e, ssa.OpAdd, c, d)
		f.SetRet(e, f.AddOp(e, ssa.OpAdd, s1, s2))
		return f
	}
	for _, n := range []int{1, 2, 8} {
		runMatchesEval(t, build(), n) // 100
	}
}

// The heap is shared across a call: alloc+store in a callee, read the pointer
// back in the caller. mk() allocs a cell holding 99 and returns the pointer;
// main() loads it. Validates the single shared bump cursor across the call ABI.
func TestAsmRunMemAcrossCall(t *testing.T) {
	mk := ssa.NewFunc("mk")
	me := mk.NewBlock()
	p := allocOp(mk, me, 8)
	storeMem(mk, me, p, 0, constOp(mk, me, 99), ssa.OpStore)
	mk.SetRet(me, p)

	main := ssa.NewFunc("main")
	be := main.NewBlock()
	ptr := callOp(main, be, "mk")
	main.SetRet(be, loadMem(main, be, ptr, 0, ssa.OpLoad))

	funcs := map[string]*ssa.Func{"mk": mk, "main": main}
	for _, n := range []int{1, 2, 8} {
		runModuleMatchesEval(t, funcs, "main", n, nil) // 99
	}
}
