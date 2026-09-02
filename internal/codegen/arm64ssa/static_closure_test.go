package arm64ssa_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// zeroCaptureModule: bare(x, env) = x, and a main that materialises the bare
// function as a value `times` times and dispatches the last one.
func zeroCaptureModule(times int) map[string]*ssa.Func {
	bare := ssa.NewFunc("bare")
	bx := bare.AddParam()
	bare.AddParam() // env
	be := bare.NewBlock()
	bare.SetRet(be, bx)

	main := ssa.NewFunc("main")
	me := main.NewBlock()
	var cell ssa.Value
	for i := 0; i < times; i++ {
		cell = makeClosureOp(main, me, "bare")
	}
	main.SetRet(me, callIndirectOp(main, me, cell, constOp(main, me, 9)))
	return map[string]*ssa.Func{"bare": bare, "main": main}
}

// A capture-free closure cell is {fn_idx, env=0, drop_idx=0, 0} — every word a
// compile-time constant, never written again — so it belongs in .rodata. It used
// to be bump-allocated at every evaluation, which for a bare function name
// handed to a helper (core/map passes __map_lookup_keyed its hash and eq
// functions) meant a fresh 40-byte cell on every call.
func TestZeroCaptureClosureCellIsStatic(t *testing.T) {
	asm := emitIdxAsm(t, zeroCaptureModule(1), "main")
	if !strings.Contains(asm, "adrp x") || !strings.Contains(asm, "clo_") {
		t.Error("the cell is not materialised from a .rodata label")
	}
	if strings.Contains(asm, "__ssa_heap_ptr") {
		t.Error("a capture-free closure still bump-allocates")
	}
	// The immortal rc header the string literals and enum sentinels carry: with
	// a live count, a scope-exit drop would write a read-only cell, and the
	// reuse pass could take it as an allocation token.
	if !strings.Contains(asm, ".4byte 0x80000000") {
		t.Error("the static cell has no immortal rc header")
	}
}

// One cell per target, however many times the value is materialised: a repeated
// label is a module the assembler rejects, and a per-site cell would put the
// allocation back as static bytes.
func TestStaticClosureCellEmittedOncePerTarget(t *testing.T) {
	asm := emitIdxAsm(t, zeroCaptureModule(3), "main")
	labels := map[string]int{}
	for _, l := range strings.Split(asm, "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "clo_") && strings.HasSuffix(l, ":") {
			labels[l]++
		}
	}
	if len(labels) != 1 {
		t.Errorf("got %d static closure cells, want 1: %v", len(labels), labels)
	}
	for l, n := range labels {
		if n != 1 {
			t.Errorf("label %s defined %d times", l, n)
		}
	}
}

// The cell being static is what lets a module whose only heap-shaped op is a
// capture-free closure reserve no arena at all.
func TestStaticClosureNeedsNoHeap(t *testing.T) {
	asm := emitIdxAsm(t, zeroCaptureModule(1), "main")
	if strings.Contains(asm, "__ssa_heap_guard") {
		t.Error("the module reserves a heap it never allocates from")
	}
}
