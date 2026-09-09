package semir

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

func recordFixture(t *testing.T) *Program {
	t.Helper()
	prog, info := checkedProgram(t, `struct Pair { left: i32[], right: i32[] }
function pilot(own seed: Pair): i32[] { var next = Pair { ...seed, right: [7i32] }; return next.left; }`)
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRecordInterfaceCorruption(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		edit       func(*Program, *recordContract)
	}{
		{"missing", "nominal record", func(p *Program, r *recordContract) { delete(p.records, "Pair") }},
		{"foreign-owner", "nominal record", func(p *Program, r *recordContract) { r.owner = &Program{} }},
		{"wrong-name", "nominal record", func(p *Program, r *recordContract) { r.name = "Other" }},
		{"duplicate-field", "duplicate field", func(p *Program, r *recordContract) { r.fields[1].name = r.fields[0].name }},
		{"empty-field", "empty or duplicate", func(p *Program, r *recordContract) { r.fields[0].name = "" }},
		{"void-field", "unsupported semantic type", func(p *Program, r *recordContract) { r.fields[0].typ = ast.VoidType{} }},
		{"recursive-field-mismatch", "record_", func(p *Program, r *recordContract) {
			r.fields[0].typ = ast.ArrayType{Elem: ast.StructType{Name: "Pair"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := recordFixture(t)
			tc.edit(p, p.records["Pair"])
			if err := ssa.Verify(p.funcs[0].graph); err != nil {
				t.Fatal(err)
			}
			if err := VerifyProgram(p); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
		})
	}
}

func TestRecordOperationCorruption(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind ssa.OpKind
		edit func(*Func, *ssa.Op)
	}{
		{"make-arity", ssa.OpRecordMake, func(f *Func, op *ssa.Op) { op.Args = op.Args[:1] }},
		{"make-field-type", ssa.OpRecordMake, func(f *Func, op *ssa.Op) { op.Args[0] = f.graph.Params[0] }},
		{"make-not-record", ssa.OpRecordMake, func(f *Func, op *ssa.Op) {
			v := f.values[op.Result.ID]
			v.typ.source = ast.TupleType{Elems: []ast.Type{ast.ArrayType{Elem: ast.NumberType{}}, ast.ArrayType{Elem: ast.NumberType{}}}}
			f.values[op.Result.ID] = v
		}},
		{"get-negative-field", ssa.OpRecordGet, func(f *Func, op *ssa.Op) { op.Imm = -1 }},
		{"get-past-field", ssa.OpRecordGet, func(f *Func, op *ssa.Op) { op.Imm = 2 }},
		{"get-arity", ssa.OpRecordGet, func(f *Func, op *ssa.Op) { op.Args = append(op.Args, op.Args[0]) }},
		{"get-result-type", ssa.OpRecordGet, func(f *Func, op *ssa.Op) {
			v := f.values[op.Result.ID]
			v.typ.source = ast.BoolType{}
			f.values[op.Result.ID] = v
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := recordFixture(t)
			f := p.funcs[0]
			var found *ssa.Op
			for _, block := range f.graph.Blocks {
				for _, op := range block.Ops {
					if op.Kind == tc.kind && found == nil {
						found = op
					}
				}
			}
			if found == nil {
				t.Fatal("fixture lacks required operation")
			}
			tc.edit(f, found)
			if err := ssa.Verify(f.graph); err != nil {
				t.Fatal(err)
			}
			if err := VerifyProgram(p); err == nil {
				t.Fatal("invalid record operation accepted")
			}
		})
	}
}

func TestRecordNominalCallContract(t *testing.T) {
	prog, info := checkedProgram(t, `struct Left { value: i32[] } struct Right { value: i32[] }
function pilot(own left: Left): i32[] { inspect(Right { value: [7] }); return left.value; }
function inspect(right: Right): void {}`)
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	call := firstCall(t, p.funcs[0])
	call.Args[0] = p.funcs[0].graph.Params[0]
	if err := ssa.Verify(p.funcs[0].graph); err != nil {
		t.Fatal(err)
	}
	if err := VerifyProgram(p); err == nil {
		t.Fatal("same-layout foreign nominal argument accepted")
	}
}

func TestRecordInterfaceDetachedFromChecker(t *testing.T) {
	prog, info := checkedProgram(t, `struct Box { pair: (i32[], i32[]) }
function pilot(box: Box): i32[] { return box.pair.0; }`)
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	info.Structs["Box"].Fields[0].Type.(ast.TupleType).Elems[0] = ast.BoolType{}
	info.Structs["Box"].Fields[0].Name = "changed"
	delete(info.Structs, "Box")
	if err := VerifyProgram(p); err != nil {
		t.Fatal(err)
	}
	if _, err := LowerARM64SSA(p); err != nil {
		t.Fatal(err)
	}
}

func TestRecordRecursiveTypesSeparateAnalysisCapability(t *testing.T) {
	for _, source := range []string{
		`struct Node { children: Node[] } function pilot(node: Node): void {}`,
		`struct A { children: B[] } struct B { parent: A[] } function pilot(node: A): void {}`,
	} {
		prog, info := checkedProgram(t, source)
		p, err := BuildProgram(prog, info)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := LowerARM64SSA(p); err != nil {
			t.Fatal(err)
		}
		if flow, err := solveReturnFlow(p); err == nil || flow != nil || !strings.Contains(err.Error(), "recursive record return-flow analysis") {
			t.Fatalf("got %v, want explicit unsupported analysis without a summary", err)
		}
	}
}

func TestRecordImportRejectsUnresolvedInterfaces(t *testing.T) {
	_, info := checkedProgram(t, `struct Box { value: i32[] } function pilot(box: Box): void {}`)
	for _, tc := range []struct {
		name string
		typ  ast.Type
		want string
	}{
		{"missing", ast.StructType{Name: "Missing"}, "missing concrete checked field interface"},
		{"unmonomorphized", ast.StructType{Name: "Box", Args: []ast.Type{ast.NumberType{}}}, "monomorphization must precede"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &Program{}
			if err := p.importRecordTypes(tc.typ, info); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
		})
	}
}

func TestRecordDeepAcyclicProvenance(t *testing.T) {
	var source strings.Builder
	source.WriteString("struct Level0 { data: i32[] }")
	const depth = 32
	for i := 1; i <= depth; i++ {
		fmt.Fprintf(&source, "struct Level%d { child: Level%d }", i, i-1)
	}
	fmt.Fprintf(&source, "function pilot(node: Level%d): i32[] { return node", depth)
	for range depth {
		source.WriteString(".child")
	}
	source.WriteString(".data; }")
	prog, info := checkedProgram(t, source.String())
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	flow, err := solveReturnFlow(p)
	if err != nil {
		t.Fatal(err)
	}
	requireSources(t, flow.funcs[p.funcs[0]].result, "p0"+strings.Repeat("/field0", depth+1))
	if _, err := LowerARM64SSA(p); err != nil {
		t.Fatal(err)
	}
}

func TestRecordReturnProvenanceKeepsFieldsDistinct(t *testing.T) {
	prog, info := checkedProgram(t, `struct Pair { left: string[], right: string[] }
function pilot(a: string[], b: string[]): string[] { return read(Pair { left: a, right: b }); }
function read(pair: Pair): string[] { return pair.right; }`)
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	flow, err := solveReturnFlow(p)
	if err != nil {
		t.Fatal(err)
	}
	requireSources(t, flow.funcs[p.funcs[0]].result, "p1")
	requireSources(t, flow.funcs[p.funcs[0]].result.children[0], "p1/element")
	requireSources(t, flow.funcs[p.funcs[1]].result, "p0/field1")
}
