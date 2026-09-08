package semir

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

func pathLabel(path *sourcePath) string {
	if path == nil {
		return ""
	}
	if path.field == -1 {
		return pathLabel(path.parent) + "/element"
	}
	return pathLabel(path.parent) + fmt.Sprintf("/field%d", path.field)
}

func sourceLabels(flow *valueFlow) []string {
	var labels []string
	for atom := range flow.roots {
		switch atom.kind {
		case sourceParameter:
			labels = append(labels, fmt.Sprintf("p%d%s", atom.param, pathLabel(atom.path)))
		case sourceGenerated:
			labels = append(labels, "generated")
		case sourceImmortal:
			labels = append(labels, "immortal")
		case sourceScalar:
			labels = append(labels, "scalar")
		}
	}
	slices.Sort(labels)
	return labels
}

func requireSources(t *testing.T, flow *valueFlow, want ...string) {
	t.Helper()
	slices.Sort(want)
	if got := sourceLabels(flow); !slices.Equal(got, want) {
		t.Fatalf("sources = %v, want %v", got, want)
	}
}

func TestCheckedReturnProvenance(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		want         []string
	}{
		{"identity", `function pilot(items: string[]): string[] { return items; }`, []string{"p0"}},
		{"owned-identity-is-still-provenance", `function pilot(own items: string[]): string[] { return items; }`, []string{"p0"}},
		{"element", `function pilot(items: string[]): string { return items[0]; }`, []string{"p0/element"}},
		{"tuple-element", `function pilot(pair: (string[], string)): string { let (items, _) = pair; return items[0]; }`, []string{"p0/field0/element"}},
		{"nested-projection", `function pilot(pair: (string[][], string[])): string { let (items, _) = pair; return items[0][0]; }`, []string{"p0/field0/element/element"}},
		{"generated-root", `function pilot(item: string): string[] { return [item]; }`, []string{"generated"}},
		{"unwrap-local-container", `function pilot(item: string): string { var a = [item]; return a[0]; }`, []string{"p0"}},
		{"forward-accessor", `function pilot(items: string[]): string { return get(items); } function get(items: string[]): string { return items[0]; }`, []string{"p0/element"}},
		{"forward-constructor", `function pilot(item: string): string { var a = wrap(item); return a[0]; } function wrap(item: string): string[] { return [item]; }`, []string{"p0"}},
		{"exact-tuple-field-through-call", `function pilot(left: string, right: string): string { return get((left, right)); } function get(pair: (string, string)): string { let (_, right) = pair; return right; }`, []string{"p1"}},
		{"caller-projection-composition", `function pilot(items: string[][]): string { return get(items[0]); } function get(items: string[]): string { return items[0]; }`, []string{"p0/element/element"}},
		{"mixed-returns", `function pilot(items: string[], choose: boolean): string[] { if (choose) { return items; } return []; }`, []string{"p0", "generated"}},
		{"different-branches", `function pilot(a: string[], b: string[], choose: boolean): string { if (choose) { return a[0]; } return b[0]; }`, []string{"p0/element", "p1/element"}},
		{"immortal", `function pilot(): string { return "literal"; }`, []string{"immortal"}},
		{"recursive-base", `function pilot(items: string[], stop: boolean): string { if (stop) { return items[0]; } return next(items, stop); } function next(items: string[], stop: boolean): string { return pilot(items, stop); }`, []string{"p0/element"}},
		{"recursive-argument-permutation", `function pilot(a: string[], b: string[], stop: boolean): string { if (stop) { return a[0]; } return pilot(b, a, stop); }`, []string{"p0/element", "p1/element"}},
		{"recursive-generated-alternative", `function pilot(items: string[], stop: boolean): string[] { if (stop) { return items; } return pilot([], stop); }`, []string{"p0", "generated"}},
		{"ungrounded-recursion", `function pilot(items: string[]): string { return next(items); } function next(items: string[]): string { return pilot(items); }`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, info := checkedProgram(t, tc.source)
			p, err := BuildProgram(prog, info)
			if err != nil {
				t.Fatal(err)
			}
			flow, err := solveReturnFlow(p)
			if err != nil {
				t.Fatal(err)
			}
			requireSources(t, flow.funcs[p.funcs[0]].result, tc.want...)
		})
	}
}

func TestReturnedContainerKeepsChildProvenance(t *testing.T) {
	prog, info := checkedProgram(t, `
function pilot(left: string, right: string): (string[], string) { return wrap(left, right); }
function wrap(left: string, right: string): (string[], string) { return ([left], right); }
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
	requireSources(t, result, "generated")
	requireSources(t, result.children[0], "generated")
	requireSources(t, result.children[0].children[0], "p0")
	requireSources(t, result.children[1], "p1")
}

func TestReturnFlowHasNoForwardingDepthLimit(t *testing.T) {
	const depth = 96
	var source strings.Builder
	source.WriteString("function pilot(items: string[]): string { return hop0(items); }\n")
	for i := 0; i < depth; i++ {
		fmt.Fprintf(&source, "function hop%d(items: string[]): string { return ", i)
		if i+1 == depth {
			source.WriteString("items[0]")
		} else {
			fmt.Fprintf(&source, "hop%d(items)", i+1)
		}
		source.WriteString("; }\n")
	}
	prog, info := checkedProgram(t, source.String())
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	flow, err := solveReturnFlow(p)
	if err != nil {
		t.Fatal(err)
	}
	requireSources(t, flow.funcs[p.funcs[0]].result, "p0/element")
}

func singleProgram(f *Func) *Program {
	p := &Program{funcs: []*Func{f}, byName: map[string]int64{f.graph.Name: 1}}
	f.program = p
	f.contract = funcContract{result: f.result, modes: append([]ParamMode(nil), f.modes...)}
	for _, param := range f.graph.Params {
		f.contract.params = append(f.contract.params, f.values[param.ID].typ)
	}
	return p
}

func TestReturnFlowLoopAndElementStores(t *testing.T) {
	typ := ast.ArrayType{Elem: ast.StringType{}}
	f := newFunc("loop", typ)
	array := f.addParam(typ, ParamCounted, ast.Position{})
	condition := f.addParam(ast.BoolType{}, ParamValue, ast.Position{})
	entry, header, body, exit := f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock()
	f.graph.SetBr(entry, header)
	f.graph.SetBrIf(header, condition, body, exit)
	f.graph.SetBr(body, header)
	value := f.addPhi(header, typ, ast.Position{}, array, array)
	literal := f.addOp(body, ssa.OpConstString, ast.StringType{}, ast.Position{})
	updated := f.addOp(body, ssa.OpArrayAppend, typ, ast.Position{}, value, literal)
	header.Ops[0].Args[1] = updated
	f.graph.SetRet(exit, value)
	flow, err := solveReturnFlow(singleProgram(f))
	if err != nil {
		t.Fatal(err)
	}
	result := flow.funcs[f].result
	requireSources(t, result, "p0", "generated")
	requireSources(t, result.children[0], "p0/element", "immortal")
}

func TestReturnFlowSkipsUnreachablePhiPredecessor(t *testing.T) {
	typ := ast.ArrayType{Elem: ast.StringType{}}
	f := newFunc("reachable", typ)
	array := f.addParam(typ, ParamBorrow, ast.Position{})
	entry, dead, join := f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock()
	f.graph.SetBr(entry, join)
	generated := f.addOp(dead, ssa.OpArrayMake, typ, ast.Position{})
	f.graph.SetBr(dead, join)
	value := f.addPhi(join, typ, ast.Position{}, array, generated)
	f.graph.SetRet(join, value)
	flow, err := solveReturnFlow(singleProgram(f))
	if err != nil {
		t.Fatal(err)
	}
	requireSources(t, flow.funcs[f].result, "p0")
}
