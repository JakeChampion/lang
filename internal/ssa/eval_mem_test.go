package ssa

import "testing"

// loadOp adds an OpLoad of base at the given byte offset.
func loadOp(f *Func, b *Block, base Value, offset int64) Value {
	v := f.AddOp(b, OpLoad, base)
	b.Ops[len(b.Ops)-1].Imm = offset
	return v
}

// storeOp adds an OpStore of val to base at the given byte offset.
func storeOp(f *Func, b *Block, base, val Value, offset int64) {
	op := f.AddOpNoResult(b, OpStore, base, val)
	op.Imm = offset
}

// alloc 16 bytes, store 42 and 100 at offsets 0 and 8, load both back, return
// the sum -> 142.
func TestEvalMemoryRoundtrip(t *testing.T) {
	f := NewFunc("mem")
	e := f.NewBlock()
	p := f.AddOp(e, OpAlloc, constIn(f, e, 16))
	storeOp(f, e, p, constIn(f, e, 42), 0)
	storeOp(f, e, p, constIn(f, e, 100), 8)
	a := loadOp(f, e, p, 0)
	b := loadOp(f, e, p, 8)
	f.SetRet(e, f.AddOp(e, OpAdd, a, b))

	got, err := Eval(f)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got != 142 {
		t.Errorf("Eval(mem) = %d, want 142", got)
	}
}

// The heap is shared across calls: main allocs a cell, a helper stores into it,
// main reads it back. setCell(ptr, v) { *ptr = v }.
func TestEvalMemorySharedAcrossCalls(t *testing.T) {
	setCell := NewFunc("setCell")
	ptr := setCell.AddParam()
	val := setCell.AddParam()
	se := setCell.NewBlock()
	storeOp(setCell, se, ptr, val, 0)
	setCell.SetRet(se, Value{}) // void

	main := NewFunc("main")
	me := main.NewBlock()
	p := main.AddOp(me, OpAlloc, constIn(main, me, 8))
	_ = callOp(main, me, "setCell", p, constIn(main, me, 77))
	main.SetRet(me, loadOp(main, me, p, 0))

	funcs := map[string]*Func{"setCell": setCell, "main": main}
	got, err := EvalIn(funcs, main)
	if err != nil {
		t.Fatalf("EvalIn: %v", err)
	}
	if got != 77 {
		t.Errorf("EvalIn(main) = %d, want 77 (heap not shared across calls?)", got)
	}
}

// Dereferencing an unallocated pointer is a clear error, not a wrong answer.
func TestEvalMemoryOutOfBounds(t *testing.T) {
	f := NewFunc("oob")
	e := f.NewBlock()
	f.SetRet(e, loadOp(f, e, constIn(f, e, 4096), 0)) // never allocated
	if _, err := Eval(f); err == nil {
		t.Error("expected an out-of-bounds error for an unallocated load")
	}
}
