package x86_64ssa

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

func emitZeroCapture(t *testing.T, times int) string {
	t.Helper()
	asm, err := EmitAsmModule(zeroCaptureModule(times), "main", 8, nil)
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	return asm
}

// A capture-free closure cell is {fn_idx, env=0, drop_idx=0, 0} — every word a
// compile-time constant, never written again — so it belongs in .rodata. It used
// to be bump-allocated at every evaluation, which for a bare function name
// handed to a helper meant a fresh 40-byte cell on every call.
func TestZeroCaptureClosureCellIsStatic(t *testing.T) {
	asm := emitZeroCapture(t, 1)
	if !strings.Contains(asm, "[rip + clo_") {
		t.Error("the cell is not materialised from a .rodata label")
	}
	if strings.Contains(asm, heapPtrSym) {
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
	asm := emitZeroCapture(t, 3)
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

// Dispatching through the static cell still reaches the target: bare(9) = 9.
func TestStaticClosureDispatches(t *testing.T) {
	if got := assembleRunModule(t, zeroCaptureModule(1), "main", 8, nil); got != 9 {
		t.Errorf("static-cell dispatch = %d, want 9", got)
	}
}
