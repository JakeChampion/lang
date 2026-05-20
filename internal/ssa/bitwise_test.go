package ssa

import "testing"

// TestBitwiseFoldAnd — `c & d` (both const) → const result.
func TestBitwiseFoldAnd(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0b1100
	b := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 0b1010
	r := f.AddOp(entry, OpAnd, a, b)
	f.SetRet(entry, r)

	Fold(f)

	if got := entry.Ops[2]; got.Kind != OpConstInt || got.Imm != 0b1000 {
		t.Errorf("AND = {%v %b}, want {OpConstInt 0b1000}", got.Kind, got.Imm)
	}
}

// TestBitwiseFoldOr — `c | d`.
func TestBitwiseFoldOr(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0b1100
	b := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 0b1010
	r := f.AddOp(entry, OpOr, a, b)
	f.SetRet(entry, r)

	Fold(f)

	if got := entry.Ops[2]; got.Kind != OpConstInt || got.Imm != 0b1110 {
		t.Errorf("OR = {%v %b}, want {OpConstInt 0b1110}", got.Kind, got.Imm)
	}
}

// TestBitwiseFoldXor — `c ^ d`.
func TestBitwiseFoldXor(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0b1100
	b := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 0b1010
	r := f.AddOp(entry, OpXor, a, b)
	f.SetRet(entry, r)

	Fold(f)

	if got := entry.Ops[2]; got.Kind != OpConstInt || got.Imm != 0b0110 {
		t.Errorf("XOR = {%v %b}, want {OpConstInt 0b0110}", got.Kind, got.Imm)
	}
}

// TestBitwiseSimplifyAndIdentity — `x & x` → `x`.
func TestBitwiseSimplifyAndIdentity(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	r := f.AddOp(entry, OpAnd, x, x)
	f.SetRet(entry, r)

	Simplify(f)
	if entry.Term.Value != x {
		t.Errorf("Term.Value = %v, want %v (x & x → x)", entry.Term.Value, x)
	}
}

// TestBitwiseSimplifyOrZero — `x | 0` → `x`.
func TestBitwiseSimplifyOrZero(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	zero := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0
	r := f.AddOp(entry, OpOr, x, zero)
	f.SetRet(entry, r)

	Simplify(f)
	if entry.Term.Value != x {
		t.Errorf("Term.Value = %v, want %v (x | 0 → x)", entry.Term.Value, x)
	}
}

// TestBitwiseSimplifyXorZero — `x ^ 0` → `x`.
func TestBitwiseSimplifyXorZero(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	zero := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0
	r := f.AddOp(entry, OpXor, x, zero)
	f.SetRet(entry, r)

	Simplify(f)
	if entry.Term.Value != x {
		t.Errorf("Term.Value = %v, want %v (x ^ 0 → x)", entry.Term.Value, x)
	}
}

// TestBitwiseSimplifyAndAllOnes — `x & -1` → `x` (-1 is the
// all-bits-set sentinel).
func TestBitwiseSimplifyAndAllOnes(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	allOnes := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = -1
	r := f.AddOp(entry, OpAnd, x, allOnes)
	f.SetRet(entry, r)

	Simplify(f)
	if entry.Term.Value != x {
		t.Errorf("Term.Value = %v, want %v (x & -1 → x)", entry.Term.Value, x)
	}
}

// TestBitwiseStrengthAndZero — `x & 0` synthesises const 0.
func TestBitwiseStrengthAndZero(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	zero := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0
	r := f.AddOp(entry, OpAnd, x, zero)
	f.SetRet(entry, r)

	StrengthReduce(f)
	if got := entry.Ops[1]; got.Kind != OpConstInt || got.Imm != 0 {
		t.Errorf("Op = {%v %d}, want {OpConstInt 0}", got.Kind, got.Imm)
	}
}

// TestBitwiseStrengthXorSelf — `x ^ x` synthesises const 0.
func TestBitwiseStrengthXorSelf(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	r := f.AddOp(entry, OpXor, x, x)
	f.SetRet(entry, r)

	StrengthReduce(f)
	if got := entry.Ops[0]; got.Kind != OpConstInt || got.Imm != 0 {
		t.Errorf("Op = {%v %d}, want {OpConstInt 0}", got.Kind, got.Imm)
	}
}

// TestBitwiseCanonicalize — `x & y` with y.ID < x.ID gets
// reordered.
func TestBitwiseCanonicalize(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam() // v1
	b := f.AddParam() // v2
	entry := f.NewBlock()
	op := &Op{Kind: OpAnd, Result: f.NewValue(), Args: []Value{b, a}}
	entry.Ops = append(entry.Ops, op)
	f.SetRet(entry, op.Result)

	Canonicalize(f)
	if op.Args[0] != a || op.Args[1] != b {
		t.Errorf("Args = %v, want [a, b]", op.Args)
	}
}
