package ssa

import "testing"

// callIndirectOp adds an OpCallIndirect dispatching on callee (a {fn, env} cell
// pointer) with the given args, and returns its result.
func callIndirectOp(f *Func, b *Block, callee Value, args ...Value) Value {
	all := append([]Value{callee}, args...)
	return f.AddOp(b, OpCallIndirect, all...)
}

// Dispatch targets take env as their last parameter (see
// docs/SSA-CLOSURE-DISPATCH.md); inc/dbl ignore it. apply(fn, x) calls the
// closure fn with x, and the callee gets (x, env). table = [inc, dbl].
func indirectFuncs() (map[string]*Func, []string) {
	inc := NewFunc("inc")
	ix := inc.AddParam()
	inc.AddParam() // env (unused)
	ie := inc.NewBlock()
	inc.SetRet(ie, inc.AddOp(ie, OpAdd, ix, constIn(inc, ie, 1)))

	dbl := NewFunc("dbl")
	dx := dbl.AddParam()
	dbl.AddParam() // env (unused)
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

// main builds {inc}/{dbl} closures and dispatches them through apply:
// apply(mkClosure(inc), 10) + apply(mkClosure(dbl), 10) = 11 + 20 = 31.
func TestEvalInTableCallIndirect(t *testing.T) {
	funcs, table := indirectFuncs()

	main := NewFunc("main")
	me := main.NewBlock()
	cInc := makeClosureOp(main, me, "inc") // {fn=inc, env}
	cDbl := makeClosureOp(main, me, "dbl")
	r0 := callOp(main, me, "apply", cInc, constIn(main, me, 10))
	r1 := callOp(main, me, "apply", cDbl, constIn(main, me, 10))
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

// Dispatching a closure directly through apply resolves the right callee.
func TestEvalInTableCallIndirectDirect(t *testing.T) {
	funcs, table := indirectFuncs()

	// mk(target, x): apply(mkClosure(target), x).
	mk := func(target string, x int64) int64 {
		m := NewFunc("m")
		me := m.NewBlock()
		c := makeClosureOp(m, me, target)
		m.SetRet(me, callOp(m, me, "apply", c, constIn(m, me, x)))
		funcs["m"] = m
		got, err := EvalInTable(funcs, table, m)
		if err != nil {
			t.Fatalf("EvalInTable(mk %s %d): %v", target, x, err)
		}
		return got
	}
	for _, tc := range []struct {
		target  string
		x, want int64
	}{{"inc", 10, 11}, {"dbl", 10, 20}, {"inc", 41, 42}, {"dbl", 21, 42}} {
		if got := mk(tc.target, tc.x); got != tc.want {
			t.Errorf("apply(%s, %d) = %d, want %d", tc.target, tc.x, got, tc.want)
		}
	}
}

// A closure over a target absent from the function-index table is a clear error.
func TestEvalInTableCallIndirectUnknownTarget(t *testing.T) {
	funcs, _ := indirectFuncs()
	m := NewFunc("m")
	me := m.NewBlock()
	c := makeClosureOp(m, me, "nope")
	m.SetRet(me, callOp(m, me, "apply", c, constIn(m, me, 1)))
	funcs["m"] = m
	if _, err := EvalInTable(funcs, []string{"inc", "dbl"}, m); err == nil {
		t.Error("expected unknown closure target to error")
	}
}
