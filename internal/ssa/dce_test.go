package ssa

import "testing"

// TestDCEDropsUnusedAdd — an OpAdd whose Result no one reads
// is removed. The used neighbour stays.
func TestDCEDropsUnusedAdd(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	used := f.AddOp(entry, OpAdd, a, b)
	_ = f.AddOp(entry, OpMul, a, b) // result unused
	f.SetRet(entry, used)

	DCE(f)

	if len(entry.Ops) != 1 {
		t.Fatalf("Ops len = %d, want 1", len(entry.Ops))
	}
	if entry.Ops[0].Kind != OpAdd {
		t.Errorf("survivor kind = %v, want OpAdd", entry.Ops[0].Kind)
	}
	if err := Verify(f); err != nil {
		t.Errorf("Verify after DCE: %v", err)
	}
}

// TestDCEKeepsTerminatorUse — value used by Ret terminator is
// not dead.
func TestDCEKeepsTerminatorUse(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	sum := f.AddOp(entry, OpAdd, a, b)
	f.SetRet(entry, sum)

	DCE(f)

	if len(entry.Ops) != 1 || entry.Ops[0].Kind != OpAdd {
		t.Errorf("Ops = %v, want [OpAdd]", entry.Ops)
	}
}

// TestDCEKeepsBrIfCond — value used as BrIf condition is not
// dead, even if no other Op consumes it.
func TestDCEKeepsBrIfCond(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	one := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 1
	cmp := f.AddOp(entry, OpEq, x, one)
	f.SetBrIf(entry, cmp, thenB, elseB)
	two := f.AddOp(thenB, OpConstInt)
	thenB.Ops[0].Imm = 2
	f.SetRet(thenB, two)
	three := f.AddOp(elseB, OpConstInt)
	elseB.Ops[0].Imm = 3
	f.SetRet(elseB, three)

	DCE(f)

	if len(entry.Ops) != 2 {
		t.Fatalf("entry Ops = %d, want 2 (const + cmp)", len(entry.Ops))
	}
	if entry.Ops[1].Kind != OpEq {
		t.Errorf("entry.Ops[1] = %v, want OpEq (used as cond)", entry.Ops[1].Kind)
	}
}

// TestDCEKeepsSideEffectOps — Call, Load, Store stay even when
// their result has no consumers.
func TestDCEKeepsSideEffectOps(t *testing.T) {
	f := NewFunc("f")
	addr := f.AddParam()
	val := f.AddParam()
	entry := f.NewBlock()
	_ = f.AddOp(entry, OpLoad, addr) // unused load
	f.AddOpNoResult(entry, OpStore, addr, val)
	_ = f.AddOp(entry, OpCall, addr) // unused call
	f.SetRet(entry, Value{})

	DCE(f)

	if len(entry.Ops) != 3 {
		t.Fatalf("Ops = %d, want 3 (load/store/call all kept)", len(entry.Ops))
	}
	wantKinds := []OpKind{OpLoad, OpStore, OpCall}
	for i, want := range wantKinds {
		if entry.Ops[i].Kind != want {
			t.Errorf("Ops[%d].Kind = %v, want %v", i, entry.Ops[i].Kind, want)
		}
	}
}

// TestDCECascades — Op chain where dropping the tail exposes
// the next-down Op as dead; iteration drops both.
func TestDCECascades(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	first := f.AddOp(entry, OpAdd, a, b)
	second := f.AddOp(entry, OpMul, first, first) // only consumes first
	_ = f.AddOp(entry, OpSub, second, second)     // tail — unused
	f.SetRet(entry, Value{})

	DCE(f)

	if len(entry.Ops) != 0 {
		t.Errorf("Ops = %d, want 0 (entire chain dead)", len(entry.Ops))
	}
}

// TestDCEAfterFold — composed pipeline. After Fold the
// original const-int operands have no consumers (the folded
// op carries the Imm directly) so DCE sweeps them up.
func TestDCEAfterFold(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	one := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 1
	two := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 2
	sum := f.AddOp(entry, OpAdd, one, two)
	f.SetRet(entry, sum)

	Fold(f)
	DCE(f)

	if len(entry.Ops) != 1 {
		t.Fatalf("Ops = %d, want 1 (only folded sum survives)", len(entry.Ops))
	}
	if entry.Ops[0].Kind != OpConstInt || entry.Ops[0].Imm != 3 {
		t.Errorf("survivor = {%v %d}, want {OpConstInt 3}", entry.Ops[0].Kind, entry.Ops[0].Imm)
	}
}

// TestDCENilFunc — DCE(nil) is a no-op, not a panic.
func TestDCENilFunc(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("DCE(nil) panicked: %v", r)
		}
	}()
	DCE(nil)
}

// TestDCECallArgsCountAsUses — values consumed only by a Call
// stay alive even though the Call itself is side-effect-y.
func TestDCECallArgsCountAsUses(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	arg := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 42
	f.AddOp(entry, OpCall, arg)
	f.SetRet(entry, Value{})

	DCE(f)

	if len(entry.Ops) != 2 {
		t.Fatalf("Ops = %d, want 2 (const + call)", len(entry.Ops))
	}
	if entry.Ops[0].Kind != OpConstInt {
		t.Errorf("Ops[0].Kind = %v, want OpConstInt (used by Call)", entry.Ops[0].Kind)
	}
}
