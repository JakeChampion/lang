package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// A constant the allocator spills is written into each reader's scratch
// register instead of being stored to a slot and reloaded, when every reader
// takes it through materialize; a reader that reads homes directly, here a
// call argument, keeps the slot.
func TestSpilledConstantIsRematerialised(t *testing.T) {
	// With one allocatable register the long-lived constants spill: c is
	// defined first and read last, after a run of adds that need the register.
	f := ssa.NewFunc("f")
	x := f.AddParam()
	b := f.NewBlock()
	// c is read as a LEFT operand so it is not folded into an immediate
	// form; the string is read through a byte load of its first byte.
	c := constOp(f, b, 1000)
	s := constStr(f, b, "abc")
	acc := x
	for i := 0; i < 4; i++ {
		acc = f.AddOp(b, ssa.OpAdd, acc, f.AddOp(b, ssa.OpMul, acc, x))
	}
	acc = f.AddOp(b, ssa.OpSub, c, acc)
	f.SetRet(b, f.AddOp(b, ssa.OpAdd, acc, f.AddOp(b, ssa.OpLoad8U, s)))

	prog, err := Emit(f, 1)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	// Each constant is written once, after the last multiply and directly
	// into its reader: at the use, not at a definition ahead of the adds
	// that spill it, and with no slot store following it.
	var insts []Inst
	for _, blk := range prog.Blocks {
		insts = append(insts, blk.Insts...)
	}
	lastMul := -1
	for i, in := range insts {
		if in.Op == BinOp && in.K == ssa.OpMul {
			lastMul = i
		}
	}
	for _, want := range []struct {
		name string
		is   func(Inst) bool
	}{
		{"the integer", func(in Inst) bool { return in.Op == MovImm && in.Imm == 1000 }},
		{"the string", func(in Inst) bool { return in.Op == ConstStr }},
	} {
		at := []int{}
		for i, in := range insts {
			if want.is(in) {
				at = append(at, i)
			}
		}
		if len(at) != 1 || at[0] < lastMul {
			t.Errorf("%s constant is written at %v with the last multiply at %d; want once, after it", want.name, at, lastMul)
			continue
		}
		if next := insts[at[0]+1]; next.Op == StoreSlot {
			t.Errorf("%s constant is stored to a slot after being written", want.name)
		}
	}
	runModuleMatchesEval(t, map[string]*ssa.Func{"f": f}, "f", 1, []int64{3})

	// The same constant passed to a call is read as a location, so it keeps
	// its slot and its definition.
	g := ssa.NewFunc("g")
	gp := g.AddParam()
	gb := g.NewBlock()
	g.SetRet(gb, gp)
	h := ssa.NewFunc("h")
	hx := h.AddParam()
	hb := h.NewBlock()
	hc := constOp(h, hb, 1000)
	hacc := hx
	for i := 0; i < 4; i++ {
		hacc = h.AddOp(hb, ssa.OpAdd, hacc, h.AddOp(hb, ssa.OpMul, hacc, hx))
	}
	h.SetRet(hb, h.AddOp(hb, ssa.OpAdd, hacc, callOp(h, hb, "g", hc)))
	hprog, err := Emit(h, 1)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	stores := 0
	for _, blk := range hprog.Blocks {
		for _, in := range blk.Insts {
			if in.Op == StoreSlot {
				stores++
			}
		}
	}
	if stores == 0 {
		t.Errorf("a spilled constant read by a call argument has no slot store")
	}
	runModuleMatchesEval(t, map[string]*ssa.Func{"g": g, "h": h}, "h", 1, []int64{3})
}
