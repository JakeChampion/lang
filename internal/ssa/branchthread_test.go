package ssa

import (
	"math"
	"testing"
)

func TestThreadPhiBranchesComparisonKinds(t *testing.T) {
	for _, kind := range []OpKind{OpEq, OpNe, OpLt, OpLtU, OpLe, OpLeU, OpGt, OpGtU, OpGe, OpGeU,
		OpFEq, OpFNe, OpFLt, OpFLe, OpFGt, OpFGe} {
		t.Run(kind.String(), func(t *testing.T) {
			for _, value := range []int64{-1, 0, 9, 10, 11, math.MinInt32, math.MaxInt32} {
				f, left, _, _ := booleanJoinFixture()
				left.Ops[1].Kind = kind
				before := f.Clone()
				ThreadPhiBranches(f)
				if left.Term.Kind != TermBrIf {
					t.Fatal("comparison edge was not threaded")
				}
				PruneUnreachable(f)
				if err := Verify(f); err != nil {
					t.Fatal(err)
				}
				// Floating comparisons interpret these register bits as f64,
				// including NaNs for negative bit patterns.
				want, err := Eval(before, 1, value)
				if err != nil {
					t.Fatal(err)
				}
				got, err := Eval(f, 1, value)
				if err != nil || got != want {
					t.Fatalf("value=%d got=%d err=%v want=%d", value, got, err, want)
				}
			}
		})
	}
}

func booleanJoinFixture() (*Func, *Block, *Block, *Block) {
	f := NewFunc("boolean_join")
	choose, x := f.AddParam(), f.AddParam()
	entry, left, right := f.NewBlock(), f.NewBlock(), f.NewBlock()
	join, yes, no := f.NewBlock(), f.NewBlock(), f.NewBlock()
	f.SetBrIf(entry, choose, left, right)
	limit := f.AddOp(left, OpConstInt)
	left.Ops[0].Imm = 10
	cmp := f.AddOp(left, OpLt, x, limit)
	f.SetBr(left, join)
	zero := f.AddOp(right, OpConstBool)
	f.SetBr(right, join)
	cond := f.AddPhi(join, cmp, zero)
	f.SetBrIf(join, cond, yes, no)
	a := f.AddOp(yes, OpConstInt)
	yes.Ops[0].Imm = 11
	f.SetRet(yes, a)
	b := f.AddOp(no, OpConstInt)
	no.Ops[0].Imm = 22
	f.SetRet(no, b)
	return f, left, right, join
}

func TestOptimizeThreadsBooleanJoin(t *testing.T) {
	f, _, _, _ := booleanJoinFixture()
	before := f.Clone()
	Optimize(f)
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
	for _, b := range f.Blocks {
		if b.Term.Kind != TermBrIf {
			continue
		}
		for _, op := range b.Ops {
			if op.Kind == OpPhi && op.Result == b.Term.Cond {
				t.Errorf("boolean phi still feeds a branch in block%d", b.ID)
			}
		}
	}
	for _, choose := range []int64{0, 1} {
		for _, x := range []int64{-10, 0, 9, 10, 11, 100} {
			want, err := Eval(before, choose, x)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Eval(f, choose, x)
			if err != nil || got != want {
				t.Fatalf("choose=%d x=%d got=%d err=%v want=%d", choose, x, got, err, want)
			}
		}
	}
}

func TestThreadPhiBranchesForwardsSuccessorPhis(t *testing.T) {
	f, _, _, join := booleanJoinFixture()
	yes := join.Term.True
	x := f.Params[1]
	v := f.AddPhi(yes, x)
	f.SetRet(yes, v)
	before := f.Clone()
	ThreadPhiBranches(f)
	if len(yes.Preds) != 3 || len(yes.Ops[0].Args) != 3 {
		t.Fatalf("successor predecessor/phi slots not extended: %s", f)
	}
	for _, arg := range yes.Ops[0].Args {
		if arg != x {
			t.Fatal("wrong forwarded phi argument")
		}
	}
	PruneUnreachable(f)
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]int64{{1, 2}, {1, 20}, {0, 2}} {
		want, err := Eval(before, args...)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Eval(f, args...)
		if err != nil || got != want {
			t.Fatalf("got=%d err=%v want=%d", got, err, want)
		}
	}
}

func TestThreadPhiBranchesPartialThreading(t *testing.T) {
	f, left, right, join := booleanJoinFixture()
	yes := join.Term.True
	cmp := left.Ops[1].Result
	f.SetBrIf(left, cmp, join, yes)
	before := f.Clone()
	ThreadPhiBranches(f)
	if left.Term.True != join || right.Term.Kind != TermBrIf {
		t.Fatal("conditional predecessor changed or unconditional one was not threaded")
	}
	if len(join.Preds) != 1 || join.Preds[0] != left || len(join.Ops[0].Args) != 1 || join.Ops[0].Args[0] != cmp {
		t.Fatal("remaining join phi no longer matches its predecessor")
	}
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]int64{{1, 2}, {1, 20}, {0, 2}} {
		want, err := Eval(before, args...)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Eval(f, args...)
		if err != nil || got != want {
			t.Fatalf("got=%d err=%v want=%d", got, err, want)
		}
	}
}

func TestThreadPhiBranchesSafetyExclusions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Func, *Block)
	}{
		{"escaping-condition", func(f *Func, b *Block) { f.SetRet(b.Term.True, b.Term.Cond) }},
		{"extra-op", func(f *Func, b *Block) { f.AddOp(b, OpConstInt) }},
		{"same-target", func(f *Func, b *Block) { b.Term.False = b.Term.True }},
		{"self-target", func(f *Func, b *Block) { b.Term.False = b }},
		{"entry-join", func(f *Func, b *Block) { f.Entry = b }},
		{"duplicate-predecessor", func(f *Func, b *Block) {
			b.Preds = append(b.Preds, b.Preds[0])
			b.Ops[0].Args = append(b.Ops[0].Args, b.Ops[0].Args[0])
		}},
		{"duplicate-successor-predecessor", func(f *Func, b *Block) { b.Term.True.Preds = append(b.Term.True.Preds, b) }},
		{"malformed-successor-phi", func(f *Func, b *Block) { f.AddPhi(b.Term.True, f.Params[0], f.Params[1]) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, _, _, join := booleanJoinFixture()
			tc.change(f, join)
			before := f.String()
			ThreadPhiBranches(f)
			if f.String() != before {
				t.Fatal("unsafe join was rewritten")
			}
		})
	}
	ThreadPhiBranches(nil)
}

func TestThreadPhiBranchesLeavesUnknownTruthiness(t *testing.T) {
	for _, value := range []int64{2, -1, 1 << 32} {
		f, left, right, join := booleanJoinFixture()
		left.Ops[1].Kind = OpConstInt
		left.Ops[1].Args = nil
		left.Ops[1].Imm = value
		before := f.Clone()
		ThreadPhiBranches(f)
		if left.Term.Kind != TermBr || left.Term.Target != join || right.Term.Kind != TermBrIf {
			t.Fatal("unknown non-boolean value was threaded or known boolean was not")
		}
		if err := Verify(f); err != nil {
			t.Fatal(err)
		}
		for _, choose := range []int64{0, 1} {
			want, err := Eval(before, choose, 0)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Eval(f, choose, 0)
			if err != nil || got != want {
				t.Fatalf("value=%d choose=%d got=%d err=%v want=%d", value, choose, got, err, want)
			}
		}
	}
}

func TestThreadPhiBranchesKeepsSkippedFaultSkipped(t *testing.T) {
	f, left, _, _ := booleanJoinFixture()
	bad := loadOp(f, left, constIn(f, left, 4096), 0)
	left.Ops[1].Args[0] = bad
	ops := left.Ops
	left.Ops = []*Op{ops[0], ops[2], ops[3], ops[1]}
	before := f.Clone()
	Optimize(f)
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
	for _, program := range []*Func{before, f} {
		if got, err := Eval(program, 0, 0); err != nil || got != 22 {
			t.Fatalf("skipped fault executed: got=%d err=%v", got, err)
		}
		if _, err := Eval(program, 1, 0); err == nil {
			t.Fatal("taken fault unexpectedly disappeared")
		}
	}
}

func TestThreadPhiBranchesPreservesLoopPhis(t *testing.T) {
	f := NewFunc("loop_join")
	n := f.AddParam()
	entry, header := f.NewBlock(), f.NewBlock()
	left, right, join := f.NewBlock(), f.NewBlock(), f.NewBlock()
	back, exit := f.NewBlock(), f.NewBlock()
	zero := constIn(f, entry, 0)
	one := constIn(f, entry, 1)
	five := constIn(f, entry, 5)
	f.SetBr(entry, header)
	i := f.AddPhi(header, zero)
	f.SetBrIf(header, f.AddOp(header, OpLt, i, n), left, right)
	c := f.AddOp(left, OpLt, i, five)
	f.SetBr(left, join)
	no := f.AddOp(right, OpConstBool)
	f.SetBr(right, join)
	f.SetBrIf(join, f.AddPhi(join, c, no), back, exit)
	next := f.AddOp(back, OpAdd, i, one)
	f.SetBr(back, header)
	header.Ops[0].Args = append(header.Ops[0].Args, next)
	f.SetRet(exit, i)
	before := f.Clone()
	if err := Verify(before); err != nil {
		t.Fatal(err)
	}
	Optimize(f)
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int64{-1, 0, 1, 4, 5, 6, 10, 100} {
		want, err := Eval(before, n)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Eval(f, n)
		if err != nil || got != want {
			t.Fatalf("n=%d got=%d err=%v want=%d", n, got, err, want)
		}
	}
}
