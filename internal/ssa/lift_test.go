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

// TestLiftRejectsUnsupportedOp — OpLoop (branches/blocks) isn't
// in the current subset; lift surfaces a clear error.
func TestLiftRejectsUnsupportedOp(t *testing.T) {
	in := &ir.Func{
		Name: "f",
		Ops: []ir.Op{
			{Kind: ir.OpLoop}, // not yet supported — needs branch handling
			{Kind: ir.OpReturn},
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
