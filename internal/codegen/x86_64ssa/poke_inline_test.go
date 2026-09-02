package x86_64ssa

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// voidPokeCall adds a result-less direct call — the shape a raw store arrives in.
func voidPokeCall(f *ssa.Func, b *ssa.Block, callee string, args ...ssa.Value) {
	f.AddOpNoResult(b, ssa.OpCall, args...).Str = callee
}

func pokeCall(f *ssa.Func, b *ssa.Block, callee string, wide bool, args ...ssa.Value) ssa.Value {
	v := f.AddOp(b, ssa.OpCall, args...)
	op := b.Ops[len(b.Ops)-1]
	op.Str = callee
	if wide {
		op.Width, op.Addr = 64, true
	}
	return v
}

// pokeModule: allocate, write and read back through the raw intrinsics
// core/map.fern is written on. 41 + 8 (__ptr_width) + the two halves of a value
// only an 8-byte load can carry back = 56.
func pokeModule() map[string]*ssa.Func {
	f := ssa.NewFunc("main")
	b := f.NewBlock()
	p := f.AddOp(b, ssa.OpAlloc, constOp(f, b, 32))
	voidPokeCall(f, b, "__store_i32", p, constOp(f, b, 41))
	narrow := f.AddOp(b, ssa.OpAdd, pokeCall(f, b, "__load_i32", false, p), pokeCall(f, b, "__ptr_width", false))

	q := f.AddOp(b, ssa.OpAlloc, constOp(f, b, 32))
	val := constOp(f, b, 2<<32|5)
	b.Ops[len(b.Ops)-1].Width = 64
	voidPokeCall(f, b, "__store_ptr", q, val)
	wide := pokeCall(f, b, "__load_ptr", true, q)
	hi := f.AddOp(b, ssa.OpShrU, wide, constOp(f, b, 32))
	b.Ops[len(b.Ops)-1].Width = 64
	lo := f.AddOp(b, ssa.OpAnd, wide, constOp(f, b, 255))
	f.SetRet(b, f.AddOp(b, ssa.OpAdd, narrow, f.AddOp(b, ssa.OpAdd, hi, lo)))
	return map[string]*ssa.Func{"main": f}
}

// This backend had no emitter for the raw pokes at all, so a program using one
// was refused. They are one instruction each, so they inline at the call site
// like the array index does — which both gives them a lowering and spares the
// caller-saves a call would have cost.
func TestRawPokesAreInlinedNotCalled(t *testing.T) {
	asm, err := EmitAsmModule(pokeModule(), "main", 8, nil)
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	for _, gone := range []string{"__load_i32", "__store_i32", "__ptr_width", "__load_ptr", "__store_ptr"} {
		if strings.Contains(asm, "call fn_"+gone) {
			t.Errorf("%s still emitted as a call", gone)
		}
	}
}

func TestRawPokeRoundTripsThroughMemory(t *testing.T) {
	for _, n := range []int{2, 4, 8} {
		if got := assembleRunModule(t, pokeModule(), "main", n, nil); got != 41+8+2+5 {
			t.Errorf("nAlloc=%d poke round-trip = %d, want %d", n, got, 41+8+2+5)
		}
	}
}
