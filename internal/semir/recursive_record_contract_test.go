package semir

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

func TestRecursiveRecordImportIsTransactional(t *testing.T) {
	_, info := checkedProgram(t, `struct Good { value: i32[] }
struct A { children: B[], payload: (i32[], i32[]) }
struct B { children: A[] }
function pilot(): i32 { return 0i32; }`)
	p := &Program{}
	if err := p.importNominalTypes(ast.StructType{Name: "Good"}, info); err != nil {
		t.Fatal(err)
	}
	good := p.records["Good"]
	original := info.Structs["A"].Fields[1].Type
	info.Structs["A"].Fields[1].Type = ast.StructType{Name: "Missing"}
	if err := p.importNominalTypes(ast.StructType{Name: "A"}, info); err == nil || !strings.Contains(err.Error(), "missing concrete") {
		t.Fatalf("got %v, want missing nested interface", err)
	}
	if len(p.records) != 1 || p.records["Good"] != good {
		t.Fatal("failed recursive import changed the existing catalogue")
	}
	info.Structs["A"].Fields[1].Type = original
	if err := p.importNominalTypes(ast.StructType{Name: "A"}, info); err != nil {
		t.Fatal(err)
	}
	if len(p.records) != 3 || p.records["Good"] != good {
		t.Fatal("retry did not publish exactly the complete recursive component")
	}
	// Both the forward and backward nominal edges survive independently of
	// the checker, including mutable slices inside copied tuple interfaces.
	original.(ast.TupleType).Elems[0] = ast.VoidType{}
	delete(info.Structs, "A")
	delete(info.Structs, "B")
	verifier := typeVerifier{program: p}
	if err := verifier.check(ast.StructType{Name: "A"}, false); err != nil {
		t.Fatal(err)
	}
}

func recursiveRecordFixture(t *testing.T) *Program {
	t.Helper()
	prog, info := checkedProgram(t, `struct A { children: B[], value: i32[] }
struct B { children: A[], flag: boolean }
function pilot(own root: A): void {}`)
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRecursiveRecordVerificationChecksBeyondBackEdges(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Program)
	}{
		{"outer-later-field", func(p *Program) { p.records["A"].fields[1].typ = ast.VoidType{} }},
		{"inner-later-field", func(p *Program) { p.records["B"].fields[1].typ = ast.VoidType{} }},
		{"duplicate-after-back-edge", func(p *Program) { p.records["B"].fields[1].name = "children" }},
		{"foreign-recursive-owner", func(p *Program) { p.records["B"].owner = &Program{} }},
		{"missing-recursive-target", func(p *Program) { delete(p.records, "B") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := recursiveRecordFixture(t)
			tc.edit(p)
			if err := ssa.Verify(p.funcs[0].graph); err != nil {
				t.Fatal(err)
			}
			if err := VerifyProgram(p); err == nil {
				t.Fatal("recursive back edge hid an invalid typed field interface")
			}
		})
	}
}

func TestRecursiveRecordDropHelpersAreFinite(t *testing.T) {
	p := recursiveRecordFixture(t)
	out, err := LowerARM64SSA(p)
	if err != nil {
		t.Fatal(err)
	}
	// Exactly one drop helper per reference-bearing type, in addition to the
	// source functions (checkedProgram also appends a scalar main entry point):
	// A, B[], B, A[], i32[]. Recursive calls must reuse those identities.
	if helpers := len(out.Functions) - len(p.funcs); helpers != 5 {
		t.Fatalf("got %d helpers, want exactly five recursive drop helpers", helpers)
	}
}

func TestReturnFlowIgnoresUnusedRecursiveCatalogue(t *testing.T) {
	prog, info := checkedProgram(t, `struct Node { children: Node[] }
function pilot(items: i32[]): i32[] { return items; }`)
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.importNominalTypes(ast.StructType{Name: "Node"}, info); err != nil {
		t.Fatal(err)
	}
	flow, err := solveReturnFlow(p)
	if err != nil {
		t.Fatal(err)
	}
	requireSources(t, flow.funcs[p.funcs[0]].result, "p0")
}
