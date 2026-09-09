package semir

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ssa"
)

func enumProofFixture(t *testing.T) *Program {
	t.Helper()
	prog, info := checkedProgram(t, `enum Choice { Empty, First(i32[]), Second(i32[]) }
function pilot(x: Choice, other: Choice, flag: boolean): i32[] {
  return match (x) { Empty => [0i32], First(a) => a, Second(b) => b };
}`)
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func enumProjection(f *Func, field int64) (*ssa.Block, *ssa.Op) {
	for _, block := range f.graph.Blocks {
		for _, op := range block.Ops {
			if op.Kind == ssa.OpSumGet && op.Imm == field {
				return block, op
			}
		}
	}
	return nil, nil
}

func TestEnumProjectionGuardCorruption(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Func, *ssa.Block, *ssa.Op)
	}{
		{"other-container", func(f *Func, block *ssa.Block, get *ssa.Op) { get.Args[0] = f.graph.Params[1] }},
		{"other-variant-same-type", func(f *Func, block *ssa.Block, get *ssa.Op) { get.Imm = 0 }},
		{"before-tag-tests", func(f *Func, block *ssa.Block, get *ssa.Op) {
			for i, op := range block.Ops {
				if op == get {
					block.Ops = append(block.Ops[:i], block.Ops[i+1:]...)
					break
				}
			}
			f.graph.Entry.Ops = append(f.graph.Entry.Ops, get)
		}},
		{"wrong-excluded-variant", func(f *Func, block *ssa.Block, get *ssa.Op) {
			for _, b := range f.graph.Blocks {
				for _, op := range b.Ops {
					if op.Kind == ssa.OpSumIs && op.Imm == 1 {
						op.Imm = 2
					}
				}
			}
		}},
		{"inverted-edge", func(f *Func, block *ssa.Block, get *ssa.Op) {
			for _, b := range f.graph.Blocks {
				if b.Term.Kind == ssa.TermBrIf {
					b.Term.True, b.Term.False = b.Term.False, b.Term.True
					return
				}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := enumProofFixture(t)
			f := p.funcs[0]
			block, get := enumProjection(f, 1)
			if get == nil {
				t.Fatal("missing final-variant projection")
			}
			tc.edit(f, block, get)
			if err := ssa.Verify(f.graph); err != nil {
				t.Fatalf("corruption must preserve ordinary SSA: %v", err)
			}
			if err := VerifyProgram(p); err == nil || !strings.Contains(err.Error(), "active-variant proof") {
				t.Fatalf("got %v, want independent active-variant rejection", err)
			}
		})
	}
}

func TestEnumProjectionRequiresExclusiveGuardEdge(t *testing.T) {
	for _, mode := range []string{"exclusive", "equal-targets", "bypass"} {
		t.Run(mode, func(t *testing.T) {
			p := enumProofFixture(t)
			f := newFunc("guard", ast.ArrayType{Elem: ast.NumberType{}})
			f.program = p
			x := f.addParam(ast.EnumType{Name: "Choice"}, ParamBorrow, ast.Position{})
			entry, body := f.graph.NewBlock(), f.graph.NewBlock()
			is := f.addOp(entry, ssa.OpSumIs, ast.BoolType{}, ast.Position{}, x)
			entry.Ops[len(entry.Ops)-1].Imm = 1
			get := f.addOp(body, ssa.OpSumGet, f.result, ast.Position{}, x)
			body.Ops[len(body.Ops)-1].Imm = 0
			f.graph.SetRet(body, get)
			if mode == "equal-targets" {
				f.graph.SetBrIf(entry, is, body, body)
			} else {
				other := f.graph.NewBlock()
				f.graph.SetBrIf(entry, is, body, other)
				if mode == "bypass" {
					f.graph.SetBr(other, body)
				} else {
					empty := f.addOp(other, ssa.OpArrayMake, f.result, ast.Position{})
					f.graph.SetRet(other, empty)
				}
			}
			if err := ssa.Verify(f.graph); err != nil {
				t.Fatal(err)
			}
			err := verifyEnumGuards(f, ssa.BuildDomTree(f.graph))
			if (err == nil) != (mode == "exclusive") {
				t.Fatalf("guard proof = %v, exclusive=%t", err, mode == "exclusive")
			}
		})
	}
}

func TestEnumInterfaceCorruption(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*enumContract)
	}{
		{"owner", func(e *enumContract) { e.owner = &Program{} }},
		{"variant-name", func(e *enumContract) { e.variants[2].name = e.variants[1].name }},
		{"field-range", func(e *enumContract) { e.variants[1].first = 1 }},
		{"negative-count", func(e *enumContract) { e.variants[0].count = -1 }},
		{"field-variant", func(e *enumContract) { e.fields[0].variant = 2 }},
		{"field-ordinal", func(e *enumContract) { e.fields[0].index = 1 }},
		{"field-type", func(e *enumContract) { e.fields[0].typ = ast.VoidType{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := enumProofFixture(t)
			tc.edit(p.enum(ast.EnumType{Name: "Choice"}))
			if err := VerifyProgram(p); err == nil {
				t.Fatal("corrupt sum interface accepted")
			}
		})
	}
}

func TestEnumContractDetachedFromChecker(t *testing.T) {
	prog, info := checkedProgram(t, `function pilot(own x: Option[(i32[], i32)]): Option[(i32[], i32)] { return x; }`)
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	args := prog.Funcs[0].ReturnType.(ast.EnumType).Args
	args[0].(ast.TupleType).Elems[0] = ast.StringType{}
	for i := range info.Enums["Option"].Variants {
		v := &info.Enums["Option"].Variants[i]
		if len(v.Payloads) != 0 {
			v.Payloads[0] = ast.BoolType{}
		}
	}
	if err := VerifyProgram(p); err != nil {
		t.Fatalf("checker mutation changed semantic type: %v", err)
	}
	if _, err := LowerARM64SSA(p); err != nil {
		t.Fatal(err)
	}
}

func TestEnumExpressionTypesDetachedAtSourceBoundary(t *testing.T) {
	prog, info := checkedProgram(t, `function pilot(): Option[i32[]] {
  var values: Option[i32[]][] = [Some([1i32])];
  values = values.append(None);
  return values[0];
}`)
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	var corrupt func(ast.Type)
	corrupt = func(typ ast.Type) {
		switch x := typ.(type) {
		case ast.ArrayType:
			corrupt(x.Elem)
		case ast.EnumType:
			for i := range x.Args {
				x.Args[i] = ast.VoidType{}
			}
		}
	}
	ast.WalkProgram(prog, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.ArrayLit:
			corrupt(n.ElemType)
		case *ast.Index:
			corrupt(n.ElemType)
		}
		return true
	})
	for _, call := range info.IntrinsicCalls {
		corrupt(call.Signature.Result)
	}
	if err := VerifyProgram(p); err != nil {
		t.Fatalf("frontend expression mutation escaped its import boundary: %v", err)
	}
	if _, err := LowerARM64SSA(p); err != nil {
		t.Fatal(err)
	}
}

func TestEnumImportTransaction(t *testing.T) {
	p := &Program{}
	info := &checker.Info{
		Structs: map[string]*ast.StructDecl{
			"Good": {Name: "Good"},
			"Root": {Name: "Root", Fields: []ast.Param{{Name: "next", Type: ast.EnumType{Name: "Link"}}}},
		},
		Enums: map[string]*ast.EnumDecl{
			"Link": {Name: "Link", Variants: []ast.EnumVariant{{Name: "Next", Payloads: []ast.Type{ast.StructType{Name: "Root"}, ast.StructType{Name: "Missing"}}}}},
		},
	}
	if err := p.importNominalTypes(ast.StructType{Name: "Good"}, info); err != nil {
		t.Fatal(err)
	}
	good := p.records["Good"]
	if err := p.importNominalTypes(ast.StructType{Name: "Root"}, info); err == nil {
		t.Fatal("missing recursive peer accepted")
	}
	if len(p.records) != 1 || len(p.enums) != 0 || p.records["Good"] != good {
		t.Fatal("failed transaction published nominal interfaces")
	}
	info.Structs["Missing"] = &ast.StructDecl{Name: "Missing"}
	if err := p.importNominalTypes(ast.StructType{Name: "Root"}, info); err != nil {
		t.Fatal(err)
	}
	v := typeVerifier{program: p}
	if err := v.check(ast.StructType{Name: "Root"}, false); err != nil {
		t.Fatal(err)
	}
}

func TestEnumReturnFlowDistinguishesVariants(t *testing.T) {
	p := enumProofFixture(t)
	flow, err := solveReturnFlow(p)
	if err != nil {
		t.Fatal(err)
	}
	requireSources(t, flow.funcs[p.funcs[0]].result, "generated", "p0/field0", "p0/field1")
}

func TestEnumOperationCorruption(t *testing.T) {
	for _, kind := range []ssa.OpKind{ssa.OpSumMake, ssa.OpSumIs, ssa.OpSumGet} {
		for _, tc := range []struct {
			name string
			edit func(*ssa.Op)
		}{
			{"missing-operand", func(op *ssa.Op) { op.Args = nil }},
			{"extra-operand", func(op *ssa.Op) { op.Args = append(op.Args, op.Args[0]) }},
			{"negative-identity", func(op *ssa.Op) { op.Imm = -1 }},
			{"unknown-identity", func(op *ssa.Op) { op.Imm = 100 }},
			{"foreign-string-data", func(op *ssa.Op) { op.Str = "wrong" }},
			{"foreign-float-data", func(op *ssa.Op) { op.F64 = 1 }},
		} {
			t.Run(kind.String()+"/"+tc.name, func(t *testing.T) {
				prog, info := checkedProgram(t, `function pilot(): i32[] {
  var x: Option[i32[]] = Some([1i32]);
  return match (x) { None => [0i32], Some(a) => a };
}`)
				p, err := BuildProgram(prog, info)
				if err != nil {
					t.Fatal(err)
				}
				var target *ssa.Op
				for _, block := range p.funcs[0].graph.Blocks {
					for _, op := range block.Ops {
						if op.Kind == kind {
							target = op
						}
					}
				}
				if target == nil {
					t.Fatal("missing sum operation")
				}
				tc.edit(target)
				if err := VerifyProgram(p); err == nil {
					t.Fatal("malformed sum operation accepted")
				}
			})
		}
	}
}

func TestEnumConstructionContractCorruption(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*checker.Info, ast.Expr, checker.EnumConstruction)
	}{
		{"missing", func(info *checker.Info, expr ast.Expr, c checker.EnumConstruction) {
			delete(info.EnumConstructions, expr)
		}},
		{"variant", func(info *checker.Info, expr ast.Expr, c checker.EnumConstruction) {
			c.VariantIndex = 100
			info.EnumConstructions[expr] = c
		}},
		{"payload-arity", func(info *checker.Info, expr ast.Expr, c checker.EnumConstruction) {
			c.Payloads = nil
			info.EnumConstructions[expr] = c
		}},
		{"payload-type", func(info *checker.Info, expr ast.Expr, c checker.EnumConstruction) {
			c.Payloads = []ast.Type{ast.StringType{}}
			info.EnumConstructions[expr] = c
		}},
		{"incomplete-type", func(info *checker.Info, expr ast.Expr, c checker.EnumConstruction) {
			c.Type.Args = nil
			info.EnumConstructions[expr] = c
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, info := checkedProgram(t, `function pilot(): Option[i32[]] { return Some([1i32]); }`)
			if len(info.EnumConstructions) != 1 {
				t.Fatal("expected one construction witness")
			}
			for expr, c := range info.EnumConstructions {
				tc.edit(info, expr, c)
			}
			if _, err := BuildProgram(prog, info); err == nil {
				t.Fatal("invalid constructor witness accepted")
			}
		})
	}
}

func TestEnumRecursiveReturnFlowGate(t *testing.T) {
	prog, info := checkedProgram(t, `enum Chain { End, Next(i32[], Chain) }
function pilot(own x: Chain): Chain { return x; }`)
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LowerARM64SSA(p); err != nil {
		t.Fatalf("counted executable ABI rejected recursive enum: %v", err)
	}
	if flow, err := solveReturnFlow(p); err == nil || flow != nil {
		t.Fatalf("optional recursive return-flow gate must not fabricate a summary: %v, %v", flow, err)
	}
}

func TestEnumNullaryUsesImmortalStorage(t *testing.T) {
	prog, info := checkedProgram(t, `function pilot(): Option[i32[]] { return None; }`)
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	out, err := LowerARM64SSA(p)
	if err != nil {
		t.Fatal(err)
	}
	sentinels := 0
	for _, f := range out.Functions {
		for _, block := range f.Blocks {
			for _, op := range block.Ops {
				if op.Kind == ssa.OpAlloc {
					t.Fatal("payloadless enum allocated a heap box")
				}
				if op.Kind == ssa.OpEnumSentinel {
					sentinels++
				}
			}
		}
	}
	if sentinels != 1 {
		t.Fatalf("got %d sentinels, want one", sentinels)
	}
}
