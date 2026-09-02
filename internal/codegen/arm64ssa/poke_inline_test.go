package arm64ssa_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/codegen/arm64ssa"
	"github.com/jakechampion/lang/internal/ssa"
)

// voidPokeCall adds a result-less direct call — the shape a raw store arrives in.
func voidPokeCall(f *ssa.Func, b *ssa.Block, callee string, args ...ssa.Value) {
	f.AddOpNoResult(b, ssa.OpCall, args...).Str = callee
}

// The raw-memory intrinsics core/map.fern reaches its kv buffer through are one
// instruction each, so a call costs far more than the work — not for the `bl`
// but for the caller-saves the allocator plants around it. A map lookup runs
// several per probe, which is what made map-shaped code the slowest thing this
// backend compiled.
func TestRawPokesAreInlinedNotCalled(t *testing.T) {
	f := ssa.NewFunc("main")
	b := f.NewBlock()
	p := addrCallOp(f, b, "__alloc", constOp(f, b, 16))
	voidPokeCall(f, b, "__store_i32", p, constOp(f, b, 41))
	sum := f.AddOp(b, ssa.OpAdd, callOp(f, b, "__load_i32", p), callOp(f, b, "__ptr_width"))
	f.SetRet(b, sum)

	asm := emitIdxAsm(t, map[string]*ssa.Func{"main": f}, "main")
	for _, gone := range []string{"__load_i32", "__store_i32", "__ptr_width"} {
		if strings.Contains(asm, "bl fn_"+gone) {
			t.Errorf("%s still emitted as a call", gone)
		}
		if strings.Contains(asm, "\nfn_"+gone+":") {
			t.Errorf("%s still has a helper body — nothing calls it", gone)
		}
	}
}

// Each poke has to reproduce its helper body exactly, width included: an
// 8-byte intrinsic that came out through a w-register would truncate a heap
// address, which on this backend's 16 GiB arena is wrong for the first
// allocation rather than only past 2 GiB.
func TestRawPokeRoundTripsThroughMemory(t *testing.T) {
	build := func() map[string]*ssa.Func {
		f := ssa.NewFunc("main")
		b := f.NewBlock()
		p := addrCallOp(f, b, "__alloc", constOp(f, b, 32))
		// i32 pair: store 41, read it back, and add __ptr_width()'s 8.
		voidPokeCall(f, b, "__store_i32", p, constOp(f, b, 41))
		narrow := f.AddOp(b, ssa.OpAdd, callOp(f, b, "__load_i32", p), callOp(f, b, "__ptr_width"))
		// 8-byte pair: a value with both halves set, so a 4-byte load reads 0
		// for the high half and the sum lands somewhere else entirely.
		q := addrCallOp(f, b, "__alloc", constOp(f, b, 32))
		val := constOp(f, b, 2<<32|5)
		b.Ops[len(b.Ops)-1].Width = 64
		voidPokeCall(f, b, "__store_ptr", q, val)
		wide := wideCallOp(f, b, "__load_ptr", q)
		hi := f.AddOp(b, ssa.OpShrU, wide, constOp(f, b, 32))
		b.Ops[len(b.Ops)-1].Width = 64 // a 32-bit lsr takes its count mod 32
		lo := f.AddOp(b, ssa.OpAnd, wide, constOp(f, b, 255))
		f.SetRet(b, f.AddOp(b, ssa.OpAdd, narrow, f.AddOp(b, ssa.OpAdd, hi, lo)))
		return map[string]*ssa.Func{"main": f}
	}
	for _, n := range []int{2, 4, arm64ssa.DefaultNumAlloc} {
		if got := assembleRunArmModule(t, build(), "main", n); got != 41+8+2+5 {
			t.Errorf("nAlloc=%d poke round-trip = %d, want %d", n, got, 41+8+2+5)
		}
	}
}
