package ssa

import "testing"

// TestCanonAddOrdersArgs — `add b, a` (b.ID > a.ID) gets
// swapped to `add a, b`.
func TestCanonAddOrdersArgs(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam() // v1
	b := f.AddParam() // v2
	entry := f.NewBlock()
	op := &Op{Kind: OpAdd, Result: f.NewValue(), Args: []Value{b, a}}
	entry.Ops = append(entry.Ops, op)
	f.SetRet(entry, op.Result)

	Canonicalize(f)

	if op.Args[0] != a || op.Args[1] != b {
		t.Errorf("Args = %v, want [a, b] (ascending ID)", op.Args)
	}
}

// TestCanonMulOrdersArgs — same for OpMul.
func TestCanonMulOrdersArgs(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	op := &Op{Kind: OpMul, Result: f.NewValue(), Args: []Value{b, a}}
	entry.Ops = append(entry.Ops, op)
	f.SetRet(entry, op.Result)

	Canonicalize(f)

	if op.Args[0] != a || op.Args[1] != b {
		t.Errorf("Args = %v, want [a, b]", op.Args)
	}
}

// TestCanonEqOrdersArgs — equality is commutative.
func TestCanonEqOrdersArgs(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	op := &Op{Kind: OpEq, Result: f.NewValue(), Args: []Value{b, a}}
	entry.Ops = append(entry.Ops, op)
	f.SetRet(entry, op.Result)

	Canonicalize(f)

	if op.Args[0] != a || op.Args[1] != b {
		t.Errorf("Args = %v, want [a, b]", op.Args)
	}
}

// TestCanonSubPreserved — Sub is NOT commutative; arg order
// must be preserved (otherwise `a-b` becomes `b-a`).
func TestCanonSubPreserved(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	op := &Op{Kind: OpSub, Result: f.NewValue(), Args: []Value{b, a}}
	entry.Ops = append(entry.Ops, op)
	f.SetRet(entry, op.Result)

	Canonicalize(f)

	if op.Args[0] != b || op.Args[1] != a {
		t.Errorf("Args = %v, want [b, a] (Sub not commutative)", op.Args)
	}
}

// TestCanonLtPreserved — Lt/Gt/Le/Ge are NOT commutative
// (swap would invert).
func TestCanonLtPreserved(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	op := &Op{Kind: OpLt, Result: f.NewValue(), Args: []Value{b, a}}
	entry.Ops = append(entry.Ops, op)
	f.SetRet(entry, op.Result)

	Canonicalize(f)

	if op.Args[0] != b || op.Args[1] != a {
		t.Errorf("Args = %v, want [b, a] (Lt not commutative)", op.Args)
	}
}

// TestCanonUnlocksCSE — `a + b` then `b + a` look identical
// to CSE only after Canonicalize. End-to-end demo.
func TestCanonUnlocksCSE(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	x := f.AddOp(entry, OpAdd, a, b) // a + b
	y := f.AddOp(entry, OpAdd, b, a) // b + a — same expression
	sum := f.AddOp(entry, OpAdd, x, y)
	f.SetRet(entry, sum)

	Canonicalize(f)
	CSE(f)
	DCE(f)

	// Expect: one canonical (a+b) survives + the final add.
	if len(entry.Ops) != 2 {
		t.Fatalf("Ops = %d, want 2 (one a+b + final); kinds %v",
			len(entry.Ops), opKinds(entry.Ops))
	}
}

// TestCanonNoOpOnAlreadyOrdered — already-ordered args
// unchanged.
func TestCanonNoOpOnAlreadyOrdered(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	op := &Op{Kind: OpAdd, Result: f.NewValue(), Args: []Value{a, b}}
	entry.Ops = append(entry.Ops, op)
	f.SetRet(entry, op.Result)

	Canonicalize(f)

	if op.Args[0] != a || op.Args[1] != b {
		t.Errorf("Args = %v, want unchanged [a, b]", op.Args)
	}
}

// TestCanonNilFunc — defensive.
func TestCanonNilFunc(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Canonicalize(nil) panicked: %v", r)
		}
	}()
	Canonicalize(nil)
}
