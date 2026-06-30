package ssa

import "testing"

// callOp adds an OpCall to callee with the given args and returns its result.
func callOp(f *Func, b *Block, callee string, args ...Value) Value {
	v := f.AddOp(b, OpCall, args...)
	b.Ops[len(b.Ops)-1].Str = callee
	return v
}

func constIn(f *Func, b *Block, imm int64) Value {
	v := f.AddOp(b, OpConstInt)
	b.Ops[len(b.Ops)-1].Imm = imm
	return v
}

// add(a,b)=a+b ; main()=add(3,4)+add(10,20)=37. Direct calls resolved via the
// function table.
func TestEvalInDirectCalls(t *testing.T) {
	add := NewFunc("add")
	a := add.AddParam()
	b := add.AddParam()
	ae := add.NewBlock()
	add.SetRet(ae, add.AddOp(ae, OpAdd, a, b))

	main := NewFunc("main")
	me := main.NewBlock()
	t1 := callOp(main, me, "add", constIn(main, me, 3), constIn(main, me, 4))
	t2 := callOp(main, me, "add", constIn(main, me, 10), constIn(main, me, 20))
	main.SetRet(me, main.AddOp(me, OpAdd, t1, t2))

	funcs := map[string]*Func{"add": add, "main": main}
	got, err := EvalIn(funcs, main)
	if err != nil {
		t.Fatalf("EvalIn: %v", err)
	}
	if got != 37 {
		t.Errorf("EvalIn(main) = %d, want 37", got)
	}
}

// factorial(n) = n<=1 ? 1 : n*factorial(n-1). Recursion via the function table.
func factorialFunc() *Func {
	f := NewFunc("factorial")
	n := f.AddParam()
	entry := f.NewBlock()
	base := f.NewBlock()
	rec := f.NewBlock()
	cond := f.AddOp(entry, OpLe, n, constIn(f, entry, 1))
	f.SetBrIf(entry, cond, base, rec)
	f.SetRet(base, constIn(f, base, 1))
	nm1 := f.AddOp(rec, OpSub, n, constIn(f, rec, 1))
	fr := callOp(f, rec, "factorial", nm1)
	f.SetRet(rec, f.AddOp(rec, OpMul, n, fr))
	return f
}

func TestEvalInRecursion(t *testing.T) {
	fac := factorialFunc()
	funcs := map[string]*Func{"factorial": fac}
	for _, tc := range []struct{ n, want int64 }{{0, 1}, {1, 1}, {2, 2}, {3, 6}, {5, 120}, {6, 720}} {
		got, err := EvalIn(funcs, fac, tc.n)
		if err != nil {
			t.Fatalf("EvalIn(factorial, %d): %v", tc.n, err)
		}
		if got != tc.want {
			t.Errorf("factorial(%d) = %d, want %d", tc.n, got, tc.want)
		}
	}
}

// A plain Eval (no table) over an OpCall is a clear error, not a wrong answer.
func TestEvalCallWithoutTableErrors(t *testing.T) {
	main := NewFunc("main")
	me := main.NewBlock()
	main.SetRet(me, callOp(main, me, "missing", constIn(main, me, 1)))
	if _, err := Eval(main); err == nil {
		t.Error("expected Eval (no function table) to error on OpCall")
	}
}
