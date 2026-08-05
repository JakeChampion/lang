package ssa

import "testing"

func makeClosureOp(f *Func, b *Block, target string, caps ...Value) Value {
	v := f.AddOp(b, OpMakeClosure, caps...)
	b.Ops[len(b.Ops)-1].Str = target
	return v
}

func makeEnvOp(f *Func, b *Block, caps ...Value) Value {
	return f.AddOp(b, OpMakeEnv, caps...)
}

// OpMakeClosure builds a {fn_idx, env_ptr, drop_idx, env_ptr} cell over an env
// block of captures. f(a,b) makes a closure over "inc" (function-value index 1 —
// indices are 1-based, 0 being the null reference) capturing (a,b), then reads
// back fn_idx, and both captures via the env pointer, combining them so a
// divergence in any field shows up: fn_idx*1000 + cap0*10 + cap1.
func TestEvalInTableMakeClosure(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	e := f.NewBlock()
	c := makeClosureOp(f, e, "inc", a, b)
	fnIdx := loadNOp(f, e, c, 0, OpLoad)
	env := loadNOp(f, e, c, 8, OpLoad)
	cap0 := loadNOp(f, e, env, 0, OpLoad)
	cap1 := loadNOp(f, e, env, 8, OpLoad)
	// fn_idx*1000 + cap0*10 + cap1
	t1 := f.AddOp(e, OpMul, fnIdx, constIn(f, e, 1000))
	t2 := f.AddOp(e, OpMul, cap0, constIn(f, e, 10))
	sum := f.AddOp(e, OpAdd, f.AddOp(e, OpAdd, t1, t2), cap1)
	f.SetRet(e, sum)

	funcs := map[string]*Func{"f": f}
	table := []string{"inc"} // inc's function-value index is 1
	got, err := EvalInTable(funcs, table, f, 5, 7)
	if err != nil {
		t.Fatalf("EvalInTable: %v", err)
	}
	if got != 1057 { // 1*1000 + 5*10 + 7
		t.Errorf("closure round-trip = %d, want 1057", got)
	}
}

// OpMakeEnv builds just the env block (no cell). f(a,b) reads back both
// captures: cap0*10 + cap1.
func TestEvalInTableMakeEnv(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	e := f.NewBlock()
	env := makeEnvOp(f, e, a, b)
	cap0 := loadNOp(f, e, env, 0, OpLoad)
	cap1 := loadNOp(f, e, env, 8, OpLoad)
	f.SetRet(e, f.AddOp(e, OpAdd, f.AddOp(e, OpMul, cap0, constIn(f, e, 10)), cap1))

	got, err := EvalInTable(map[string]*Func{"f": f}, nil, f, 5, 7)
	if err != nil {
		t.Fatalf("EvalInTable: %v", err)
	}
	if got != 57 {
		t.Errorf("env round-trip = %d, want 57", got)
	}
}

// A closure over a target absent from the table is a clear error.
func TestEvalInTableMakeClosureUnknownTarget(t *testing.T) {
	f := NewFunc("f")
	e := f.NewBlock()
	f.SetRet(e, makeClosureOp(f, e, "nope"))
	if _, err := EvalInTable(map[string]*Func{"f": f}, []string{"inc"}, f); err == nil {
		t.Error("expected unknown closure target to error")
	}
}
