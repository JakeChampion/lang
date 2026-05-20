package ssa

import (
	"strings"
	"testing"
)

// TestPrintSimpleFunction — golden form for the canonical
//
//   func f(a, b) { return a + b; }
//
// shape. Whitespace-stable, single block, one Add + Ret.
func TestPrintSimpleFunction(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	sum := f.AddOp(entry, OpAdd, a, b)
	f.SetRet(entry, sum)

	want := strings.Join([]string{
		"func f(v1, v2):",
		"  block 1:",
		"    v3 = add v1, v2",
		"    ret v3",
		"",
	}, "\n")

	if got := f.String(); got != want {
		t.Errorf("Func.String() mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestPrintBranching — diamond CFG with const_int leaves.
// Brif renders cond + both targets, each branch prints its
// const + ret.
func TestPrintBranching(t *testing.T) {
	f := NewFunc("f")
	c := f.AddParam()
	entry := f.NewBlock()
	thenB := f.NewBlock()
	elseB := f.NewBlock()
	f.SetBrIf(entry, c, thenB, elseB)
	one := f.AddOp(thenB, OpConstInt)
	thenB.Ops[0].Imm = 1
	f.SetRet(thenB, one)
	two := f.AddOp(elseB, OpConstInt)
	elseB.Ops[0].Imm = 2
	f.SetRet(elseB, two)

	want := strings.Join([]string{
		"func f(v1):",
		"  block 1:",
		"    brif v1, block 2, block 3",
		"  block 2:",
		"    v2 = const_int 1",
		"    ret v2",
		"  block 3:",
		"    v3 = const_int 2",
		"    ret v3",
		"",
	}, "\n")

	if got := f.String(); got != want {
		t.Errorf("Func.String() mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestPrintConstString — OpConstString uses Go-quoted Str
// for safe round-tripping of newlines / quotes.
func TestPrintConstString(t *testing.T) {
	f := NewFunc("g")
	entry := f.NewBlock()
	op := &Op{Kind: OpConstString, Result: f.NewValue(), Str: "hi\nthere"}
	entry.Ops = append(entry.Ops, op)
	f.SetRet(entry, op.Result)

	want := strings.Join([]string{
		`func g():`,
		`  block 1:`,
		`    v1 = const_string "hi\nthere"`,
		`    ret v1`,
		"",
	}, "\n")

	if got := f.String(); got != want {
		t.Errorf("Func.String() mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestPrintCall — OpCall renders the callee name (from Str)
// and then comma-separated arg list.
func TestPrintCall(t *testing.T) {
	f := NewFunc("h")
	a := f.AddParam()
	entry := f.NewBlock()
	op := &Op{Kind: OpCall, Result: f.NewValue(), Str: "puts", Args: []Value{a}}
	entry.Ops = append(entry.Ops, op)
	f.SetRet(entry, op.Result)

	want := strings.Join([]string{
		`func h(v1):`,
		`  block 1:`,
		`    v2 = call "puts", v1`,
		`    ret v2`,
		"",
	}, "\n")

	if got := f.String(); got != want {
		t.Errorf("Func.String() mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestPrintSideEffectOp — Store has no Result; the printer
// omits the `<result> =` LHS.
func TestPrintSideEffectOp(t *testing.T) {
	f := NewFunc("s")
	addr := f.AddParam()
	val := f.AddParam()
	entry := f.NewBlock()
	f.AddOpNoResult(entry, OpStore, addr, val)
	f.SetRet(entry, Value{})

	want := strings.Join([]string{
		"func s(v1, v2):",
		"  block 1:",
		"    store v1, v2",
		"    ret",
		"",
	}, "\n")

	if got := f.String(); got != want {
		t.Errorf("Func.String() mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestPrintUnconditionalBranch — Br terminator with a single
// target block.
func TestPrintUnconditionalBranch(t *testing.T) {
	f := NewFunc("u")
	entry := f.NewBlock()
	target := f.NewBlock()
	f.SetBr(entry, target)
	f.SetRet(target, Value{})

	want := strings.Join([]string{
		"func u():",
		"  block 1:",
		"    br block 2",
		"  block 2:",
		"    ret",
		"",
	}, "\n")

	if got := f.String(); got != want {
		t.Errorf("Func.String() mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestPrintNilFunc — defensive: nil Func renders the sentinel
// rather than panicking.
func TestPrintNilFunc(t *testing.T) {
	var f *Func
	if got := f.String(); got != "<nil func>" {
		t.Errorf("(*Func)(nil).String() = %q, want %q", got, "<nil func>")
	}
}

// TestPrintMissingTerminator — a block without SetRet/SetBr
// renders the `<invalid>` placeholder. Lets debug dumps still
// be useful while a Builder is mid-construction.
func TestPrintMissingTerminator(t *testing.T) {
	f := NewFunc("m")
	a := f.AddParam()
	entry := f.NewBlock()
	f.AddOp(entry, OpAdd, a, a)
	// no terminator set

	got := f.String()
	if !strings.Contains(got, "<invalid>") {
		t.Errorf("expected <invalid> placeholder in:\n%s", got)
	}
}
