package ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// A function the lift cannot model is reported, not dropped. Every
// hand-written copy of this loop swallowed the error, which makes a
// coverage hole indistinguishable from a clean answer.
func TestLiftProgramReportsWhatItCouldNotLift(t *testing.T) {
	p := &ir.Program{Funcs: []*ir.Func{
		{Name: "ok", PtrW: 8, Ops: []ir.Op{{Kind: ir.OpConstI32, I32: 1}, {Kind: ir.OpDrop}}},
		{Name: "broken", PtrW: 8, Ops: []ir.Op{{Kind: ir.OpDrop}}},
	}}
	funcs, failed := LiftProgram(p)
	if _, ok := funcs["ok"]; !ok {
		t.Error("the liftable function is missing from the result")
	}
	if len(failed) != 1 || failed[0].Func != "broken" {
		t.Errorf("want one reported failure for `broken`, got %+v", failed)
	}
	if _, ok := funcs["broken"]; ok {
		t.Error("a function that failed to lift was returned anyway")
	}
}

// ResolveWidths is skipped at ptrW 4: a wasm32 pointer is an i32, so
// widening one is wrong rather than merely unnecessary.
func TestLiftProgramDoesNotWidenAWasm32Lowering(t *testing.T) {
	body := []ir.Op{{Kind: ir.OpConstI32, I32: 8}, {Kind: ir.OpAlloc}, {Kind: ir.OpDrop}}
	wide := &ir.Program{Funcs: []*ir.Func{{Name: "f", PtrW: 8, Ops: body}}}
	narrow := &ir.Program{Funcs: []*ir.Func{{Name: "f", PtrW: 4, Ops: body}}}

	if !anyAddrOp(t, wide) {
		t.Error("no op was marked as an address at ptrW 8 — ResolveWidths did not run")
	}
	if anyAddrOp(t, narrow) {
		t.Error("an op was marked as an address at ptrW 4 — ResolveWidths ran on wasm32")
	}
}

func anyAddrOp(t *testing.T, p *ir.Program) bool {
	t.Helper()
	funcs, failed := LiftProgram(p)
	if len(failed) != 0 {
		t.Fatalf("lift failed: %+v", failed)
	}
	for _, f := range funcs {
		for _, b := range f.Blocks {
			for _, o := range b.Ops {
				if o.Addr {
					return true
				}
			}
		}
	}
	return false
}
