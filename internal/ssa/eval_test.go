package ssa

import "testing"

// Straight-line arithmetic: f(a, b) = (a + b) * 3.
func TestEvalArithmetic(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	sum := f.AddOp(entry, OpAdd, a, b)
	three := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 3
	prod := f.AddOp(entry, OpMul, sum, three)
	f.SetRet(entry, prod)

	got, err := Eval(f, 4, 5)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got != 27 {
		t.Errorf("Eval((4+5)*3) = %d, want 27", got)
	}
}

// Control flow: f(c) = c != 0 ? 10 : 20, via a diamond + phi.
func TestEvalDiamondPhi(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	merge := f.NewBlock()

	f.SetBrIf(entry, c, thenB, elseB)
	ten := f.AddOp(thenB, OpConstInt)
	thenB.Ops[0].Imm = 10
	f.SetBr(thenB, merge)
	twenty := f.AddOp(elseB, OpConstInt)
	elseB.Ops[0].Imm = 20
	f.SetBr(elseB, merge)
	// merge.Preds == [thenB, elseB] in branch order.
	p := f.AddPhi(merge, ten, twenty)
	f.SetRet(merge, p)

	for _, tc := range []struct {
		c, want int64
	}{{1, 10}, {0, 20}, {7, 10}} {
		got, err := Eval(f, tc.c)
		if err != nil {
			t.Fatalf("Eval(c=%d): %v", tc.c, err)
		}
		if got != tc.want {
			t.Errorf("Eval(c=%d) = %d, want %d", tc.c, got, tc.want)
		}
	}
}

// Loop: i = 0; while (i < 5) { i = i + 1 } return i  ->  5. Exercises a header
// phi resolved across both the entry edge and the back-edge.
func TestEvalCountingLoop(t *testing.T) {
	f := NewFunc("countTo5")
	entry := f.NewBlock()
	header := f.NewBlock()
	body := f.NewBlock()
	exit := f.NewBlock()

	init := f.AddOp(entry, OpConstInt) // i = 0 (Imm defaults to 0)
	f.SetBr(entry, header)

	inext := f.NewValue()
	i := f.AddPhi(header, init, inext)
	limit := f.AddOp(header, OpConstInt)
	header.Ops[1].Imm = 5
	cond := f.AddOp(header, OpLt, i, limit)
	f.SetBrIf(header, cond, body, exit)

	one := f.AddOp(body, OpConstInt)
	body.Ops[0].Imm = 1
	add := f.AddOpNoResult(body, OpAdd, i, one)
	add.Result = inext
	f.SetBr(body, header)

	f.SetRet(exit, i)

	got, err := Eval(f)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got != 5 {
		t.Errorf("Eval(countTo5) = %d, want 5", got)
	}
}

// i32 width: results are masked to 32 bits (sign-extended). 2^31-1 + 1 wraps to
// the i32 minimum.
func TestEvalWidthMasking(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	max := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0x7fffffff
	one := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 1
	sum := f.AddOp(entry, OpAdd, max, one) // Width 0 => i32
	f.SetRet(entry, sum)

	got, err := Eval(f)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got != int64(int32(-2147483648)) {
		t.Errorf("Eval(i32 overflow) = %d, want %d", got, int32(-2147483648))
	}
}

// Semantic preservation: Optimize must not change a function's result. This is
// the property the evaluator exists to guard for the whole regalloc track.
func TestEvalPreservedAcrossOptimize(t *testing.T) {
	build := func() (*Func, Value, Value) {
		f := NewFunc("f")
		a := f.AddParam()
		b := f.AddParam()
		entry := f.NewBlock()
		// (a + b) - b, which the optimiser may simplify; result must equal a.
		sum := f.AddOp(entry, OpAdd, a, b)
		diff := f.AddOp(entry, OpSub, sum, b)
		f.SetRet(entry, diff)
		return f, a, b
	}

	args := [][2]int64{{3, 4}, {10, -2}, {0, 0}, {-5, 9}}

	f1, _, _ := build()
	before := make([]int64, len(args))
	for i, ab := range args {
		v, err := Eval(f1, ab[0], ab[1])
		if err != nil {
			t.Fatalf("Eval before: %v", err)
		}
		before[i] = v
	}

	f2, _, _ := build()
	Optimize(f2)
	for i, ab := range args {
		v, err := Eval(f2, ab[0], ab[1])
		if err != nil {
			t.Fatalf("Eval after optimize: %v", err)
		}
		if v != before[i] {
			t.Errorf("Optimize changed result for args %v: before=%d after=%d", ab, before[i], v)
		}
	}
}

// Parallel phi semantics: a header that swaps two values across the back-edge
// (a,b = b,a) must read both old values before assigning either. A sequential
// read-then-assign would make both equal. After n iterations a is the original
// a if n is even, b if odd.
func TestEvalParallelPhiSwap(t *testing.T) {
	f := NewFunc("swap")
	n := f.AddParam()
	entry := f.NewBlock()
	header := f.NewBlock()
	body := f.NewBlock()
	exit := f.NewBlock()

	a0 := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 100
	b0 := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 200
	i0 := f.AddOp(entry, OpConstInt)
	entry.Ops[2].Imm = 0
	f.SetBr(entry, header)

	iNext := f.NewValue()
	a := f.AddPhi(header, a0, b0) // back-edge arg patched below
	b := f.AddPhi(header, b0, a0)
	i := f.AddPhi(header, i0, iNext)
	cond := f.AddOp(header, OpLt, i, n)
	f.SetBrIf(header, cond, body, exit)

	one := f.AddOp(body, OpConstInt)
	body.Ops[0].Imm = 1
	add := f.AddOpNoResult(body, OpAdd, i, one)
	add.Result = iNext
	f.SetBr(body, header)
	// Back-edge: a's phi pulls b, b's phi pulls a (the swap).
	header.Ops[0].Args[1] = b
	header.Ops[1].Args[1] = a

	f.SetRet(exit, a)

	for _, tc := range []struct{ n, want int64 }{{0, 100}, {1, 200}, {2, 100}, {3, 200}, {4, 100}} {
		got, err := Eval(f, tc.n)
		if err != nil {
			t.Fatalf("Eval(n=%d): %v", tc.n, err)
		}
		if got != tc.want {
			t.Errorf("Eval(swap, n=%d) = %d, want %d", tc.n, got, tc.want)
		}
	}
}
