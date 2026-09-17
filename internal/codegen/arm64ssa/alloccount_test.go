package arm64ssa_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/codegen/arm64ssa"
	"github.com/jakechampion/lang/internal/ssa"
)

// The allocation count (#9596), this backend's half. The counter is the leak
// census's own (`__ssa_lc_alloc_count`), so the two readings cannot drift; what
// is new is that a module reading __heap_alloc_count() gets the ticks without
// FERN_LEAKCHECK and without the exit report. Spelled out rather than taken
// from the unexported constant, because these tests are outside the package —
// and a rename that broke the tick would then be caught here.
const allocCountSym = "__ssa_lc_alloc_count"

// Both allocation paths count. The first request bumps (the guard's tick) and
// the second, after the block is released, pops (the allocator's tick) — the
// shape the bump mark cannot see, and the one this backend counts at a
// different seam from the other.
func TestHeapAllocCountCountsPopsAndBumpsAlike(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	before := wideCallOp(f, e, "__fern_heap_alloc_count")
	block := wideCallOp(f, e, "__alloc", constOp(f, e, 32))
	callOp(f, e, "__free", block, constOp(f, e, 32))
	callOp(f, e, "__alloc", constOp(f, e, 32)) // the pop: no bump
	after := wideCallOp(f, e, "__fern_heap_alloc_count")
	f.SetRet(e, f.AddOp(e, ssa.OpSub, after, before))
	if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 8); got != 2 {
		t.Errorf("two allocations counted %d, want 2 — a pop is a block handed out too", got)
	}
}

// A module that never reads the count carries neither the counter nor the tick,
// so its text is what it was before the observable existed — and it keeps the
// inline allocation fast path, which only a counting module gives up.
func TestHeapAllocCountAbsentLeavesTheTextAlone(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	f.SetRet(e, allocOp(f, e, 24))
	asm, err := arm64ssa.EmitAsmModule(map[string]*ssa.Func{"main": f}, "main", 8, nil)
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	if strings.Contains(asm, allocCountSym) {
		t.Errorf("a module that never reads the count still names %s:\n%s", allocCountSym, asm)
	}
}

// A counting module reserves the counter and ticks it in both places a block
// can come from — the bump guard and the allocator's freelist pop.
func TestHeapAllocCountReservesAndTicksTheCounter(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	alloc := allocOp(f, e, 24) // a constant size — inlinable, were it not counting
	callOp(f, e, "__free", alloc, constOp(f, e, 24))
	f.SetRet(e, wideCallOp(f, e, "__fern_heap_alloc_count"))
	asm, err := arm64ssa.EmitAsmModule(map[string]*ssa.Func{"main": f}, "main", 8, nil)
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	if !strings.Contains(asm, "\n"+allocCountSym+":") {
		t.Errorf("the counter is read but never reserved:\n%s", asm)
	}
	// Two ticks: the guard's (every bump) and the allocator's (every pop).
	// Neither alone covers a block handed out.
	if got := strings.Count(asm, "add x1, x1, #:lo12:"+allocCountSym); got < 1 {
		t.Errorf("the allocator never addresses the counter:\n%s", asm)
	}
	if got := strings.Count(asm, "adrp x0, "+allocCountSym); got != 1 {
		t.Errorf("the bump guard ticks %d times, want 1:\n%s", got, asm)
	}
}
