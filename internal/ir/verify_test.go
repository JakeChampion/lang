// Negative coverage for the IR verifier.
//
// The corpus gate (TestIRVerifierAcceptsEveryLoweredCase) shows the
// verifier is quiet on 478 real lowered programs. On its own that is
// equally consistent with a verifier that checks nothing, so each
// detector is exercised here against hand-built malformed IR.
package ir

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// fn builds a function with n addressable local slots and the given ops.
func fn(name string, nSlots int, ops ...Op) *Func {
	return &Func{
		Name:         name,
		ScratchTypes: make([]ast.Type, nSlots),
		Ops:          ops,
	}
}

func verifyOne(f *Func, rest ...*Func) []Problem {
	return Verify(&Program{Funcs: append([]*Func{f}, rest...)})
}

func TestVerifyRejects(t *testing.T) {
	tests := []struct {
		name string
		prog []*Func
		want string
	}{
		{
			name: "local index past the frame",
			prog: []*Func{fn("f", 2, Op{Kind: OpLoadLocal, I32: 2})},
			want: "local index 2 is outside the frame",
		},
		{
			name: "negative local index",
			prog: []*Func{fn("f", 2, Op{Kind: OpStoreLocal, I32: -1})},
			want: "local index -1 is outside the frame",
		},
		{
			name: "tee past the frame",
			prog: []*Func{fn("f", 1, Op{Kind: OpTeeLocal, I32: 5})},
			want: "local index 5 is outside the frame",
		},
		{
			name: "scope never closed",
			prog: []*Func{fn("f", 0, Op{Kind: OpBlock}, Op{Kind: OpReturnVoid})},
			want: "scope is never closed",
		},
		{
			name: "end with no scope",
			prog: []*Func{fn("f", 0, Op{Kind: OpEnd})},
			want: "end with no open scope",
		},
		{
			name: "else with no scope",
			prog: []*Func{fn("f", 0, Op{Kind: OpElse})},
			want: "else with no open scope",
		},
		{
			name: "else closing a block",
			prog: []*Func{fn("f", 0, Op{Kind: OpBlock}, Op{Kind: OpElse}, Op{Kind: OpEnd})},
			want: "else closes a block opened at op 0, not an if",
		},
		{
			name: "two elses for one if",
			prog: []*Func{fn("f", 0,
				Op{Kind: OpIf}, Op{Kind: OpElse}, Op{Kind: OpElse}, Op{Kind: OpEnd})},
			want: "second else for the if opened at op 0",
		},
		{
			// The invariant that silently branches into unrelated code:
			// two scopes are open, so depth 3 has no target.
			name: "branch depth with no target",
			prog: []*Func{fn("f", 0,
				Op{Kind: OpBlock}, Op{Kind: OpLoop},
				Op{Kind: OpBr, I32: 3},
				Op{Kind: OpEnd}, Op{Kind: OpEnd})},
			want: "branch depth 3 has no target — only 2 scopes are open",
		},
		{
			name: "negative branch depth",
			prog: []*Func{fn("f", 0, Op{Kind: OpBrIf, I32: -1})},
			want: "branch depth -1 has no target",
		},
		{
			name: "call to a name that does not exist",
			prog: []*Func{fn("f", 0, Op{Kind: OpCallDirect, Str: "nowhere", I32: 0})},
			want: `calls "nowhere", which is not a defined function`,
		},
		{
			name: "call with no callee name",
			prog: []*Func{fn("f", 0, Op{Kind: OpCallDirect, Str: "", I32: 0})},
			want: "call with no callee name",
		},
		{
			name: "call arity mismatch",
			prog: []*Func{
				fn("f", 0, Op{Kind: OpCallDirect, Str: "g", I32: 4}),
				&Func{Name: "g", Params: make([]ast.Param, 2)},
			},
			want: "calls g with 4 args, but it declares 2 parameters",
		},
		{
			name: "negative argument count",
			prog: []*Func{
				fn("f", 0, Op{Kind: OpCallDirect, Str: "g", I32: -1}),
				&Func{Name: "g", Params: nil},
			},
			want: "negative argument count -1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Verify(&Program{Funcs: tc.prog})
			if len(got) != 1 {
				t.Fatalf("got %d problems, want exactly 1:%s", len(got), FormatProblems(got, 10))
			}
			if !strings.Contains(got[0].Error(), tc.want) {
				t.Errorf("problem %q does not mention %q", got[0].Error(), tc.want)
			}
		})
	}
}

func TestVerifyAcceptsWellFormedIR(t *testing.T) {
	callee := &Func{Name: "g", Params: make([]ast.Param, 2)}
	f := fn("f", 3,
		Op{Kind: OpConstI32, I32: 1},
		Op{Kind: OpStoreLocal, I32: 2},
		Op{Kind: OpBlock},
		Op{Kind: OpLoop},
		Op{Kind: OpLoadLocal, I32: 0},
		Op{Kind: OpBrIf, I32: 1},
		Op{Kind: OpBr, I32: 0},
		Op{Kind: OpEnd},
		Op{Kind: OpEnd},
		Op{Kind: OpLoadLocal, I32: 1},
		Op{Kind: OpLoadLocal, I32: 2},
		Op{Kind: OpCallDirect, Str: "g", I32: 2},
		Op{Kind: OpReturn},
	)
	if got := verifyOne(f, callee); len(got) > 0 {
		t.Errorf("well-formed IR reported problems:%s", FormatProblems(got, 10))
	}
}

// A closure target takes its environment pointer as a trailing
// parameter the call site does not push, so one-fewer-arg is legal and
// must not be reported.
func TestVerifyAllowsClosureEnvArity(t *testing.T) {
	callee := &Func{Name: "clo", Params: make([]ast.Param, 3)}
	f := fn("f", 0, Op{Kind: OpCallDirect, Str: "clo", I32: 2})
	if got := verifyOne(f, callee); len(got) > 0 {
		t.Errorf("closure-env arity reported problems:%s", FormatProblems(got, 10))
	}
}

// Builtins and __-prefixed runtime helpers are provided by the backends,
// not the program, so a call to one is not an unresolved callee.
func TestVerifyAllowsBuiltinsAndRuntimeHelpers(t *testing.T) {
	f := fn("f", 0,
		Op{Kind: OpCallDirect, Str: "print", I32: 1},
		Op{Kind: OpCallDirect, Str: "now_ns", I32: 0},
		Op{Kind: OpCallDirect, Str: "__fern_alloc", I32: 1},
	)
	if got := verifyOne(f); len(got) > 0 {
		t.Errorf("builtin / runtime-helper calls reported problems:%s", FormatProblems(got, 10))
	}
}

// A branch may name the implicit function-body scope, one past the
// innermost open scope — that is how a lowered `return` leaves the
// outermost block. Reporting it would fire on almost every real function.
func TestVerifyAllowsBranchToFunctionBody(t *testing.T) {
	f := fn("f", 0,
		Op{Kind: OpBlock},
		Op{Kind: OpBr, I32: 1},
		Op{Kind: OpEnd},
	)
	if got := verifyOne(f); len(got) > 0 {
		t.Errorf("branch to the function body reported problems:%s", FormatProblems(got, 10))
	}
}
