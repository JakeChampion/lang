package ssa

import "testing"

// TestFoldSimpleAdd — `1 + 2` folds to `const_int 3`. The Op's
// Result Value stays the same so any pre-cached use survives.
func TestFoldSimpleAdd(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	one := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 1
	two := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 2
	sum := f.AddOp(entry, OpAdd, one, two)
	f.SetRet(entry, sum)

	Fold(f)

	addOp := entry.Ops[2]
	if addOp.Kind != OpConstInt {
		t.Fatalf("add.Kind = %v, want OpConstInt", addOp.Kind)
	}
	if addOp.Imm != 3 {
		t.Errorf("add.Imm = %d, want 3", addOp.Imm)
	}
	if len(addOp.Args) != 0 {
		t.Errorf("add.Args = %v, want empty after fold", addOp.Args)
	}
	if addOp.Result != sum {
		t.Errorf("add.Result = %v, want %v (Value must survive fold)", addOp.Result, sum)
	}
	if err := Verify(f); err != nil {
		t.Errorf("Verify after fold: %v", err)
	}
}

// TestFoldChain — `(1 + 2) * (3 - 1)` cascades through Fold's
// single pass because the def-site map updates as we go.
func TestFoldChain(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	one := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 1
	two := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 2
	three := f.AddOp(entry, OpConstInt)
	entry.Ops[2].Imm = 3
	one2 := f.AddOp(entry, OpConstInt)
	entry.Ops[3].Imm = 1
	lhs := f.AddOp(entry, OpAdd, one, two)
	rhs := f.AddOp(entry, OpSub, three, one2)
	prod := f.AddOp(entry, OpMul, lhs, rhs)
	f.SetRet(entry, prod)

	Fold(f)

	if got := entry.Ops[4].Imm; got != 3 {
		t.Errorf("lhs.Imm = %d, want 3", got)
	}
	if got := entry.Ops[5].Imm; got != 2 {
		t.Errorf("rhs.Imm = %d, want 2", got)
	}
	if got := entry.Ops[6]; got.Kind != OpConstInt || got.Imm != 6 {
		t.Errorf("prod = {%v %d}, want {OpConstInt 6}", got.Kind, got.Imm)
	}
}

// TestFoldDivision — both happy path and divide-by-zero.
// `10 / 3` folds to 3; `10 / 0` is left alone (runtime traps).
func TestFoldDivision(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	ten := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 10
	three := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 3
	zero := f.AddOp(entry, OpConstInt)
	entry.Ops[2].Imm = 0
	good := f.AddOp(entry, OpDiv, ten, three)
	bad := f.AddOp(entry, OpDiv, ten, zero)
	_ = bad
	f.SetRet(entry, good)

	Fold(f)

	if got := entry.Ops[3]; got.Kind != OpConstInt || got.Imm != 3 {
		t.Errorf("10/3 = {%v %d}, want {OpConstInt 3}", got.Kind, got.Imm)
	}
	if got := entry.Ops[4]; got.Kind != OpDiv {
		t.Errorf("10/0 folded to %v; expected OpDiv (preserve runtime trap)", got.Kind)
	}
}

// TestFoldRemainder — same divide-by-zero guard as Div.
func TestFoldRemainder(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	ten := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 10
	three := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 3
	r := f.AddOp(entry, OpRem, ten, three)
	f.SetRet(entry, r)

	Fold(f)

	if got := entry.Ops[2]; got.Kind != OpConstInt || got.Imm != 1 {
		t.Errorf("10%%3 = {%v %d}, want {OpConstInt 1}", got.Kind, got.Imm)
	}
}

// TestFoldComparisons — every comparison kind folds to
// OpConstBool with Imm 0 / 1.
func TestFoldComparisons(t *testing.T) {
	cases := []struct {
		name string
		kind OpKind
		lhs  int64
		rhs  int64
		want int64
	}{
		{"eq_true", OpEq, 5, 5, 1},
		{"eq_false", OpEq, 5, 6, 0},
		{"ne_true", OpNe, 5, 6, 1},
		{"ne_false", OpNe, 5, 5, 0},
		{"lt_true", OpLt, 1, 2, 1},
		{"lt_false", OpLt, 2, 1, 0},
		{"le_eq", OpLe, 2, 2, 1},
		{"gt_true", OpGt, 3, 1, 1},
		{"ge_eq", OpGe, 4, 4, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := NewFunc("f")
			entry := f.NewBlock()
			a := f.AddOp(entry, OpConstInt)
			entry.Ops[0].Imm = c.lhs
			b := f.AddOp(entry, OpConstInt)
			entry.Ops[1].Imm = c.rhs
			cmp := f.AddOp(entry, c.kind, a, b)
			f.SetRet(entry, cmp)

			Fold(f)

			got := entry.Ops[2]
			if got.Kind != OpConstBool {
				t.Fatalf("cmp.Kind = %v, want OpConstBool", got.Kind)
			}
			if got.Imm != c.want {
				t.Errorf("cmp.Imm = %d, want %d", got.Imm, c.want)
			}
		})
	}
}

// TestFoldSkipsNonConstArgs — an op with a Param argument
// can't fold. The op stays untouched.
func TestFoldSkipsNonConstArgs(t *testing.T) {
	f := NewFunc("f")
	p := f.AddParam()
	entry := f.NewBlock()
	one := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 1
	sum := f.AddOp(entry, OpAdd, p, one)
	f.SetRet(entry, sum)

	Fold(f)

	if got := entry.Ops[1]; got.Kind != OpAdd {
		t.Errorf("add.Kind = %v, want OpAdd (still unfoldable)", got.Kind)
	}
}

// TestFoldAcrossBlocks — a const defined in entry, used in a
// successor, folds via the def-site map (which threads across
// blocks in iteration order).
func TestFoldAcrossBlocks(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	next := f.NewBlock()
	one := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 1
	two := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 2
	f.SetBr(entry, next)
	sum := f.AddOp(next, OpAdd, one, two)
	f.SetRet(next, sum)

	Fold(f)

	if got := next.Ops[0]; got.Kind != OpConstInt || got.Imm != 3 {
		t.Errorf("cross-block add = {%v %d}, want {OpConstInt 3}", got.Kind, got.Imm)
	}
}

// TestFoldNilFunc — Fold(nil) is a no-op, not a panic.
func TestFoldNilFunc(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Fold(nil) panicked: %v", r)
		}
	}()
	Fold(nil)
}
