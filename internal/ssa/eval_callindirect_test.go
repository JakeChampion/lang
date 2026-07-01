package ssa

import "testing"

// callIndirectOp adds an OpCallIndirect dispatching on callee (a function-index
// value) with the given args, and returns its result.
func callIndirectOp(f *Func, b *Block, callee Value, args ...Value) Value {
	all := append([]Value{callee}, args...)
	return f.AddOp(b, OpCallIndirect, all...)
}

// inc(x)=x+1, dbl(x)=x*2, table=[inc,dbl]. apply(fn,x)=fn(x) via OpCallIndirect.
// main sums apply(0,10)+apply(1,10) = 11 + 20 = 31.
func indirectFuncs() (map[string]*Func, []string) {
	inc := NewFunc("inc")
	ix := inc.AddParam()
	ie := inc.NewBlock()
	inc.SetRet(ie, inc.AddOp(ie, OpAdd, ix, constIn(inc, ie, 1)))

	dbl := NewFunc("dbl")
	dx := dbl.AddParam()
	de := dbl.NewBlock()
	dbl.SetRet(de, dbl.AddOp(de, OpMul, dx, constIn(dbl, de, 2)))

	apply := NewFunc("apply")
	fn := apply.AddParam()
	x := apply.AddParam()
	ae := apply.NewBlock()
	apply.SetRet(ae, callIndirectOp(apply, ae, fn, x))

	funcs := map[string]*Func{"inc": inc, "dbl": dbl, "apply": apply}
	return funcs, []string{"inc", "dbl"}
}

func TestEvalInTableCallIndirect(t *testing.T) {
	funcs, table := indirectFuncs()

	main := NewFunc("main")
	me := main.NewBlock()
	r0 := callOp(main, me, "apply", constIn(main, me, 0), constIn(main, me, 10))
	r1 := callOp(main, me, "apply", constIn(main, me, 1), constIn(main, me, 10))
	main.SetRet(me, main.AddOp(me, OpAdd, r0, r1))
	funcs["main"] = main

	got, err := EvalInTable(funcs, table, main)
	if err != nil {
		t.Fatalf("EvalInTable: %v", err)
	}
	if got != 31 { // inc(10)=11 + dbl(10)=20
		t.Errorf("EvalInTable(main) = %d, want 31", got)
	}
}

// Dispatching apply directly through each table index resolves the right callee.
func TestEvalInTableCallIndirectDirect(t *testing.T) {
	funcs, table := indirectFuncs()
	apply := funcs["apply"]
	for _, tc := range []struct {
		idx, x, want int64
	}{{0, 10, 11}, {1, 10, 20}, {0, 41, 42}, {1, 21, 42}} {
		got, err := EvalInTable(funcs, table, apply, tc.idx, tc.x)
		if err != nil {
			t.Fatalf("EvalInTable(apply, %d, %d): %v", tc.idx, tc.x, err)
		}
		if got != tc.want {
			t.Errorf("apply(idx=%d, %d) = %d, want %d", tc.idx, tc.x, got, tc.want)
		}
	}
}

// An out-of-range function index is a clear error, not a wrong answer.
func TestEvalInTableCallIndirectOutOfRange(t *testing.T) {
	funcs, table := indirectFuncs()
	apply := funcs["apply"]
	if _, err := EvalInTable(funcs, table, apply, 5, 10); err == nil {
		t.Error("expected out-of-range function index to error")
	}
}
