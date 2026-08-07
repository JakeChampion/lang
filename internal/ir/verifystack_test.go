// Negative coverage for the stack half of the IR verifier.
//
// The corpus gate shows the pass is quiet on ~8,000 real lowered
// functions at both pointer widths. A pass that checked nothing would
// look identical, so every detector is exercised here against hand-built
// IR that breaks exactly one invariant.
package ir

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// stackFn builds a function with n untyped local slots, the given result
// type, and the given ops.
func stackFn(name string, nSlots int, ret ast.Type, ops ...Op) *Func {
	return &Func{
		Name:         name,
		ReturnType:   ret,
		ScratchTypes: make([]ast.Type, nSlots),
		Ops:          ops,
	}
}

func verifyStackOnly(t *testing.T, f *Func, rest ...*Func) []Problem {
	t.Helper()
	known := map[string]*Func{f.Name: f}
	for _, r := range rest {
		known[r.Name] = r
	}
	problems, bail := verifyStack(f, known, map[string]*ExternFunc{}, 8)
	if bail != "" {
		t.Fatalf("function was skipped (%s) rather than checked", bail)
	}
	return problems
}

func TestVerifyStackRejects(t *testing.T) {
	i32 := ast.NumberType{Width: 32}
	tests := []struct {
		name string
		fn   *Func
		want string
	}{
		{
			// The break that silently reads whatever the register
			// allocator last left behind.
			name: "operand consumed that was never produced",
			fn: stackFn("f", 0, ast.VoidType{},
				Op{Kind: OpConstI32}, Op{Kind: OpAdd}, Op{Kind: OpDrop},
				Op{Kind: OpReturnVoid}),
			want: "operand stack underflow",
		},
		{
			name: "float op fed an integer",
			fn: stackFn("f", 0, ast.VoidType{},
				Op{Kind: OpConstI32}, Op{Kind: OpConstF64},
				Op{Kind: OpFAdd}, Op{Kind: OpDrop}, Op{Kind: OpReturnVoid}),
			want: "wants a float operand, but the stack holds a int",
		},
		{
			name: "integer op fed a float",
			fn: stackFn("f", 0, ast.VoidType{},
				Op{Kind: OpConstF64}, Op{Kind: OpConstI32},
				Op{Kind: OpAdd}, Op{Kind: OpDrop}, Op{Kind: OpReturnVoid}),
			want: "wants a int operand, but the stack holds a float",
		},
		{
			name: "block leaves more than its type promises",
			fn: stackFn("f", 0, ast.VoidType{},
				Op{Kind: OpBlock, I32: BlockTypeVoid},
				Op{Kind: OpConstI32},
				Op{Kind: OpEnd},
				Op{Kind: OpReturnVoid}),
			want: "leaves 1 stack slots, but its block type promises 0",
		},
		{
			name: "block leaves less than its type promises",
			fn: stackFn("f", 0, i32,
				Op{Kind: OpBlock, I32: BlockTypeI32},
				Op{Kind: OpEnd},
				Op{Kind: OpReturn}),
			want: "leaves 0 stack slots, but its block type promises 1",
		},
		{
			// The two arms of an if have to agree, or the value the
			// scope yields depends on which way the branch went.
			name: "then-arm disagrees with the block type",
			fn: stackFn("f", 0, i32,
				Op{Kind: OpConstI32},
				Op{Kind: OpIf, I32: BlockTypeI32},
				Op{Kind: OpConstI32}, Op{Kind: OpConstI32},
				Op{Kind: OpElse},
				Op{Kind: OpConstI32},
				Op{Kind: OpEnd},
				Op{Kind: OpReturn}),
			want: "the then-arm leaves 2 stack slots, but the if promises 1",
		},
		{
			name: "if with a result and no else arm",
			fn: stackFn("f", 0, i32,
				Op{Kind: OpConstI32},
				Op{Kind: OpIf, I32: BlockTypeI32},
				Op{Kind: OpConstI32},
				Op{Kind: OpEnd},
				Op{Kind: OpReturn}),
			want: "promises 1 stack slots but has no else arm",
		},
		{
			name: "branch carries less than its label needs",
			fn: stackFn("f", 0, i32,
				Op{Kind: OpBlock, I32: BlockTypeI32},
				Op{Kind: OpBr, I32: 0},
				Op{Kind: OpEnd},
				Op{Kind: OpReturn}),
			want: "branch to depth 0 needs 1 stack slots, but only 0 are available",
		},
		{
			name: "function ends holding nothing for its result",
			fn:   stackFn("f", 0, i32, Op{Kind: OpConstI32}, Op{Kind: OpDrop}),
			want: "function ends holding 0 stack slots, but its result needs 1",
		},
		{
			name: "function ends holding a value it does not return",
			fn:   stackFn("f", 0, ast.VoidType{}, Op{Kind: OpConstI32}),
			want: "function ends holding 1 stack slots, but its result needs 0",
		},
		{
			// A void callee leaves nothing, so consuming a result is
			// consuming something else's.
			name: "result taken from a callee that returns nothing",
			fn: stackFn("f", 0, ast.VoidType{},
				Op{Kind: OpCallDirect, Str: "g", I32: 0},
				Op{Kind: OpDrop},
				Op{Kind: OpReturnVoid}),
			want: "operand stack underflow",
		},
		{
			name: "store into a local the value never reached",
			fn: stackFn("f", 1, ast.VoidType{},
				Op{Kind: OpStoreLocal, I32: 0}, Op{Kind: OpReturnVoid}),
			want: "operand stack underflow",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := verifyStackOnly(t, tc.fn, &Func{Name: "g", ReturnType: ast.VoidType{}})
			if len(got) != 1 {
				t.Fatalf("got %d problems, want exactly 1:%s", len(got), FormatProblems(got, 10))
			}
			if !strings.Contains(got[0].Error(), tc.want) {
				t.Errorf("problem %q does not mention %q", got[0].Error(), tc.want)
			}
		})
	}
}

// Code after an unconditional branch or a return is unreachable, and
// wasm validation types it polymorphically: it may pop operands nothing
// pushed. Reporting that would fire on the tail of nearly every lowered
// loop.
func TestVerifyStackAllowsUnreachableCode(t *testing.T) {
	f := stackFn("f", 0, ast.VoidType{},
		Op{Kind: OpBlock, I32: BlockTypeVoid},
		Op{Kind: OpBr, I32: 0},
		Op{Kind: OpAdd}, // pops two operands that were never pushed
		Op{Kind: OpDrop},
		Op{Kind: OpEnd},
		Op{Kind: OpReturnVoid},
	)
	if got := verifyStackOnly(t, f); len(got) > 0 {
		t.Errorf("unreachable code reported problems:%s", FormatProblems(got, 10))
	}
}

// A loop label carries the loop's parameters, not its results, so a
// branch that restarts a value-producing loop carries nothing.
func TestVerifyStackAllowsBranchToLoop(t *testing.T) {
	f := stackFn("f", 0, ast.NumberType{Width: 32},
		Op{Kind: OpLoop, I32: BlockTypeI32},
		Op{Kind: OpConstI32},
		Op{Kind: OpBrIf, I32: 0},
		Op{Kind: OpConstI32},
		Op{Kind: OpEnd},
		Op{Kind: OpReturn},
	)
	if got := verifyStackOnly(t, f); len(got) > 0 {
		t.Errorf("branch to a loop reported problems:%s", FormatProblems(got, 10))
	}
}

// Under the two-word string ABI a string value is its (data, len) pair,
// so the same op list is well-formed at one pointer width and not at the
// other. The pass has to be told which target it is looking at.
func TestVerifyStackIsTargetAware(t *testing.T) {
	f := &Func{
		Name:       "f",
		ReturnType: ast.StringType{},
		Ops: []Op{
			{Kind: OpConstStr, Str: "x"},
			{Kind: OpReturn},
		},
	}
	known := map[string]*Func{"f": f}
	for _, tc := range []struct {
		ptrW int
		name string
	}{{4, "wasm32"}, {8, "native"}} {
		problems, bail := verifyStack(f, known, map[string]*ExternFunc{}, tc.ptrW)
		if bail != "" {
			t.Fatalf("%s: skipped (%s)", tc.name, bail)
		}
		if len(problems) > 0 {
			t.Errorf("%s: %s", tc.name, FormatProblems(problems, 5))
		}
	}

	// Dropping a string with a single drop balances on a native target
	// and leaves the length word behind on wasm32 — the same op list,
	// well-formed at one width and not at the other, which is what makes
	// the width a parameter rather than a constant.
	half := stackFn("h", 0, ast.VoidType{},
		Op{Kind: OpBlock, I32: BlockTypeVoid},
		Op{Kind: OpConstStr, Str: "x"},
		Op{Kind: OpDrop},
		Op{Kind: OpEnd},
		Op{Kind: OpReturnVoid},
	)
	if problems, _ := verifyStack(half, map[string]*Func{"h": half}, nil, 8); len(problems) > 0 {
		t.Errorf("native: %s", FormatProblems(problems, 5))
	}
	problems, _ := verifyStack(half, map[string]*Func{"h": half}, nil, 4)
	if len(problems) == 0 {
		t.Error("wasm32: dropping half a string pair went unreported")
	}
}

// Anything the pass cannot model must skip the function and say why,
// never report a problem it is not sure of.
func TestVerifyStackSkipsRatherThanGuesses(t *testing.T) {
	// A call whose result is an unresolved type parameter: the slot it
	// leaves could be an int, a float, or a two-word string.
	callee := &Func{Name: "id", ReturnType: ast.ParamType{Name: "T"},
		Params: []ast.Param{{Name: "x", Type: ast.NumberType{Width: 32}}}}
	f := stackFn("f", 0, ast.VoidType{},
		Op{Kind: OpConstI32},
		Op{Kind: OpCallDirect, Str: "id", I32: 1},
		Op{Kind: OpDrop},
		Op{Kind: OpReturnVoid})

	problems, bail := verifyStack(f, map[string]*Func{"f": f, "id": callee}, nil, 8)
	if bail == "" {
		t.Fatal("an erased result type was modelled rather than skipped")
	}
	if len(problems) > 0 {
		t.Errorf("a skipped function still reported problems:%s", FormatProblems(problems, 5))
	}
}
