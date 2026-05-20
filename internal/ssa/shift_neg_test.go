package ssa

import "testing"

// TestShiftFoldShl — `1 << 3` folds to 8.
func TestShiftFoldShl(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 1
	b := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 3
	r := f.AddOp(entry, OpShl, a, b)
	f.SetRet(entry, r)

	Fold(f)

	if got := entry.Ops[2]; got.Kind != OpConstInt || got.Imm != 8 {
		t.Errorf("1<<3 = {%v %d}, want {OpConstInt 8}", got.Kind, got.Imm)
	}
}

// TestShiftFoldShr — `0xff >> 4` folds to 0x0f.
func TestShiftFoldShr(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0xff
	b := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 4
	r := f.AddOp(entry, OpShr, a, b)
	f.SetRet(entry, r)

	Fold(f)

	if got := entry.Ops[2]; got.Kind != OpConstInt || got.Imm != 0xf {
		t.Errorf("0xff>>4 = {%v %d}, want {OpConstInt 0xf}", got.Kind, got.Imm)
	}
}

// TestShiftOutOfRangeNotFolded — count < 0 or >= 64 leaves
// the op untouched (runtime owns the trap).
func TestShiftOutOfRangeNotFolded(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 1
	b := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 64
	r := f.AddOp(entry, OpShl, a, b)
	f.SetRet(entry, r)

	Fold(f)

	if got := entry.Ops[2]; got.Kind != OpShl {
		t.Errorf("out-of-range shift folded to %v; expected to stay as OpShl", got.Kind)
	}
}

// TestShiftSimplifyZero — `x << 0`, `x >> 0`, `x >>u 0` all
// alias to `x`. The signed/unsigned distinction matters for
// the shift's runtime semantics; the zero-count identity
// holds for all three.
func TestShiftSimplifyZero(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	zero := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0
	shl := f.AddOp(entry, OpShl, x, zero)
	shr := f.AddOp(entry, OpShr, shl, zero)
	shrU := f.AddOp(entry, OpShrU, shr, zero)
	f.SetRet(entry, shrU)

	Simplify(f)
	if entry.Term.Value != x {
		t.Errorf("Term.Value = %v, want %v (shifts by 0 → x)", entry.Term.Value, x)
	}
}

// TestNegFold — `-(const 42)` folds to const_int -42.
func TestNegFold(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	c := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 42
	r := f.AddOp(entry, OpNeg, c)
	f.SetRet(entry, r)

	Fold(f)

	if got := entry.Ops[1]; got.Kind != OpConstInt || got.Imm != -42 {
		t.Errorf("Neg = {%v %d}, want {OpConstInt -42}", got.Kind, got.Imm)
	}
}

// TestNegLeavesNonConst — Neg of a Param stays as OpNeg.
func TestNegLeavesNonConst(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	r := f.AddOp(entry, OpNeg, x)
	f.SetRet(entry, r)

	Fold(f)
	if entry.Ops[0].Kind != OpNeg {
		t.Errorf("Kind = %v, want OpNeg (non-const arg)", entry.Ops[0].Kind)
	}
}

// TestShiftCanonicalizeUnaffected — shifts are NOT
// commutative; Canonicalize must not swap operands.
func TestShiftCanonicalizeUnaffected(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	op := &Op{Kind: OpShl, Result: f.NewValue(), Args: []Value{b, a}}
	entry.Ops = append(entry.Ops, op)
	f.SetRet(entry, op.Result)

	Canonicalize(f)

	if op.Args[0] != b || op.Args[1] != a {
		t.Errorf("Args = %v, want [b, a] (shift not commutative)", op.Args)
	}
}

// TestShiftNegInOptimizePipeline — end-to-end via Optimize.
// `-((1 << 3) + (8 - 8))` folds to const_int -8.
func TestShiftNegInOptimizePipeline(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 1
	b := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 3
	eight := f.AddOp(entry, OpShl, a, b) // 8
	eight2 := f.AddOp(entry, OpConstInt)
	entry.Ops[3].Imm = 8
	subZero := f.AddOp(entry, OpSub, eight2, eight2) // 0
	sum := f.AddOp(entry, OpAdd, eight, subZero)     // 8
	neg := f.AddOp(entry, OpNeg, sum)                // -8
	f.SetRet(entry, neg)

	Optimize(f)

	if len(entry.Ops) != 1 {
		t.Fatalf("Ops = %d, want 1; kinds %v", len(entry.Ops), opKinds(entry.Ops))
	}
	if got := entry.Ops[0]; got.Kind != OpConstInt || got.Imm != -8 {
		t.Errorf("survivor = {%v %d}, want {OpConstInt -8}", got.Kind, got.Imm)
	}
}
