package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// findCall returns the first Call inst in the emitted program.
func findCall(p *Program) (Inst, bool) {
	for _, blk := range p.Blocks {
		for _, in := range blk.Insts {
			if in.Op == Call {
				return in, true
			}
		}
	}
	return Inst{}, false
}

// A call whose operands die at the call and whose result is returned has nothing
// live across it, so the call-clobber-aware save set is empty — no caller-saved
// register is spilled around the call.
func TestCallSaveEmptyWhenNothingLiveAcross(t *testing.T) {
	main := ssa.NewFunc("main")
	e := main.NewBlock()
	r := callOp(main, e, "add", constOp(main, e, 3), constOp(main, e, 4))
	main.SetRet(e, r)

	prog, err := Emit(main, 8)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	call, ok := findCall(prog)
	if !ok {
		t.Fatal("no Call inst emitted")
	}
	if !call.SaveRegsSet {
		t.Fatal("SaveRegsSet = false, want computed save set")
	}
	if len(call.SaveRegs) != 0 {
		t.Errorf("SaveRegs = %v, want empty (nothing live across the call)", call.SaveRegs)
	}
}

// A param used after the call is live across it, so its register home must be in
// the save set (else the callee could clobber it and the post-call add would read
// garbage).
func TestCallSaveKeepsLiveAcross(t *testing.T) {
	main := ssa.NewFunc("main")
	x := main.AddParam()
	e := main.NewBlock()
	r := callOp(main, e, "add", constOp(main, e, 3), constOp(main, e, 4))
	main.SetRet(e, main.AddOp(e, ssa.OpAdd, r, x)) // x live across the call

	prog, err := Emit(main, 8)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	call, ok := findCall(prog)
	if !ok {
		t.Fatal("no Call inst emitted")
	}
	if !call.SaveRegsSet {
		t.Fatal("SaveRegsSet = false, want computed save set")
	}
	// x's home is param 0. If it landed on a caller-saved register it must be in
	// the save set (else the callee could clobber it before the post-call add).
	xLoc := prog.ParamLocs[0]
	if xLoc.IsReg && isCallerSaved(xLoc.Reg) {
		found := false
		for _, r := range call.SaveRegs {
			if r == xLoc.Reg {
				found = true
			}
		}
		if !found {
			t.Errorf("x in caller-saved reg %d not in SaveRegs %v", xLoc.Reg, call.SaveRegs)
		}
	}
}
