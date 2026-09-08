package semir

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/ssa"
)

func checkedProgram(t testing.TB, source string) (*ast.Program, *checker.Info) {
	t.Helper()
	prog, err := parser.Parse(source + "\nfunction main(): i32 { return 0; }")
	if err != nil {
		t.Fatal(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatal(err)
	}
	return prog, info
}

func firstCall(t *testing.T, f *Func) *ssa.Op {
	t.Helper()
	for _, block := range f.graph.Blocks {
		for _, op := range block.Ops {
			if op.Kind == ssa.OpSemanticCall {
				return op
			}
		}
	}
	t.Fatal("no semantic call")
	return nil
}

func TestDirectCallContracts(t *testing.T) {
	prog, info := checkedProgram(t, `
function pilot(own items: string[]): string[] { return replace(items, items); }
function replace(reader: string[], own taken: string[]): string[] { return taken; }
`)
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	caller, callee := p.funcs[0], p.funcs[1]
	call := firstCall(t, caller)
	target, err := caller.callee(call)
	if err != nil || target != callee {
		t.Fatalf("forward call has wrong identity: %v", err)
	}
	effects, err := ownershipEffects(caller)
	if err != nil {
		t.Fatal(err)
	}
	e := effects.ops[call]
	if e.result != resultCall || e.callee != callee || e.inputs[0].consume || !e.inputs[1].consume {
		t.Fatalf("lost own/borrow contract or invented result ownership: %+v", e)
	}
	if e.inputs[0].value != e.inputs[1].value {
		t.Fatal("aliases of one value must not become independent argument identities")
	}
	if ssa.BuildUses(caller.graph).Count(caller.graph.Params[0]) != 2 {
		t.Fatal("both argument uses must be visible before selecting a transfer")
	}
	// The owned slot needs a counted unit while the borrowed slot still reads
	// the same object. No resultCounted or unique verdict follows from this.
}

func TestRecursiveCallContracts(t *testing.T) {
	prog, info := checkedProgram(t, `
function pilot(items: string[], stop: boolean): string[] {
  if (stop) { return items; }
  return next(items, stop);
}
function next(items: string[], stop: boolean): string[] { return pilot(items, stop); }
`)
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		f := p.funcs[i]
		call := firstCall(t, f)
		target, err := f.callee(call)
		if err != nil || target != p.funcs[1-i] {
			t.Fatalf("recursive function identity lost: %v", err)
		}
		effects, err := ownershipEffects(f)
		if err != nil || effects.ops[call].result != resultCall {
			t.Fatalf("recursion invented a return ownership summary: %v", err)
		}
	}
}

func TestInvalidCallContracts(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Program, *ssa.Op)
		want string
	}{
		{"missing-callee", func(_ *Program, op *ssa.Op) { op.Imm = 0 }, "function identity"},
		{"outside-module", func(p *Program, op *ssa.Op) { op.Imm = int64(len(p.funcs) + 1) }, "function identity"},
		{"missing-argument", func(_ *Program, op *ssa.Op) { op.Args = nil }, "arity"},
		{"wrong-result", func(p *Program, op *ssa.Op) {
			p.funcs[0].values[op.Result.ID] = valueInfo{typ: ast.StringType{}}
		}, "arity"},
		{"wrong-param", func(p *Program, _ *ssa.Op) {
			p.funcs[1].contract.params[0] = ast.ArrayType{Elem: ast.NumberType{}}
		}, "call contract"},
		{"stale-own-contract", func(p *Program, _ *ssa.Op) { p.funcs[1].contract.modes[0] = ParamBorrow }, "call contract"},
		{"missing-mode", func(p *Program, _ *ssa.Op) { p.funcs[1].contract.modes = nil }, "call contract"},
		{"foreign-function", func(p *Program, _ *ssa.Op) { p.funcs[1].program = &Program{} }, "call contract"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, info := checkedProgram(t, `
function pilot(own items: string[]): string[] { return replace(items); }
function replace(own items: string[]): string[] { return items; }
`)
			p, err := BuildProgram(prog, info)
			if err != nil {
				t.Fatal(err)
			}
			tc.edit(p, firstCall(t, p.funcs[0]))
			if err := VerifyProgram(p); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("VerifyProgram = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestCalledUnsupportedBodyIsNotSilentlyAccepted(t *testing.T) {
	prog, info := checkedProgram(t, `
function pilot(items: string[]): string[] { return replace(items); }
function replace(items: string[]): string[] { loop { defer cleanup(); break; } return items; }
function cleanup(): void {}
`)
	if p, err := BuildProgram(prog, info); p != nil || err == nil || !strings.Contains(err.Error(), "iteration cleanup") {
		t.Fatalf("callee without typed body accepted: %v", err)
	}
}

func TestCallEffectsRejectStaleOwnershipContract(t *testing.T) {
	prog, info := checkedProgram(t, `
function pilot(own items: string[]): string[] { return replace(items); }
function replace(own items: string[]): string[] { return items; }
`)
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	p.funcs[1].contract.modes[0] = ParamBorrow
	if effects, err := ownershipEffects(p.funcs[0]); effects != nil || err == nil {
		t.Fatal("call analysis treated a stale own contract as a borrow")
	}
}

func TestBuildRejectsUncheckedOwnChange(t *testing.T) {
	prog, info := checkedProgram(t, `function pilot(own items: string[]): string[] { return items; }`)
	prog.Funcs[0].Params[0].Own = false
	if p, err := BuildProgram(prog, info); p != nil || err == nil {
		t.Fatal("declaration changed ownership after checking but was accepted")
	}
}
