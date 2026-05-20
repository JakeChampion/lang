package ssa

import "testing"

// TestLICMHoistsInvariantAdd — `a + b` inside a while loop
// where a, b are loop-invariant (defined in entry) hoists to
// the preheader.
//
// CFG:
//
//	entry: def a, b; br header
//	header: brif cond, body, done
//	body: tmp = add a, b; br header     ← tmp gets hoisted
//	done: ret
func TestLICMHoistsInvariantAdd(t *testing.T) {
	f := NewFunc("f")
	cond := f.AddParam()
	entry := f.NewBlock()
	header := f.NewBlock()
	body := f.NewBlock()
	done := f.NewBlock()
	a := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 2
	b := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 3
	f.SetBr(entry, header)
	f.SetBrIf(header, cond, body, done)
	tmp := f.AddOp(body, OpAdd, a, b)
	_ = tmp
	f.SetBr(body, header)
	f.SetRet(done, Value{})

	preEntryOps := len(entry.Ops)
	hoisted := LICM(f)

	if hoisted != 1 {
		t.Errorf("hoisted = %d, want 1", hoisted)
	}
	if len(body.Ops) != 0 {
		t.Errorf("body.Ops = %d, want 0 (Add hoisted)", len(body.Ops))
	}
	if len(entry.Ops) != preEntryOps+1 {
		t.Errorf("entry.Ops = %d, want %d (Add moved here)", len(entry.Ops), preEntryOps+1)
	}
	if err := Verify(f); err != nil {
		t.Errorf("Verify post-LICM: %v", err)
	}
}

// TestLICMLeavesIterationDependent — `i + 1` in body where i
// is a loop-header phi is loop-variant; not hoisted.
func TestLICMLeavesIterationDependent(t *testing.T) {
	f := NewFunc("f")
	cond := f.AddParam()
	entry := f.NewBlock()
	header := f.NewBlock()
	body := f.NewBlock()
	done := f.NewBlock()

	zero := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0
	f.SetBr(entry, header)

	// Pre-mint the phi result so body can reference it as a
	// back-edge value.
	phiRes := f.NewValue()
	phiOp := &Op{Kind: OpPhi, Result: phiRes, Args: []Value{zero, Value{}}}
	header.Ops = append(header.Ops, phiOp)
	f.SetBrIf(header, cond, body, done)

	one := f.AddOp(body, OpConstInt)
	body.Ops[0].Imm = 1
	inc := f.AddOp(body, OpAdd, phiRes, one)
	phiOp.Args[1] = inc
	f.SetBr(body, header)
	f.SetRet(done, phiRes)

	hoisted := LICM(f)

	// inc (the Add) depends on phiRes (defined inside the
	// loop), so it is NOT loop-invariant and must stay. The
	// const 1, on the other hand, has no operands and IS
	// trivially invariant — it should hoist out. So we expect
	// exactly 1 hoist and the Add still in body.
	if hoisted != 1 {
		t.Errorf("hoisted = %d, want 1 (const 1 alone hoists; add stays)",
			hoisted)
	}
	if len(body.Ops) != 1 {
		t.Errorf("body.Ops = %d, want 1 (add stays in body)",
			len(body.Ops))
	}
	if body.Ops[0].Kind != OpAdd {
		t.Errorf("surviving body op kind = %v, want OpAdd",
			body.Ops[0].Kind)
	}
}

// TestLICMSkipsDiv — `a / b` (where a, b are loop-invariant)
// is NOT hoisted because Div can trap; lifting it out would
// cause the trap on a path where the loop never iterates.
func TestLICMSkipsDiv(t *testing.T) {
	f := NewFunc("f")
	cond := f.AddParam()
	entry := f.NewBlock()
	header := f.NewBlock()
	body := f.NewBlock()
	done := f.NewBlock()
	a := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 10
	b := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 2
	f.SetBr(entry, header)
	f.SetBrIf(header, cond, body, done)
	f.AddOp(body, OpDiv, a, b)
	f.SetBr(body, header)
	f.SetRet(done, Value{})

	hoisted := LICM(f)
	if hoisted != 0 {
		t.Errorf("hoisted = %d, want 0 (Div can trap)", hoisted)
	}
	if len(body.Ops) != 1 {
		t.Errorf("body.Ops = %d, want 1 (Div stays)", len(body.Ops))
	}
}

// TestLICMSkipsImpure — Load in body isn't hoisted (could be
// reading a memory location the loop body writes elsewhere).
func TestLICMSkipsImpure(t *testing.T) {
	f := NewFunc("f")
	cond := f.AddParam()
	ptr := f.AddParam()
	entry := f.NewBlock()
	header := f.NewBlock()
	body := f.NewBlock()
	done := f.NewBlock()
	f.SetBr(entry, header)
	f.SetBrIf(header, cond, body, done)
	f.AddOp(body, OpLoad, ptr)
	f.SetBr(body, header)
	f.SetRet(done, Value{})

	hoisted := LICM(f)
	if hoisted != 0 {
		t.Errorf("hoisted = %d, want 0 (Load is impure)", hoisted)
	}
}

// TestLICMNoLoopsNoOp — a straight-line function has nothing
// to hoist.
func TestLICMNoLoopsNoOp(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	entry := f.NewBlock()
	f.AddOp(entry, OpAdd, a, a)
	f.SetRet(entry, Value{})

	hoisted := LICM(f)
	if hoisted != 0 {
		t.Errorf("hoisted = %d, want 0 (no loops)", hoisted)
	}
}

// TestLICMChainOfInvariant — two ops where the second depends
// on the first; both invariant. Both hoist in order.
func TestLICMChainOfInvariant(t *testing.T) {
	f := NewFunc("f")
	cond := f.AddParam()
	entry := f.NewBlock()
	header := f.NewBlock()
	body := f.NewBlock()
	done := f.NewBlock()
	a := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 2
	b := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 3
	f.SetBr(entry, header)
	f.SetBrIf(header, cond, body, done)
	sum := f.AddOp(body, OpAdd, a, b)
	f.AddOp(body, OpMul, sum, sum) // depends on sum, which is invariant
	f.SetBr(body, header)
	f.SetRet(done, Value{})

	hoisted := LICM(f)
	if hoisted != 2 {
		t.Errorf("hoisted = %d, want 2 (Add + Mul both invariant)", hoisted)
	}
	if len(body.Ops) != 0 {
		t.Errorf("body.Ops = %d, want 0", len(body.Ops))
	}
	if err := Verify(f); err != nil {
		t.Errorf("Verify post-LICM: %v", err)
	}
}

// TestLICMNilFunc — defensive.
func TestLICMNilFunc(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("LICM(nil) panicked: %v", r)
		}
	}()
	LICM(nil)
}
