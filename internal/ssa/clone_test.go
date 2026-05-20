package ssa

import "testing"

// TestCloneSimple — Clone of a single-block function reproduces
// the same structure + passes Verify.
func TestCloneSimple(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	sum := f.AddOp(entry, OpAdd, a, b)
	f.SetRet(entry, sum)

	c := f.Clone()
	if err := Verify(c); err != nil {
		t.Fatalf("Verify(clone): %v", err)
	}
	if c.String() != f.String() {
		t.Errorf("clone String() mismatch:\norig:\n%s\nclone:\n%s", f, c)
	}
	if c == f {
		t.Error("Clone returned same pointer")
	}
	if &c.Blocks[0] == &f.Blocks[0] {
		t.Error("Blocks slice shared with original")
	}
}

// TestCloneDiamond — clone of a diamond CFG preserves all
// edges + Preds.
func TestCloneDiamond(t *testing.T) {
	f := NewFunc("f")
	cond := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()
	f.SetBrIf(entry, cond, thenB, elseB)
	one := f.AddOp(thenB, OpConstInt)
	thenB.Ops[0].Imm = 1
	f.SetBr(thenB, merge)
	two := f.AddOp(elseB, OpConstInt)
	elseB.Ops[0].Imm = 2
	f.SetBr(elseB, merge)
	phi := f.AddPhi(merge, one, two)
	f.SetRet(merge, phi)

	c := f.Clone()
	if err := Verify(c); err != nil {
		t.Fatalf("Verify(clone): %v", err)
	}
	if c.String() != f.String() {
		t.Errorf("clone diverged from original")
	}

	// Pointer identity check: clone's Entry must NOT be the
	// original's Entry.
	if c.Entry == f.Entry {
		t.Error("clone.Entry is same pointer as original")
	}
	// But IDs match.
	if c.Entry.ID != f.Entry.ID {
		t.Errorf("Entry.ID = %d, want %d", c.Entry.ID, f.Entry.ID)
	}
}

// TestCloneIsolated — mutating the clone doesn't affect the
// original.
func TestCloneIsolated(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	entry := f.NewBlock()
	op := f.AddOp(entry, OpAdd, a, a)
	f.SetRet(entry, op)

	c := f.Clone()

	// Mutate the clone: append a new op.
	_ = c.AddOp(c.Blocks[0], OpSub, c.Params[0], c.Params[0])
	// Original entry should still have only one op.
	if len(f.Blocks[0].Ops) != 1 {
		t.Errorf("original Ops len = %d, want 1 (clone mutation leaked)",
			len(f.Blocks[0].Ops))
	}
	if len(c.Blocks[0].Ops) != 2 {
		t.Errorf("clone Ops len = %d, want 2", len(c.Blocks[0].Ops))
	}

	// Mutate an Op's Imm in the clone.
	c.Blocks[0].Ops[0].Imm = 42
	if f.Blocks[0].Ops[0].Imm == 42 {
		t.Error("mutating clone Op.Imm leaked to original")
	}
}

// TestCloneIDsPreserved — Value + Block IDs match across
// clone (consumers can keep external indices stable).
func TestCloneIDsPreserved(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	sum := f.AddOp(entry, OpAdd, a, b)
	f.SetRet(entry, sum)

	c := f.Clone()
	if c.Params[0].ID != f.Params[0].ID || c.Params[1].ID != f.Params[1].ID {
		t.Errorf("param IDs diverged")
	}
	if c.Blocks[0].ID != f.Blocks[0].ID {
		t.Errorf("block IDs diverged")
	}
	if c.Blocks[0].Ops[0].Result.ID != f.Blocks[0].Ops[0].Result.ID {
		t.Errorf("op result IDs diverged")
	}
	if c.nextValueID != f.nextValueID || c.nextBlockID != f.nextBlockID {
		t.Errorf("Builder counters diverged")
	}
}

// TestCloneNilFunc — Clone(nil) returns nil, doesn't panic.
func TestCloneNilFunc(t *testing.T) {
	var f *Func
	if c := f.Clone(); c != nil {
		t.Errorf("Clone(nil) = %v, want nil", c)
	}
}

// TestCloneOptimizationCompose — clone, run Optimize, compare
// to running Optimize on the original. Both should give the
// same printed form.
func TestCloneOptimizationCompose(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	entry := f.NewBlock()
	one := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0
	r := f.AddOp(entry, OpAdd, a, one) // a + 0 → a
	f.SetRet(entry, r)

	c := f.Clone()
	Optimize(c)
	Optimize(f)

	if c.String() != f.String() {
		t.Errorf("clone+Optimize diverged from orig+Optimize:\norig:\n%s\nclone:\n%s",
			f, c)
	}
}
