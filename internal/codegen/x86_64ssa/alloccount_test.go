package x86_64ssa

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// The allocation count (#9596) is the observable a `fip` claim rests on: a
// steady state that recycles blocks moves it and leaves the bump cursor alone,
// so it is the only one that can tell "allocates nothing" from "allocates and
// recycles". These pin the two halves this backend owes it — that every block
// it hands out is counted, and that a module which never reads the count is
// shaped exactly as it was before the counter existed.

// Both allocation paths count. The first request bumps (the list is empty) and
// the second, after the block is released, pops — the shape the bump mark
// cannot see.
func TestHeapAllocCountCountsPopsAndBumpsAlike(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	before := wideCallOp(f, e, "__fern_heap_alloc_count")
	block := wideCallOp(f, e, "__alloc", constOp(f, e, 32))
	callOp(f, e, "__free", block, constOp(f, e, 32))
	callOp(f, e, "__alloc", constOp(f, e, 32)) // the pop: no bump
	after := wideCallOp(f, e, "__fern_heap_alloc_count")
	f.SetRet(e, f.AddOp(e, ssa.OpSub, after, before))
	if got := assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil); got != 2 {
		t.Errorf("two allocations counted %d, want 2 — a pop is a block handed out too", got)
	}
}

// A module that never reads the count carries neither the counter nor the tick,
// so its text is what it was before the observable existed. The inline
// allocation fast path is also still available to it: only a counting module
// gives that up, and only because a block popped inline is a block the counter
// never saw.
func TestHeapAllocCountAbsentLeavesTheTextAlone(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	f.SetRet(e, allocOp(f, e, 24))
	asm, err := EmitAsmModule(map[string]*ssa.Func{"main": f}, "main", 8, nil)
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	if strings.Contains(asm, allocCountSym) {
		t.Errorf("a module that never reads the count still names %s:\n%s", allocCountSym, asm)
	}
	if !strings.Contains(asm, "mov r11, [") {
		t.Errorf("the inline pop is gone from a module that does not count:\n%s", asm)
	}
}

// A counting module ticks once per allocation and nowhere else: the tick lives
// in __alloc, and the inline pop that would bypass it is declined.
func TestHeapAllocCountPutsOneTickInTheAllocator(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	alloc := allocOp(f, e, 24) // a constant size — inlinable, were it not counting
	callOp(f, e, "__free", alloc, constOp(f, e, 24))
	f.SetRet(e, wideCallOp(f, e, "__fern_heap_alloc_count"))
	asm, err := EmitAsmModule(map[string]*ssa.Func{"main": f}, "main", 8, nil)
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	if got := strings.Count(asm, "add qword ptr [rip + "+allocCountSym+"], 1"); got != 1 {
		t.Errorf("found %d ticks, want exactly 1 (in __alloc):\n%s", got, asm)
	}
	if !strings.Contains(asm, "\n"+allocCountSym+":") {
		t.Errorf("the counter is read but never reserved:\n%s", asm)
	}
	if strings.Contains(asm, "mov r11, [") {
		t.Errorf("a counting module still pops inline, so those blocks go uncounted:\n%s", asm)
	}
}
