package ssa

import (
	"strings"
	"testing"
)

// TestSelectConstTrueAliases — select(const_bool 1, a, b) → a.
func TestSelectConstTrueAliases(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	c := f.AddOp(entry, OpConstBool)
	entry.Ops[0].Imm = 1
	sel := f.AddOp(entry, OpSelect, c, a, b)
	f.SetRet(entry, sel)

	Simplify(f)
	if entry.Term.Value != a {
		t.Errorf("Term.Value = %v, want %v (select-true → ifTrue)", entry.Term.Value, a)
	}
}

// TestSelectConstFalseAliases — select(const_bool 0, a, b) → b.
func TestSelectConstFalseAliases(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	c := f.AddOp(entry, OpConstBool)
	entry.Ops[0].Imm = 0
	sel := f.AddOp(entry, OpSelect, c, a, b)
	f.SetRet(entry, sel)

	Simplify(f)
	if entry.Term.Value != b {
		t.Errorf("Term.Value = %v, want %v (select-false → ifFalse)", entry.Term.Value, b)
	}
}

// TestSelectIdenticalBranches — select(c, a, a) → a regardless of cond.
func TestSelectIdenticalBranches(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	a := f.AddParam()
	entry := f.NewBlock()
	sel := f.AddOp(entry, OpSelect, c, a, a)
	f.SetRet(entry, sel)

	Simplify(f)
	if entry.Term.Value != a {
		t.Errorf("Term.Value = %v, want %v (both branches identical)", entry.Term.Value, a)
	}
}

// TestSelectLeavesGeneric — select with non-const cond and
// distinct branches stays.
func TestSelectLeavesGeneric(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	sel := f.AddOp(entry, OpSelect, c, a, b)
	f.SetRet(entry, sel)

	Simplify(f)
	if entry.Ops[0].Kind != OpSelect {
		t.Errorf("Kind = %v, want OpSelect (no simplify trigger)", entry.Ops[0].Kind)
	}
}

// TestSelectPrints — Func.String renders args as a 3-tuple.
func TestSelectPrints(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	sel := f.AddOp(entry, OpSelect, c, a, b)
	f.SetRet(entry, sel)

	got := f.String()
	want := "v4 = select v1, v2, v3"
	if !strings.Contains(got, want) {
		t.Errorf("Func.String() missing %q in:\n%s", want, got)
	}
}

// TestSelectBoolPassthrough — `select(c, true, false)` aliases
// to `c` directly. Both branch operands must be const_bool with
// the matching values; anything else (e.g. const_bool true/true
// or const_int 1/const_int 0) falls through to other rules.
func TestSelectBoolPassthrough(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	tv := f.AddOp(entry, OpConstBool)
	entry.Ops[0].Imm = 1
	fv := f.AddOp(entry, OpConstBool)
	entry.Ops[1].Imm = 0
	sel := f.AddOp(entry, OpSelect, c, tv, fv)
	f.SetRet(entry, sel)
	_ = sel

	Simplify(f)

	if entry.Term.Value != c {
		t.Errorf("Term.Value = %v, want %v (select(c, true, false) → c)",
			entry.Term.Value, c)
	}
}

// TestSelectBoolPassthroughInverted — `select(c, false, true)`
// is NOT yet handled (would need to mint an OpNot). Verify the
// safe behaviour — the OpSelect is left alone.
func TestSelectBoolPassthroughInverted(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	fv := f.AddOp(entry, OpConstBool)
	entry.Ops[0].Imm = 0
	tv := f.AddOp(entry, OpConstBool)
	entry.Ops[1].Imm = 1
	sel := f.AddOp(entry, OpSelect, c, fv, tv)
	f.SetRet(entry, sel)

	Simplify(f)

	if entry.Ops[2].Kind != OpSelect {
		t.Errorf("Kind = %v, want OpSelect (false/true case not yet rewritten)",
			entry.Ops[2].Kind)
	}
}

// TestSelectInOptimizePipeline — `select(const_bool true, x, y)`
// goes away via Simplify + DCE.
func TestSelectInOptimizePipeline(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	y := f.AddParam()
	entry := f.NewBlock()
	c := f.AddOp(entry, OpConstBool)
	entry.Ops[0].Imm = 1
	sel := f.AddOp(entry, OpSelect, c, x, y)
	f.SetRet(entry, sel)

	Optimize(f)

	// entry.Ops should be empty (const + select both dead);
	// ret should point at x directly.
	if entry.Term.Value != x {
		t.Errorf("Term.Value = %v, want %v", entry.Term.Value, x)
	}
}
