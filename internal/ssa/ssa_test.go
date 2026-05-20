package ssa

import (
	"strings"
	"testing"
)

// TestBuildSimpleFunction — build the SSA equivalent of
//
//	function f(a, b) {
//	    return a + b;
//	}
//
// One entry block, one Add op, one Ret terminator. Verifies
// Builder mechanics + Verify-pass acceptance.
func TestBuildSimpleFunction(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	sum := f.AddOp(entry, OpAdd, a, b)
	f.SetRet(entry, sum)

	if err := Verify(f); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(f.Blocks) != 1 {
		t.Errorf("Blocks = %d, want 1", len(f.Blocks))
	}
	if f.Entry != entry {
		t.Errorf("Entry mismatch")
	}
	if len(entry.Ops) != 1 {
		t.Errorf("entry.Ops = %d, want 1", len(entry.Ops))
	}
	if entry.Ops[0].Kind != OpAdd {
		t.Errorf("Op kind = %v, want OpAdd", entry.Ops[0].Kind)
	}
	if entry.Term.Kind != TermRet {
		t.Errorf("Term kind = %v, want TermRet", entry.Term.Kind)
	}
	if entry.Term.Value != sum {
		t.Errorf("ret value = %v, want %v", entry.Term.Value, sum)
	}
}

// TestBuildBranchingFunction — build the SSA equivalent of
//
//	function f(c) {
//	    if (c) { return 1; }
//	    return 2;
//	}
//
// Entry block ends with BrIf; True branch returns 1, False
// branch returns 2. Verifies Preds tracking + multi-block
// Verify.
func TestBuildBranchingFunction(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	one := f.AddOp(thenB, OpConstInt)
	thenB.Ops[0].Imm = 1
	f.SetRet(thenB, one)
	two := f.AddOp(elseB, OpConstInt)
	elseB.Ops[0].Imm = 2
	f.SetRet(elseB, two)

	if err := Verify(f); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got := len(thenB.Preds); got != 1 || thenB.Preds[0] != entry {
		t.Errorf("thenB.Preds = %v, want [entry]", thenB.Preds)
	}
	if got := len(elseB.Preds); got != 1 || elseB.Preds[0] != entry {
		t.Errorf("elseB.Preds = %v, want [entry]", elseB.Preds)
	}
	succs := entry.Succs()
	if len(succs) != 2 || succs[0] != thenB || succs[1] != elseB {
		t.Errorf("entry.Succs() = %v, want [thenB, elseB]", succs)
	}
}

// TestVerifyRejectsEntryNotInBlocks — f.Entry pointing at a
// Block that isn't in f.Blocks would have walks (DFS, RPO,
// dom-tree) skip the entry on a list iteration. Catch that.
func TestVerifyRejectsEntryNotInBlocks(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	f.SetRet(entry, Value{})
	// Drop entry from f.Blocks but keep f.Entry pointing at it.
	f.Blocks = nil

	err := Verify(f)
	if err == nil {
		t.Fatal("expected Verify error for Entry not in Blocks")
	}
	if !strings.Contains(err.Error(), "Entry block") {
		t.Errorf("error %q does not mention Entry block", err)
	}
}

// TestVerifyRejectsBrWithNilTarget — `br` terminator with a
// nil Target is structurally invalid (downstream consumers
// would crash on the dangling pointer). Verify catches it.
func TestVerifyRejectsBrWithNilTarget(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	entry.Term = Terminator{Kind: TermBr, Target: nil}

	err := Verify(f)
	if err == nil {
		t.Fatal("expected Verify error for br with nil target")
	}
	if !strings.Contains(err.Error(), "nil target") {
		t.Errorf("error %q does not mention nil target", err)
	}
}

// TestVerifyRejectsBrIfWithNilTrue — brif with nil True
// branch is structurally invalid.
func TestVerifyRejectsBrIfWithNilTrue(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	other := f.NewBlock()
	f.SetRet(other, Value{})
	entry.Term = Terminator{Kind: TermBrIf, Cond: c, True: nil, False: other}

	err := Verify(f)
	if err == nil {
		t.Fatal("expected Verify error for brif with nil True")
	}
	if !strings.Contains(err.Error(), "nil True target") {
		t.Errorf("error %q does not mention nil True target", err)
	}
}

// TestVerifyRejectsBrIfWithNilFalse — brif with nil False
// branch is structurally invalid.
func TestVerifyRejectsBrIfWithNilFalse(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	other := f.NewBlock()
	f.SetRet(other, Value{})
	entry.Term = Terminator{Kind: TermBrIf, Cond: c, True: other, False: nil}

	err := Verify(f)
	if err == nil {
		t.Fatal("expected Verify error for brif with nil False")
	}
	if !strings.Contains(err.Error(), "nil False target") {
		t.Errorf("error %q does not mention nil False target", err)
	}
}

// TestVerifyRejectsMissingTerminator — a block with no
// terminator is structurally invalid. Verify should catch
// it with a clear error.
func TestVerifyRejectsMissingTerminator(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.AddParam()
	f.AddOp(entry, OpAdd, a, a)
	// No SetRet / SetBr — terminator stays TermInvalid.

	err := Verify(f)
	if err == nil {
		t.Fatal("expected Verify error for missing terminator")
	}
	if !strings.Contains(err.Error(), "no terminator") {
		t.Errorf("error %q does not mention missing terminator", err)
	}
}

// TestVerifyRejectsDoubleAssignment — SSA requires single-
// assignment. If two Ops produce the same Value.ID, Verify
// catches the violation.
func TestVerifyRejectsDoubleAssignment(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	entry := f.NewBlock()
	f.AddOp(entry, OpAdd, a, a)
	// Manually re-use the same Value.ID for a second Op.
	op := &Op{Kind: OpSub, Result: entry.Ops[0].Result, Args: []Value{a, a}}
	entry.Ops = append(entry.Ops, op)
	f.SetRet(entry, op.Result)

	err := Verify(f)
	if err == nil {
		t.Fatal("expected Verify error for double-assignment")
	}
	if !strings.Contains(err.Error(), "defined twice") {
		t.Errorf("error %q does not mention double definition", err)
	}
}

// TestVerifyRejectsUndefinedValue — using a Value that has
// no def site anywhere in the function is a use-before-def
// violation.
func TestVerifyRejectsUndefinedValue(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	bogus := Value{ID: 999}
	f.AddOp(entry, OpAdd, bogus, bogus)
	f.SetRet(entry, Value{})

	err := Verify(f)
	if err == nil {
		t.Fatal("expected Verify error for undefined value")
	}
	if !strings.Contains(err.Error(), "undefined value") {
		t.Errorf("error %q does not mention undefined value", err)
	}
}

// TestValueStringFormat — `v<ID>` per SSA convention, with
// `v_` for the zero sentinel.
func TestValueStringFormat(t *testing.T) {
	if got := (Value{ID: 42}).String(); got != "v42" {
		t.Errorf("Value{ID:42}.String() = %q, want %q", got, "v42")
	}
	if got := (Value{}).String(); got != "v_" {
		t.Errorf("Value{}.String() = %q, want %q", got, "v_")
	}
}

// TestOpKindString — every kind has a stable string form.
// Add ones surface as `add`, comparisons as `eq` / `lt`,
// etc. Pin so dump output stays reviewable.
func TestOpKindString(t *testing.T) {
	cases := []struct {
		k    OpKind
		want string
	}{
		{OpAdd, "add"}, {OpSub, "sub"}, {OpMul, "mul"},
		{OpAnd, "and"}, {OpOr, "or"}, {OpXor, "xor"},
		{OpShl, "shl"}, {OpShr, "shr"}, {OpNeg, "neg"},
		{OpNot, "not"}, {OpSelect, "select"},
		{OpEq, "eq"}, {OpLt, "lt"}, {OpGe, "ge"},
		{OpConstInt, "const_int"}, {OpCall, "call"},
		{OpInvalid, "invalid"},
	}
	for _, c := range cases {
		if got := c.k.String(); got != c.want {
			t.Errorf("%v.String() = %q, want %q", c.k, got, c.want)
		}
	}
}
