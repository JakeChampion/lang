package ssa

import "testing"

// TestSimplifyAddZero — `x + 0` → `x`. Uses of the Add result
// now reference x directly.
func TestSimplifyAddZero(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	zero := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0
	sum := f.AddOp(entry, OpAdd, x, zero)
	doubled := f.AddOp(entry, OpAdd, sum, sum)
	f.SetRet(entry, doubled)

	Simplify(f)

	// `doubled = add sum, sum` should now be `add x, x` (sum has been aliased).
	if entry.Ops[2].Args[0] != x || entry.Ops[2].Args[1] != x {
		t.Errorf("doubled.Args = %v, want both = %v", entry.Ops[2].Args, x)
	}
}

// TestSimplifyZeroAdd — `0 + x` → `x` (commutative form).
func TestSimplifyZeroAdd(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	zero := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0
	sum := f.AddOp(entry, OpAdd, zero, x)
	f.SetRet(entry, sum)

	Simplify(f)

	if entry.Term.Value != x {
		t.Errorf("Term.Value = %v, want %v (0+x aliases x)", entry.Term.Value, x)
	}
}

// TestSimplifySubZero — `x - 0` → `x`.
func TestSimplifySubZero(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	zero := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0
	r := f.AddOp(entry, OpSub, x, zero)
	f.SetRet(entry, r)

	Simplify(f)
	if entry.Term.Value != x {
		t.Errorf("Term.Value = %v, want %v", entry.Term.Value, x)
	}
}

// TestSimplifyMulOne — `x * 1` → `x` and `1 * x` → `x`.
func TestSimplifyMulOne(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	one := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 1
	r1 := f.AddOp(entry, OpMul, x, one)
	r2 := f.AddOp(entry, OpMul, one, r1)
	f.SetRet(entry, r2)

	Simplify(f)
	if entry.Term.Value != x {
		t.Errorf("Term.Value = %v, want %v (1*(x*1) aliases x)", entry.Term.Value, x)
	}
}

// TestSimplifyDivOne — `x / 1` → `x`. We must NOT touch x/0
// (constfold also leaves it alone) and we don't fold 1/x
// (would change semantics).
func TestSimplifyDivOne(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	one := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 1
	zero := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 0
	good := f.AddOp(entry, OpDiv, x, one)
	bad := f.AddOp(entry, OpDiv, x, zero)
	weird := f.AddOp(entry, OpDiv, one, x)
	f.SetRet(entry, good)
	_ = bad
	_ = weird

	Simplify(f)

	if entry.Term.Value != x {
		t.Errorf("x/1 should alias x; Term.Value = %v", entry.Term.Value)
	}
	// x/0 still references x (no sub for `bad`)
	if entry.Ops[3].Args[0] != x {
		t.Errorf("x/0 args[0] = %v, want %v (unchanged)", entry.Ops[3].Args[0], x)
	}
	// 1/x args unchanged
	if entry.Ops[4].Args[1] != x {
		t.Errorf("1/x args[1] = %v, want %v (unchanged)", entry.Ops[4].Args[1], x)
	}
}

// TestSimplifyTransitiveChain — `(x + 0) + 0` aliases through
// to x in a single resolver pass.
func TestSimplifyTransitiveChain(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	zero := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0
	a := f.AddOp(entry, OpAdd, x, zero)
	b := f.AddOp(entry, OpAdd, a, zero)
	c := f.AddOp(entry, OpAdd, b, zero)
	f.SetRet(entry, c)

	Simplify(f)

	if entry.Term.Value != x {
		t.Errorf("Term.Value = %v, want %v (three-deep alias)", entry.Term.Value, x)
	}
}

// TestSimplifyNoOp — function with nothing to simplify is
// untouched.
func TestSimplifyNoOp(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	sum := f.AddOp(entry, OpAdd, a, b)
	f.SetRet(entry, sum)

	Simplify(f)

	if entry.Ops[0].Args[0] != a || entry.Ops[0].Args[1] != b {
		t.Errorf("Args = %v, want unchanged %v / %v", entry.Ops[0].Args, a, b)
	}
}

// TestSimplifyFollowedByDCE — after Simplify rewrites uses,
// the now-orphan identity Op falls to DCE.
func TestSimplifyFollowedByDCE(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	zero := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0
	_ = f.AddOp(entry, OpAdd, x, zero) // identity, will be aliased + dropped
	doubled := f.AddOp(entry, OpAdd, x, x)
	f.SetRet(entry, doubled)

	Simplify(f)
	DCE(f)

	// After: only `add x, x` should survive (const 0 + identity-add both dead).
	if len(entry.Ops) != 1 {
		t.Fatalf("Ops len = %d, want 1; got %v", len(entry.Ops), entry.Ops)
	}
	if entry.Ops[0].Kind != OpAdd {
		t.Errorf("survivor kind = %v, want OpAdd", entry.Ops[0].Kind)
	}
}

// TestSimplifyBrIfCond — `cond + 0` aliases through to cond
// in the brif terminator.
func TestSimplifyBrIfCond(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	zero := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0
	wrapped := f.AddOp(entry, OpAdd, c, zero)
	f.SetBrIf(entry, wrapped, thenB, elseB)
	f.SetRet(thenB, Value{})
	f.SetRet(elseB, Value{})

	Simplify(f)

	if entry.Term.Cond != c {
		t.Errorf("Term.Cond = %v, want %v", entry.Term.Cond, c)
	}
}

// TestSimplifyNilFunc — defensive: nil input is a no-op.
func TestSimplifyNilFunc(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Simplify(nil) panicked: %v", r)
		}
	}()
	Simplify(nil)
}
