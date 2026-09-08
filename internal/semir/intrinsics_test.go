package semir

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ssa"
)

func TestSourceAppendUsesTypedIntrinsic(t *testing.T) {
	prog, info := checkedProgram(t, `function pilot(items: string[], item: string): string[] { return items.append(item); }`)
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	f := p.funcs[0]
	op := f.graph.Entry.Ops[0]
	if op.Kind != ssa.OpArrayAppend || len(op.Args) != 2 || op.Args[0] != f.graph.Params[0] || op.Args[1] != f.graph.Params[1] {
		t.Fatal("append lost semantic identity or evaluation-order operands")
	}
	flow, err := solveReturnFlow(p)
	if err != nil {
		t.Fatal(err)
	}
	result := flow.funcs[f].result
	requireSources(t, result, "p0", "generated")
	requireSources(t, result.children[0], "p0/element", "p1")
	for call := range info.IntrinsicCalls {
		delete(info.IntrinsicCalls, call)
	}
	if _, err := BuildProgram(prog, info); err == nil {
		t.Fatal("missing semantic identity was reconstructed from a helper name")
	}
}

func TestAppendContractRejectsStaleTypes(t *testing.T) {
	prog, info := checkedProgram(t, `function pilot(items: string[], item: string): string[] { return items.append(item); }`)
	for call, contract := range info.IntrinsicCalls {
		contract.Signature = &ast.FuncType{
			Params: []ast.Type{ast.ArrayType{Elem: ast.NumberType{}}, ast.NumberType{}},
			Result: ast.ArrayType{Elem: ast.NumberType{}},
		}
		info.IntrinsicCalls[call] = contract
	}
	if _, err := BuildProgram(prog, info); err == nil || !strings.Contains(err.Error(), "intrinsic argument type") {
		t.Fatalf("stale intrinsic signature accepted: %v", err)
	}
}

func TestUnknownIntrinsicIsNotAnOrdinaryCall(t *testing.T) {
	prog, info := checkedProgram(t, `function pilot(items: string[], item: string): string[] { return items.append(item); }`)
	for call, contract := range info.IntrinsicCalls {
		contract.Kind = checker.IntrinsicNone
		info.IntrinsicCalls[call] = contract
	}
	if _, err := BuildProgram(prog, info); err == nil {
		t.Fatal("unknown intrinsic acquired an ordinary-call contract")
	}
}

func TestNestedAppendReturnFlow(t *testing.T) {
	prog, info := checkedProgram(t, `
function pilot(items: string[][], item: string[]): string[] { return grow(items, item)[0]; }
function grow(items: string[][], item: string[]): string[][] { return items.append(item); }
`)
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	flow, err := solveReturnFlow(p)
	if err != nil {
		t.Fatal(err)
	}
	result := flow.funcs[p.funcs[0]].result
	requireSources(t, result, "p0/element", "p1")
	requireSources(t, result.children[0], "p0/element/element", "p1/element")
}
