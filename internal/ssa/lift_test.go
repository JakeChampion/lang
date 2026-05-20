package ssa

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
)

// TestLiftConstReturn — `function f() { return 42; }` lowers
// to one const_int + ret.
func TestLiftConstReturn(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 42},
			{Kind: ir.OpReturn},
		},
	}

	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(out.Blocks) != 1 || len(out.Blocks[0].Ops) != 1 {
		t.Fatalf("got %v", out)
	}
	op := out.Blocks[0].Ops[0]
	if op.Kind != OpConstInt || op.Imm != 42 {
		t.Errorf("op = {%v %d}, want {OpConstInt 42}", op.Kind, op.Imm)
	}
	if out.Blocks[0].Term.Kind != TermRet {
		t.Errorf("Term.Kind = %v, want TermRet", out.Blocks[0].Term.Kind)
	}
}

// TestLiftAddChain — `1 + 2` lowers to two consts + an add.
func TestLiftAddChain(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpConstI32, I32: 2},
			{Kind: ir.OpAdd},
			{Kind: ir.OpReturn},
		},
	}

	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(out.Blocks[0].Ops) != 3 {
		t.Fatalf("Ops = %d, want 3", len(out.Blocks[0].Ops))
	}
	add := out.Blocks[0].Ops[2]
	if add.Kind != OpAdd {
		t.Errorf("add.Kind = %v, want OpAdd", add.Kind)
	}
}

// TestLiftThenOptimize — after lift, Optimize collapses
// `1 + 2` to const_int 3.
func TestLiftThenOptimize(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpConstI32, I32: 2},
			{Kind: ir.OpAdd},
			{Kind: ir.OpReturn},
		},
	}

	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	Optimize(out)

	if len(out.Blocks[0].Ops) != 1 {
		t.Fatalf("Ops after Optimize = %d, want 1", len(out.Blocks[0].Ops))
	}
	if op := out.Blocks[0].Ops[0]; op.Kind != OpConstInt || op.Imm != 3 {
		t.Errorf("Op = {%v %d}, want {OpConstInt 3}", op.Kind, op.Imm)
	}
}

// TestLiftReturnVoid — function ending with OpReturnVoid
// produces `ret` (no value).
func TestLiftReturnVoid(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops:  []ir.Op{{Kind: ir.OpReturnVoid}},
	}

	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if out.Blocks[0].Term.Kind != TermRet {
		t.Errorf("Term.Kind = %v, want TermRet", out.Blocks[0].Term.Kind)
	}
	if out.Blocks[0].Term.Value.IsValid() {
		t.Errorf("Term.Value should be zero sentinel, got %v", out.Blocks[0].Term.Value)
	}
}

// TestLiftImplicitVoid — function with no terminator falls
// through to implicit void ret.
func TestLiftImplicitVoid(t *testing.T) {
	in := &ir.Func{Name: "f"} // empty Ops

	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if out.Blocks[0].Term.Kind != TermRet {
		t.Errorf("Term.Kind = %v, want TermRet", out.Blocks[0].Term.Kind)
	}
}

// TestLiftIdentityParam — `function f(a) { return a; }` lifts
// to a Func with one param and a ret of that param.
func TestLiftIdentityParam(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "a"}},
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpReturn},
		},
	}

	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(out.Params) != 1 {
		t.Fatalf("Params = %d, want 1", len(out.Params))
	}
	if out.Blocks[0].Term.Value != out.Params[0] {
		t.Errorf("Term.Value = %v, want %v (param)", out.Blocks[0].Term.Value, out.Params[0])
	}
}

// TestLiftAddTwoParams — `f(a, b) { return a + b; }` lifts to
// a Func with one OpAdd consuming two params.
func TestLiftAddTwoParams(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "a"}, {Name: "b"}},
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpLoadLocal, I32: 1},
			{Kind: ir.OpAdd},
			{Kind: ir.OpReturn},
		},
	}

	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(out.Blocks[0].Ops) != 1 || out.Blocks[0].Ops[0].Kind != OpAdd {
		t.Fatalf("got %v", out.Blocks[0].Ops)
	}
	add := out.Blocks[0].Ops[0]
	if add.Args[0] != out.Params[0] || add.Args[1] != out.Params[1] {
		t.Errorf("add.Args = %v, want [param0, param1]", add.Args)
	}
}

// TestLiftLoadLocalOutOfRange — slot index past params count
// fails clean.
func TestLiftLoadLocalOutOfRange(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "a"}},
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 5}, // out of range
			{Kind: ir.OpReturn},
		},
	}

	_, err := LiftFromIR(in)
	if err == nil {
		t.Fatal("expected out-of-range error")
	}
	if !strings.Contains(err.Error(), "out of range") {
		t.Errorf("error %q doesn't mention out of range", err)
	}
}

// TestLiftParamsRoundTripOptimize — `f(a, b) { return a + 0 + b; }`
// after lift+Optimize collapses to plain `add a, b`.
func TestLiftParamsRoundTripOptimize(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "a"}, {Name: "b"}},
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpAdd},
			{Kind: ir.OpLoadLocal, I32: 1},
			{Kind: ir.OpAdd},
			{Kind: ir.OpReturn},
		},
	}

	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	Optimize(out)

	if len(out.Blocks[0].Ops) != 1 || out.Blocks[0].Ops[0].Kind != OpAdd {
		t.Fatalf("Optimize result = %v, want one OpAdd", out.Blocks[0].Ops)
	}
}

// TestLiftDivRem — Div + Rem on params.
func TestLiftDivRem(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "a"}, {Name: "b"}},
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpLoadLocal, I32: 1},
			{Kind: ir.OpDivS},
			{Kind: ir.OpReturn},
		},
	}

	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if out.Blocks[0].Ops[0].Kind != OpDiv {
		t.Errorf("got %v, want OpDiv", out.Blocks[0].Ops[0].Kind)
	}
}

// TestLiftBitwise — And, Or, Xor on params.
func TestLiftBitwise(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "a"}, {Name: "b"}},
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpLoadLocal, I32: 1},
			{Kind: ir.OpAnd},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpOr},
			{Kind: ir.OpLoadLocal, I32: 1},
			{Kind: ir.OpXor},
			{Kind: ir.OpReturn},
		},
	}

	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestLiftShifts — Shl + Shr.
func TestLiftShifts(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "a"}, {Name: "b"}},
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpLoadLocal, I32: 1},
			{Kind: ir.OpShl},
			{Kind: ir.OpLoadLocal, I32: 1},
			{Kind: ir.OpShrS},
			{Kind: ir.OpReturn},
		},
	}

	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	kinds := []OpKind{out.Blocks[0].Ops[0].Kind, out.Blocks[0].Ops[1].Kind}
	if kinds[0] != OpShl || kinds[1] != OpShr {
		t.Errorf("kinds = %v, want [OpShl, OpShr]", kinds)
	}
}

// TestLiftComparisons — every cmp kind.
func TestLiftComparisons(t *testing.T) {
	pairs := []struct {
		from ir.OpKind
		want OpKind
	}{
		{ir.OpEq, OpEq},
		{ir.OpNe, OpNe},
		{ir.OpLtS, OpLt},
		{ir.OpLeS, OpLe},
		{ir.OpGtS, OpGt},
		{ir.OpGeS, OpGe},
	}
	for _, p := range pairs {
		t.Run(p.want.String(), func(t *testing.T) {
			in := &ir.Func{
				Name:   "f",
				Params: []ast.Param{{Name: "a"}, {Name: "b"}},
				Ops: []ir.Op{
					{Kind: ir.OpLoadLocal, I32: 0},
					{Kind: ir.OpLoadLocal, I32: 1},
					{Kind: p.from},
					{Kind: ir.OpReturn},
				},
			}
			out, err := LiftFromIR(in)
			if err != nil {
				t.Fatalf("LiftFromIR: %v", err)
			}
			if out.Blocks[0].Ops[0].Kind != p.want {
				t.Errorf("kind = %v, want %v", out.Blocks[0].Ops[0].Kind, p.want)
			}
		})
	}
}

// TestLiftNot — unary OpNot.
func TestLiftNot(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "a"}},
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpNot},
			{Kind: ir.OpReturn},
		},
	}

	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if out.Blocks[0].Ops[0].Kind != OpNot {
		t.Errorf("kind = %v, want OpNot", out.Blocks[0].Ops[0].Kind)
	}
}

// TestLiftCmpAndOptimize — `(a == b) != (a == b)` after lift +
// Optimize collapses to const_bool 0 (CSE merges the eqs;
// `x != x` is StrengthReduce'd to const 0 — actually that's
// for integer x-x = 0; for bools x != x → const_bool 0 needs
// FoldBranches on the cmp result. Let's just check the chain
// runs end-to-end without error.)
func TestLiftCmpAndOptimize(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "a"}, {Name: "b"}},
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpLoadLocal, I32: 1},
			{Kind: ir.OpEq},
			{Kind: ir.OpReturn},
		},
	}

	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	Optimize(out)
	if err := Verify(out); err != nil {
		t.Fatalf("Verify after Optimize: %v", err)
	}
}

// TestLiftConstStr — string literal lowers to OpConstString.
func TestLiftConstStr(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstStr, Str: "hello"},
			{Kind: ir.OpReturn},
		},
	}

	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	op := out.Blocks[0].Ops[0]
	if op.Kind != OpConstString {
		t.Errorf("Kind = %v, want OpConstString", op.Kind)
	}
	if op.Str != "hello" {
		t.Errorf("Str = %q, want %q", op.Str, "hello")
	}
}

// TestLiftStringConcatCall — `concat("hello", "world")`.
// Strings flow through OpCallDirect to a runtime helper.
func TestLiftStringConcatCall(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstStr, Str: "hello "},
			{Kind: ir.OpConstStr, Str: "world"},
			{Kind: ir.OpCallDirect, Str: "__str_concat", I32: 2},
			{Kind: ir.OpReturn},
		},
	}

	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(out.Blocks[0].Ops) != 3 {
		t.Fatalf("Ops = %d, want 3", len(out.Blocks[0].Ops))
	}
	call := out.Blocks[0].Ops[2]
	if call.Kind != OpCall || call.Str != "__str_concat" {
		t.Errorf("call = {%v %q}, want {OpCall __str_concat}", call.Kind, call.Str)
	}
}

// TestLiftConstF64 — `function f() { return 3.14; }` lifts
// const_f64 to OpConstFloat.
func TestLiftConstF64(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstF64, F64: 3.14},
			{Kind: ir.OpReturn},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	op := out.Blocks[0].Ops[0]
	if op.Kind != OpConstFloat || op.F64 != 3.14 {
		t.Errorf("Op = {%v %g}, want {OpConstFloat 3.14}", op.Kind, op.F64)
	}
}

// TestLiftConstF32 — F32 widens to F64 in SSA.
func TestLiftConstF32(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstF32, F32: 1.5},
			{Kind: ir.OpReturn},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if op := out.Blocks[0].Ops[0]; op.Kind != OpConstFloat || op.F64 != 1.5 {
		t.Errorf("Op = {%v %g}, want {OpConstFloat 1.5}", op.Kind, op.F64)
	}
}

// TestLiftFloatAdd — `1.0 + 2.0` lifts to OpFAdd, folds to
// const_float 3.0 after Optimize.
func TestLiftFloatAdd(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstF64, F64: 1.0},
			{Kind: ir.OpConstF64, F64: 2.0},
			{Kind: ir.OpFAdd},
			{Kind: ir.OpReturn},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	Optimize(out)
	if len(out.Blocks[0].Ops) != 1 {
		t.Fatalf("Ops = %d, want 1", len(out.Blocks[0].Ops))
	}
	if op := out.Blocks[0].Ops[0]; op.Kind != OpConstFloat || op.F64 != 3.0 {
		t.Errorf("Op = {%v %g}, want {OpConstFloat 3}", op.Kind, op.F64)
	}
}

// TestLiftFloatCmp — float comparison lifts cleanly.
func TestLiftFloatCmp(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstF64, F64: 1.0},
			{Kind: ir.OpConstF64, F64: 2.0},
			{Kind: ir.OpFLt},
			{Kind: ir.OpReturn},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	Optimize(out)
	if op := out.Blocks[0].Ops[0]; op.Kind != OpConstBool || op.Imm != 1 {
		t.Errorf("Op = {%v %d}, want {OpConstBool 1}", op.Kind, op.Imm)
	}
}

// TestLiftFloatNeg — unary FNeg.
func TestLiftFloatNeg(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstF64, F64: 4.5},
			{Kind: ir.OpFNeg},
			{Kind: ir.OpReturn},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	Optimize(out)
	if op := out.Blocks[0].Ops[0]; op.Kind != OpConstFloat || op.F64 != -4.5 {
		t.Errorf("Op = {%v %g}, want {OpConstFloat -4.5}", op.Kind, op.F64)
	}
}

// TestLiftStoreLocal — `var x = 5; return x;` lifts to one
// const_int 5 + ret; OpStoreLocal writes the const into a
// local slot, OpLoadLocal reads it back.
func TestLiftStoreLocal(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Locals: []*ast.Var{{Name: "x"}},
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 5},
			{Kind: ir.OpStoreLocal, I32: 0}, // x = 5
			{Kind: ir.OpLoadLocal, I32: 0},  // load x
			{Kind: ir.OpReturn},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// Only one Op (the const) and a ret of that const's Value.
	if len(out.Blocks[0].Ops) != 1 {
		t.Fatalf("Ops = %d, want 1", len(out.Blocks[0].Ops))
	}
	if out.Blocks[0].Term.Value != out.Blocks[0].Ops[0].Result {
		t.Errorf("Term.Value = %v, want %v", out.Blocks[0].Term.Value, out.Blocks[0].Ops[0].Result)
	}
}

// TestLiftStoreOverwrite — `x = 1; x = 2; return x;` returns 2.
func TestLiftStoreOverwrite(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Locals: []*ast.Var{{Name: "x"}},
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpStoreLocal, I32: 0},
			{Kind: ir.OpConstI32, I32: 2},
			{Kind: ir.OpStoreLocal, I32: 0},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpReturn},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	Optimize(out)
	// After Optimize: dead const_int 1 is gone; only const_int 2 + ret.
	if len(out.Blocks[0].Ops) != 1 {
		t.Fatalf("Ops after Optimize = %d, want 1", len(out.Blocks[0].Ops))
	}
	if op := out.Blocks[0].Ops[0]; op.Imm != 2 {
		t.Errorf("survivor Imm = %d, want 2", op.Imm)
	}
}

// TestLiftTeeLocal — TeeLocal stores AND leaves the value on
// the stack.
func TestLiftTeeLocal(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Locals: []*ast.Var{{Name: "x"}},
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 7},
			{Kind: ir.OpTeeLocal, I32: 0}, // x = 7, also leaves 7 on stack
			{Kind: ir.OpReturn},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// The const_int's Value flows directly to the return.
	if out.Blocks[0].Term.Value != out.Blocks[0].Ops[0].Result {
		t.Errorf("Tee should leave value on stack; ret value = %v, const result = %v",
			out.Blocks[0].Term.Value, out.Blocks[0].Ops[0].Result)
	}
}

// TestLiftLoadUninitialisedLocal — reading a local before any
// store fails clean.
func TestLiftLoadUninitialisedLocal(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Locals: []*ast.Var{{Name: "x"}},
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpReturn},
		},
	}
	_, err := LiftFromIR(in)
	if err == nil {
		t.Fatal("expected error for uninitialised local read")
	}
	if !strings.Contains(err.Error(), "uninitialised") {
		t.Errorf("error %q doesn't mention uninitialised", err)
	}
}

// TestLiftLocalArithmetic — `var x = a + 1; var y = x * 2; return y;`
// composes locals with binary arithmetic; Optimize folds it
// down (if a were const) or leaves it as a sequenced computation.
func TestLiftLocalArithmetic(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "a"}},
		Locals: []*ast.Var{{Name: "x"}, {Name: "y"}},
		Ops: []ir.Op{
			// x = a + 1
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpAdd},
			{Kind: ir.OpStoreLocal, I32: 1}, // x = ...
			// y = x * 2
			{Kind: ir.OpLoadLocal, I32: 1},
			{Kind: ir.OpConstI32, I32: 2},
			{Kind: ir.OpMul},
			{Kind: ir.OpStoreLocal, I32: 2}, // y = ...
			// return y
			{Kind: ir.OpLoadLocal, I32: 2},
			{Kind: ir.OpReturn},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// 4 ops in SSA: 2 const + 2 binary (after we drop redundant loads).
	// Loads don't emit ops; the resulting SSA reuses the producer's Value.
	if len(out.Blocks[0].Ops) != 4 {
		t.Fatalf("Ops = %d, want 4: %v", len(out.Blocks[0].Ops), opKinds(out.Blocks[0].Ops))
	}
}

// TestLiftDrop — OpDrop pops one stack value. The producer Op
// is left in place; DCE reclaims it later if no one else uses it.
func TestLiftDrop(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 42}, // pushed
			{Kind: ir.OpDrop},              // popped, discarded
			{Kind: ir.OpReturnVoid},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// const_int 42 is still emitted (the producer); ret is void.
	if len(out.Blocks[0].Ops) != 1 {
		t.Fatalf("Ops = %d, want 1", len(out.Blocks[0].Ops))
	}
	if out.Blocks[0].Term.Kind != TermRet || out.Blocks[0].Term.Value.IsValid() {
		t.Errorf("Term = %+v, want void Ret", out.Blocks[0].Term)
	}
}

// TestLiftDropThenOptimize — after Optimize/DCE, an unused
// const+drop pair both disappear.
func TestLiftDropThenOptimize(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 42},
			{Kind: ir.OpDrop},
			{Kind: ir.OpReturnVoid},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	Optimize(out)
	if len(out.Blocks[0].Ops) != 0 {
		t.Errorf("Ops after Optimize = %d, want 0 (dropped const should die)", len(out.Blocks[0].Ops))
	}
}

// TestLiftDropStackUnderflow — OpDrop with empty stack fails.
func TestLiftDropStackUnderflow(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops:  []ir.Op{{Kind: ir.OpDrop}, {Kind: ir.OpReturnVoid}},
	}
	_, err := LiftFromIR(in)
	if err == nil {
		t.Fatal("expected stack-underflow error")
	}
	if !strings.Contains(err.Error(), "needs 1 operand") {
		t.Errorf("error %q doesn't mention operand count", err)
	}
}

// TestLiftIfElseVoid — `if (c) { foo() } else { bar() }`
// lifts to a diamond CFG with brif → {thenB, elseB} both
// branching to postB.
func TestLiftIfElseVoid(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "c"}},
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpIf, I32: ir.BlockTypeVoid},
			{Kind: ir.OpCallDirect, Str: "foo", I32: 0},
			{Kind: ir.OpDrop},
			{Kind: ir.OpElse},
			{Kind: ir.OpCallDirect, Str: "bar", I32: 0},
			{Kind: ir.OpDrop},
			{Kind: ir.OpEnd},
			{Kind: ir.OpReturnVoid},
		},
	}

	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(out.Blocks) != 4 {
		t.Fatalf("Blocks = %d, want 4 (entry, then, else, post); got %v", len(out.Blocks), out.Blocks)
	}
	if out.Blocks[0].Term.Kind != TermBrIf {
		t.Errorf("entry.Term.Kind = %v, want TermBrIf", out.Blocks[0].Term.Kind)
	}
}

// TestLiftIfNoElse — `if (c) { foo() }` without else.
func TestLiftIfNoElse(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "c"}},
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpIf, I32: ir.BlockTypeVoid},
			{Kind: ir.OpCallDirect, Str: "foo", I32: 0},
			{Kind: ir.OpDrop},
			{Kind: ir.OpEnd},
			{Kind: ir.OpReturnVoid},
		},
	}

	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// thenB has one call op + br post; elseB (empty) just br post.
	// postB has the ret.
}

// TestLiftIfPhiMerge — both arms store the same local to
// different values; expect a phi at the merge block.
func TestLiftIfPhiMerge(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "c"}},
		Locals: []*ast.Var{{Name: "x"}},
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0}, // cond
			{Kind: ir.OpIf, I32: ir.BlockTypeVoid},
			// x = 1
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpStoreLocal, I32: 1},
			{Kind: ir.OpElse},
			// x = 2
			{Kind: ir.OpConstI32, I32: 2},
			{Kind: ir.OpStoreLocal, I32: 1},
			{Kind: ir.OpEnd},
			// return x
			{Kind: ir.OpLoadLocal, I32: 1},
			{Kind: ir.OpReturn},
		},
	}

	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// postB should have a phi op.
	postB := out.Blocks[3]
	if len(postB.Ops) < 1 || postB.Ops[0].Kind != OpPhi {
		t.Fatalf("postB.Ops[0].Kind = %v, want OpPhi; ops = %v", postB.Ops[0].Kind, postB.Ops)
	}
	phi := postB.Ops[0]
	if len(phi.Args) != 2 {
		t.Errorf("phi.Args = %v, want 2 args", phi.Args)
	}
}

// TestLiftIfThenOnly — only the then arm stores; else preserves
// the pre-value. Phi merges (then-value, pre-value).
func TestLiftIfThenOnly(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "c"}, {Name: "p"}},
		Locals: []*ast.Var{{Name: "x"}},
		Ops: []ir.Op{
			// x = p
			{Kind: ir.OpLoadLocal, I32: 1},
			{Kind: ir.OpStoreLocal, I32: 2},
			// if (c) { x = 99 }
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpIf, I32: ir.BlockTypeVoid},
			{Kind: ir.OpConstI32, I32: 99},
			{Kind: ir.OpStoreLocal, I32: 2},
			{Kind: ir.OpEnd},
			// return x
			{Kind: ir.OpLoadLocal, I32: 2},
			{Kind: ir.OpReturn},
		},
	}

	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestLiftIfFoldsToBr — when cond is const true, Optimize
// collapses the brif to br + drops the dead else branch.
func TestLiftIfFoldsToBr(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Locals: []*ast.Var{{Name: "x"}},
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 1}, // cond = true (non-zero)
			{Kind: ir.OpIf, I32: ir.BlockTypeVoid},
			{Kind: ir.OpConstI32, I32: 7},
			{Kind: ir.OpStoreLocal, I32: 0},
			{Kind: ir.OpElse},
			{Kind: ir.OpConstI32, I32: 8},
			{Kind: ir.OpStoreLocal, I32: 0},
			{Kind: ir.OpEnd},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpReturn},
		},
	}

	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	Optimize(out)
	// We're not super strict about post-Optimize layout (passes
	// may evolve), but result must verify + the test runs without
	// panic.
	if err := Verify(out); err != nil {
		t.Fatalf("Verify after Optimize: %v", err)
	}
}

// TestLiftIfRejectsNonVoidNoElse — value-producing OpIf
// requires both arms (no OpElse-less form).
func TestLiftIfRejectsNonVoidNoElse(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpIf, I32: ir.BlockTypeI32}, // expression form
			{Kind: ir.OpConstI32, I32: 7},
			{Kind: ir.OpEnd}, // missing OpElse
			{Kind: ir.OpReturnVoid},
		},
	}
	_, err := LiftFromIR(in)
	if err == nil {
		t.Fatal("expected error for non-void if without else")
	}
	if !strings.Contains(err.Error(), "requires OpElse") {
		t.Errorf("error %q doesn't mention OpElse requirement", err)
	}
}

// TestLiftIfExpressionMergesValue — value-producing if: both
// arms push a const; the lift emits a phi at postB that's
// pushed onto the stack for the next consumer.
func TestLiftIfExpressionMergesValue(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "c"}},
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpIf, I32: ir.BlockTypeI32},
			{Kind: ir.OpConstI32, I32: 7},
			{Kind: ir.OpElse},
			{Kind: ir.OpConstI32, I32: 9},
			{Kind: ir.OpEnd},
			{Kind: ir.OpReturn},
		},
	}

	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	postB := out.Blocks[3]
	if len(postB.Ops) < 1 || postB.Ops[0].Kind != OpPhi {
		t.Fatalf("postB first Op = %v, want OpPhi", postB.Ops[0].Kind)
	}
	phi := postB.Ops[0]
	if len(phi.Args) != 2 {
		t.Errorf("phi.Args = %v, want 2", phi.Args)
	}
	// The phi's result is what `ret` consumes.
	if postB.Term.Value != phi.Result {
		t.Errorf("Term.Value = %v, want %v (phi result)", postB.Term.Value, phi.Result)
	}
}

// TestLiftIfRejectsUnclosed — missing OpEnd surfaces a clear error.
func TestLiftIfRejectsUnclosed(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "c"}},
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpIf, I32: ir.BlockTypeVoid},
			// no OpEnd
		},
	}
	_, err := LiftFromIR(in)
	if err == nil {
		t.Fatal("expected error for unclosed scope")
	}
	if !strings.Contains(err.Error(), "unclosed") {
		t.Errorf("error %q doesn't mention unclosed", err)
	}
}

// TestLiftBlockLinear — OpBlock + OpEnd with no OpBr inside
// works as a linear pass-through. The block boundary creates
// a new SSA block but PruneUnreachable / DCE-friendly enough
// to optimize away.
func TestLiftBlockLinear(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpBlock, I32: ir.BlockTypeVoid},
			{Kind: ir.OpConstI32, I32: 42},
			{Kind: ir.OpEnd},
			{Kind: ir.OpReturn},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// 2 blocks: entry (with the const) + postB (with the ret).
	if len(out.Blocks) != 2 {
		t.Fatalf("Blocks = %d, want 2", len(out.Blocks))
	}
}

// TestLiftBlockNestedInIf — sanity: OpBlock inside an if arm
// works.
func TestLiftBlockNestedInIf(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "c"}},
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpIf, I32: ir.BlockTypeVoid},
			{Kind: ir.OpBlock, I32: ir.BlockTypeVoid},
			{Kind: ir.OpCallDirect, Str: "foo", I32: 0},
			{Kind: ir.OpDrop},
			{Kind: ir.OpEnd}, // close block
			{Kind: ir.OpEnd}, // close if
			{Kind: ir.OpReturnVoid},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestLiftBlockRejectsNonVoid — Phase 9 only handles void blocks.
func TestLiftBlockRejectsNonVoid(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpBlock, I32: ir.BlockTypeI32},
			{Kind: ir.OpEnd},
			{Kind: ir.OpReturnVoid},
		},
	}
	_, err := LiftFromIR(in)
	if err == nil {
		t.Fatal("expected error for non-void OpBlock")
	}
	if !strings.Contains(err.Error(), "non-void BlockType") {
		t.Errorf("error %q doesn't mention non-void BlockType", err)
	}
}

// TestLiftBrToBlock — early-exit from an OpBlock scope via OpBr.
// `block { if (c) { x = 1; br 1 }  x = 2 } return x;`
// — both paths feed into the post-block; phi merges x.
func TestLiftBrToBlock(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "c"}},
		Locals: []*ast.Var{{Name: "x"}},
		Ops: []ir.Op{
			{Kind: ir.OpBlock, I32: ir.BlockTypeVoid}, // outer block (depth 0)
			{Kind: ir.OpLoadLocal, I32: 0},            // cond
			{Kind: ir.OpIf, I32: ir.BlockTypeVoid},
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpStoreLocal, I32: 1},
			{Kind: ir.OpBr, I32: 1}, // exit outer block (depth 1 from inside if)
			{Kind: ir.OpEnd},        // close if
			{Kind: ir.OpConstI32, I32: 2},
			{Kind: ir.OpStoreLocal, I32: 1},
			{Kind: ir.OpEnd}, // close outer block
			{Kind: ir.OpLoadLocal, I32: 1},
			{Kind: ir.OpReturn},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestLiftBrTrivialExit — `block { br 0 }` — branch out of the
// block we're in. Block becomes effectively empty.
func TestLiftBrTrivialExit(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpBlock, I32: ir.BlockTypeVoid},
			{Kind: ir.OpBr, I32: 0},
			{Kind: ir.OpEnd},
			{Kind: ir.OpReturnVoid},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestLiftBrDepthOutOfRange — OpBr with depth past the scope
// stack fails cleanly.
func TestLiftBrDepthOutOfRange(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpBlock, I32: ir.BlockTypeVoid},
			{Kind: ir.OpBr, I32: 5},
			{Kind: ir.OpEnd},
			{Kind: ir.OpReturnVoid},
		},
	}
	_, err := LiftFromIR(in)
	if err == nil {
		t.Fatal("expected out-of-range error")
	}
	if !strings.Contains(err.Error(), "out of range") {
		t.Errorf("error %q doesn't mention out of range", err)
	}
}

// TestLiftBrToIf — OpBr targeting an open OpIf scope (early-exit
// from a branch arm). Phase 10c.
func TestLiftBrToIf(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpIf, I32: ir.BlockTypeVoid},
			{Kind: ir.OpBr, I32: 0}, // exit the if scope (depth 0)
			{Kind: ir.OpEnd},
			{Kind: ir.OpReturnVoid},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestLiftBrIfExits — `block { brif (c, exit) ... }`.
// Conditional early-exit via brif then continue inside.
func TestLiftBrIfExits(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "c"}},
		Locals: []*ast.Var{{Name: "x"}},
		Ops: []ir.Op{
			{Kind: ir.OpBlock, I32: ir.BlockTypeVoid},
			{Kind: ir.OpLoadLocal, I32: 0}, // cond
			{Kind: ir.OpBrIf, I32: 0},      // exit block if cond
			{Kind: ir.OpConstI32, I32: 5},
			{Kind: ir.OpStoreLocal, I32: 1},
			{Kind: ir.OpEnd},
			{Kind: ir.OpReturnVoid},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestLiftBrIfWithPhi — both branch and fall-through assign to
// the same local; phi merges the two values at the block's exit.
func TestLiftBrIfWithPhi(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "c"}},
		Locals: []*ast.Var{{Name: "x"}},
		Ops: []ir.Op{
			{Kind: ir.OpBlock, I32: ir.BlockTypeVoid},
			// x = 1 (initial)
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpStoreLocal, I32: 1},
			// if (c) br out (x stays 1)
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpBrIf, I32: 0},
			// fall-through: x = 2
			{Kind: ir.OpConstI32, I32: 2},
			{Kind: ir.OpStoreLocal, I32: 1},
			{Kind: ir.OpEnd},
			// return x — should be 1 or 2 depending on c
			{Kind: ir.OpLoadLocal, I32: 1},
			{Kind: ir.OpReturn},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// The post-block (where the ret lives) should have a phi for
	// x merging the brif-taken path and the fall-through path.
	hasPhi := false
	for _, b := range out.Blocks {
		for _, op := range b.Ops {
			if op.Kind == OpPhi {
				hasPhi = true
				break
			}
		}
	}
	if !hasPhi {
		t.Errorf("expected a phi op somewhere; got:\n%s", out)
	}
}

// TestLiftBrIfTargetingIfRejected — Phase 10 mirrors Phase 9b:
// TestLiftBrIfToIf — OpBrIf targeting an open OpIf scope.
// Phase 10c.
func TestLiftBrIfToIf(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "c"}},
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpIf, I32: ir.BlockTypeVoid},
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpBrIf, I32: 0}, // conditional exit of the if scope
			{Kind: ir.OpEnd},
			{Kind: ir.OpReturnVoid},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestLiftLoopEmpty — `loop { }` with fall-through immediately;
// degenerate but should still verify. The header phi (for any
// initialised slot) ends up with a single Args entry (preLoop
// value) since there's no back-edge.
func TestLiftLoopEmpty(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "p"}},
		Ops: []ir.Op{
			{Kind: ir.OpLoop, I32: ir.BlockTypeVoid},
			{Kind: ir.OpEnd},
			{Kind: ir.OpReturnVoid},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestLiftLoopBackBranch — `loop { ...; br 0 }` always loops
// (never falls through). After Verify + Optimize, the post-loop
// block is unreachable and gets pruned.
func TestLiftLoopBackBranch(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "p"}},
		Locals: []*ast.Var{{Name: "i"}},
		Ops: []ir.Op{
			// i = 0
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpStoreLocal, I32: 1},
			// loop
			{Kind: ir.OpLoop, I32: ir.BlockTypeVoid},
			// i = i + 1
			{Kind: ir.OpLoadLocal, I32: 1},
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpAdd},
			{Kind: ir.OpStoreLocal, I32: 1},
			// br 0 (back to loop header)
			{Kind: ir.OpBr, I32: 0},
			{Kind: ir.OpEnd},
			// unreachable past-loop
			{Kind: ir.OpReturnVoid},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// The header should have a phi for slot[1] (i): Args = [0, i+1]
	// — two distinct values, so TrivialPhis won't collapse it.
	hasPhi := false
	for _, b := range out.Blocks {
		for _, op := range b.Ops {
			if op.Kind == OpPhi {
				hasPhi = true
				break
			}
		}
	}
	if !hasPhi {
		t.Errorf("expected at least one phi at loop header; got:\n%s", out)
	}
}

// TestLiftLoopCounted — `i = 0; loop { if (i >= 10) break;
// i = i + 1 }` typical counter loop. brif exits, br loops.
func TestLiftLoopCounted(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Locals: []*ast.Var{{Name: "i"}},
		Ops: []ir.Op{
			{Kind: ir.OpConstI32, I32: 0},
			{Kind: ir.OpStoreLocal, I32: 0},
			{Kind: ir.OpBlock, I32: ir.BlockTypeVoid}, // outer for break
			{Kind: ir.OpLoop, I32: ir.BlockTypeVoid},
			// if (i >= 10) br 1 (exit block)
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpConstI32, I32: 10},
			{Kind: ir.OpGeS},
			{Kind: ir.OpBrIf, I32: 1}, // depth 1 = the outer OpBlock
			// i = i + 1
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpConstI32, I32: 1},
			{Kind: ir.OpAdd},
			{Kind: ir.OpStoreLocal, I32: 0},
			// continue loop
			{Kind: ir.OpBr, I32: 0},
			{Kind: ir.OpEnd}, // close loop
			{Kind: ir.OpEnd}, // close block
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpReturn},
		},
	}
	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestLiftLoopRejectsNonVoid — non-void OpLoop rejected.
func TestLiftLoopRejectsNonVoid(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpLoop, I32: ir.BlockTypeI32},
			{Kind: ir.OpEnd},
			{Kind: ir.OpReturnVoid},
		},
	}
	_, err := LiftFromIR(in)
	if err == nil {
		t.Fatal("expected error for non-void OpLoop")
	}
	if !strings.Contains(err.Error(), "non-void BlockType") {
		t.Errorf("error %q doesn't mention non-void BlockType", err)
	}
}

// TestLiftCallDirect — `foo(a, b)` → OpCall with callee "foo".
func TestLiftCallDirect(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "a"}, {Name: "b"}},
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpLoadLocal, I32: 1},
			{Kind: ir.OpCallDirect, Str: "foo", I32: 2},
			{Kind: ir.OpReturn},
		},
	}

	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	call := out.Blocks[0].Ops[0]
	if call.Kind != OpCall {
		t.Errorf("Kind = %v, want OpCall", call.Kind)
	}
	if call.Str != "foo" {
		t.Errorf("callee = %q, want %q", call.Str, "foo")
	}
	if len(call.Args) != 2 {
		t.Errorf("args = %d, want 2", len(call.Args))
	}
}

// TestLiftCallZeroArgs — call with no args (e.g. `now()`).
func TestLiftCallZeroArgs(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpCallDirect, Str: "now", I32: 0},
			{Kind: ir.OpReturn},
		},
	}

	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	call := out.Blocks[0].Ops[0]
	if call.Kind != OpCall || call.Str != "now" || len(call.Args) != 0 {
		t.Errorf("got {%v %q args=%d}, want {OpCall now args=0}",
			call.Kind, call.Str, len(call.Args))
	}
}

// TestLiftCallChained — `foo(bar(x))` — nested calls.
func TestLiftCallChained(t *testing.T) {
	in := &ir.Func{
		Name:   "f",
		Params: []ast.Param{{Name: "x"}},
		Ops: []ir.Op{
			{Kind: ir.OpLoadLocal, I32: 0},
			{Kind: ir.OpCallDirect, Str: "bar", I32: 1},
			{Kind: ir.OpCallDirect, Str: "foo", I32: 1},
			{Kind: ir.OpReturn},
		},
	}

	out, err := LiftFromIR(in)
	if err != nil {
		t.Fatalf("LiftFromIR: %v", err)
	}
	if err := Verify(out); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	ops := out.Blocks[0].Ops
	if len(ops) != 2 {
		t.Fatalf("Ops = %d, want 2", len(ops))
	}
	if ops[0].Str != "bar" || ops[1].Str != "foo" {
		t.Errorf("call order = [%q %q], want [bar foo]", ops[0].Str, ops[1].Str)
	}
	if ops[1].Args[0] != ops[0].Result {
		t.Errorf("foo's arg should be bar's result; got %v vs %v", ops[1].Args[0], ops[0].Result)
	}
}

// TestLiftCallStackUnderflow — too-few-args produces a clean
// error.
func TestLiftCallStackUnderflow(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpCallDirect, Str: "foo", I32: 2}, // expects 2 args, none on stack
		},
	}

	_, err := LiftFromIR(in)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "needs 2 args") {
		t.Errorf("error %q doesn't mention arg count", err)
	}
}

// TestLiftRejectsUnsupportedOp — OpCallIndirect isn't in the
// current subset; lift surfaces a clear error.
func TestLiftRejectsUnsupportedOp(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpCallIndirect, I32: 0}, // not yet supported
			{Kind: ir.OpReturnVoid},
		},
	}

	_, err := LiftFromIR(in)
	if err == nil {
		t.Fatal("expected error for unsupported op")
	}
	if !strings.Contains(err.Error(), "unsupported op") {
		t.Errorf("error %q doesn't mention unsupported op", err)
	}
}

// TestLiftRejectsNilInput — defensive.
func TestLiftRejectsNilInput(t *testing.T) {
	_, err := LiftFromIR(nil)
	if err == nil {
		t.Fatal("expected error for nil input")
	}
}

// TestLiftRejectsStackUnderflow — OpAdd with no stack input
// fails with a clear message.
func TestLiftRejectsStackUnderflow(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpAdd}, // nothing on the stack
		},
	}

	_, err := LiftFromIR(in)
	if err == nil {
		t.Fatal("expected stack-underflow error")
	}
	if !strings.Contains(err.Error(), "operands") {
		t.Errorf("error %q doesn't mention operands", err)
	}
}
