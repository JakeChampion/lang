package semir

import (
	"slices"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

func conditionalArrayDiamond(mode ParamMode) *availabilityFixture {
	typ := ast.ArrayType{Elem: ast.NumberType{Width: 64, Signed: true}}
	a := availabilityDiamond(typ)
	a.f.modes[1] = mode
	a.bad.Ops[0].Kind, a.bad.Ops[0].Imm = ssa.OpArrayMake, 0
	return a
}

func conditionalSharedDiamond() *availabilityFixture {
	a := conditionalArrayDiamond(ParamCounted)
	copy := a.f.addPhi(a.join, a.f.result, ast.Position{}, a.none, a.some)
	info := a.f.values[copy.ID]
	info.typ.form = availabilityForm
	a.f.values[copy.ID] = info
	a.f.addOp(a.join, ssa.OpStateHas, ast.BoolType{}, ast.Position{}, copy)
	return a
}

func TestConditionalUnitsDuplicatePhiAcquiresBeforeMove(t *testing.T) {
	a := conditionalSharedDiamond()
	p := verifiedUnitPlan(t, a.f)
	for _, from := range []*ssa.Block{a.yes, a.no} {
		s := p.edges[flowEdge{from, a.join}]
		if len(s.supplies) != 2 || s.supplies[0].mode != unitRetain || s.supplies[1].mode != unitMove {
			t.Fatalf("duplicated conditional phi supply: %+v", s)
		}
	}
	if _, err := LowerARM64SSA(singleProgram(a.f)); err != nil {
		t.Fatal(err)
	}
	e := flowEdge{a.yes, a.join}
	s := p.edges[e]
	s.supplies[0].mode = unitMove
	p.edges[e] = s
	if err := verifyFunctionUnits(p, nil); err == nil || !strings.Contains(err.Error(), "transferred twice") {
		t.Fatalf("duplicate conditional move admitted: %v", err)
	}
}

func conditionalArrayLoop() *Func {
	f := availabilityLoop()
	typ := ast.ArrayType{Elem: ast.NumberType{Width: 64, Signed: true}}
	f.result, f.modes[0] = typ, ParamCounted
	for id, info := range f.values {
		if _, boolean := info.typ.source.(ast.BoolType); !boolean {
			info.typ.source = typ
			f.values[id] = info
		}
	}
	// Exercise machine fixups for forward references as well as backedge units.
	f.graph.Blocks[1], f.graph.Blocks[2] = f.graph.Blocks[2], f.graph.Blocks[1]
	return f
}

func conditionalNestedProjection() *availabilityFixture {
	a := conditionalArrayDiamond(ParamCounted)
	typ := ast.TupleType{Elems: []ast.Type{a.f.result}}
	box := a.f.addOp(a.yes, ssa.OpTupleMake, typ, ast.Position{}, a.payload)
	a.yes.Ops[0], a.yes.Ops[1] = a.yes.Ops[1], a.yes.Ops[0]
	a.yes.Ops[1].Args[0] = box
	for _, v := range []ssa.Value{a.some, a.none, a.state, a.get.Result} {
		info := a.f.values[v.ID]
		info.typ.source = typ
		a.f.values[v.ID] = info
	}
	child := a.f.addOp(a.good, ssa.OpTupleGet, a.f.result, ast.Position{}, a.get.Result)
	a.f.graph.SetRet(a.good, child)
	return a
}

func TestConditionalPayloadProvenanceAndContainment(t *testing.T) {
	a := conditionalNestedProjection()
	p := singleProgram(a.f)
	units := verifiedUnitPlan(t, a.f)
	deps := units.lifetime.dependencies[a.good.Term.Value.ID]
	if !slices.Contains(deps, a.state) || !slices.Contains(deps, a.get.Result) {
		t.Fatal("nested projection lost conditional containment anchors")
	}
	flow, err := solveReturnFlow(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(flow.funcs[a.f].values[a.none.ID].roots) != 0 || len(flow.funcs[a.f].values[a.none.ID].children[0].roots) != 0 {
		t.Fatal("absence invented payload provenance")
	}
	if !flow.funcs[a.f].result.roots[source{kind: sourceParameter, param: 1}] {
		t.Fatal("Present/Get lost returned parameter identity")
	}
	if _, err := LowerARM64SSA(p); err != nil {
		t.Fatal(err)
	}
}

func TestConditionalUnitsLoopCarriesPresenceObligation(t *testing.T) {
	f := conditionalArrayLoop()
	verifiedUnitPlan(t, f)
	if _, err := LowerARM64SSA(singleProgram(f)); err != nil {
		t.Fatal(err)
	}
}

func conditionalReplacementLoop() *Func {
	num := ast.NumberType{}
	f := newFunc("pilot", ast.ArrayType{Elem: num})
	limit := f.addParam(num, ParamValue, ast.Position{})
	g := f.graph
	entry, header, body := g.NewBlock(), g.NewBlock(), g.NewBlock()
	present, absent, latch := g.NewBlock(), g.NewBlock(), g.NewBlock()
	exit, good, bad := g.NewBlock(), g.NewBlock(), g.NewBlock()
	zero := f.addOp(entry, ssa.OpConstInt, num, ast.Position{})
	one := f.addOp(entry, ssa.OpConstInt, num, ast.Position{})
	entry.Ops[len(entry.Ops)-1].Imm = 1
	none := f.addState(entry, ssa.OpStateAbsent, f.result, ast.Position{})
	g.SetBr(entry, header)
	g.SetBr(latch, header)
	i := f.addPhi(header, num, ast.Position{}, zero, zero)
	state := f.addState(header, ssa.OpPhi, f.result, ast.Position{}, none, none)
	more := f.addOp(header, ssa.OpLt, ast.BoolType{}, ast.Position{}, i, limit)
	g.SetBrIf(header, more, body, exit)
	clear := f.addOp(body, ssa.OpEq, ast.BoolType{}, ast.Position{}, i, one)
	g.SetBrIf(body, clear, absent, present)
	array := f.addOp(present, ssa.OpArrayMake, f.result, ast.Position{}, i)
	some := f.addState(present, ssa.OpStatePresent, f.result, ast.Position{}, array)
	g.SetBr(present, latch)
	empty := f.addState(absent, ssa.OpStateAbsent, f.result, ast.Position{})
	g.SetBr(absent, latch)
	nextState := f.addState(latch, ssa.OpPhi, f.result, ast.Position{}, some, empty)
	next := f.addOp(latch, ssa.OpAdd, num, ast.Position{}, i, one)
	header.Ops[0].Args[1], header.Ops[1].Args[1] = next, nextState
	has := f.addOp(exit, ssa.OpStateHas, ast.BoolType{}, ast.Position{}, state)
	g.SetBrIf(exit, has, good, bad)
	value := f.addOp(good, ssa.OpStateGet, f.result, ast.Position{}, state)
	g.SetRet(good, value)
	fallback := f.addOp(bad, ssa.OpArrayMake, f.result, ast.Position{})
	g.SetRet(bad, fallback)
	return f
}

func TestConditionalUnitsRepeatedReplacement(t *testing.T) {
	f := conditionalReplacementLoop()
	verifiedUnitPlan(t, f)
	if _, err := LowerARM64SSA(singleProgram(f)); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkConditionalReplacement(b *testing.B) {
	p := singleProgram(conditionalReplacementLoop())
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := LowerARM64SSA(p); err != nil {
			b.Fatal(err)
		}
	}
}

func TestConditionalUnitsDistinguishAbsenceAndPayload(t *testing.T) {
	for _, mode := range []ParamMode{ParamBorrow, ParamCounted} {
		a := conditionalArrayDiamond(mode)
		p := verifiedUnitPlan(t, a.f)
		for _, v := range []ssa.Value{a.none, a.some, a.state} {
			if p.lifetime.owned[v.ID] || !p.lifetime.conditional[v.ID] {
				t.Fatal("availability became an unconditional owner")
			}
		}
		if len(p.ops[a.no.Ops[0]].supplies) != 0 {
			t.Fatal("absence acquired a nonexistent payload")
		}
		got := p.ops[a.yes.Ops[0]].supplies
		want := unitRetain
		if mode == ParamCounted {
			want = unitMove
		}
		if len(got) != 1 || got[0].value != a.payload || got[0].mode != want || got[0].conditional {
			t.Fatalf("Present did not acquire its ordinary payload: %+v", got)
		}
		for _, from := range []*ssa.Block{a.no, a.yes} {
			got := p.edges[flowEdge{from, a.join}].supplies
			if len(got) != 1 || !got[0].conditional || got[0].mode != unitMove {
				t.Fatalf("join did not transfer its conditional obligation: %+v", got)
			}
		}
		if !slices.Contains(p.lifetime.dependencies[a.get.Result.ID], a.state) {
			t.Fatal("payload borrow lost its state anchor")
		}
		if _, err := LowerARM64SSA(singleProgram(a.f)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConditionalUnitVerifierRejectsCorruptPlans(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*functionUnits, *availabilityFixture)
		want string
	}{
		{"unguarded-supply", func(p *functionUnits, a *availabilityFixture) {
			e := flowEdge{a.yes, a.join}
			s := p.edges[e]
			s.supplies[0].conditional = false
			p.edges[e] = s
		}, "wrong presence condition"},
		{"wrong-state-supply", func(p *functionUnits, a *availabilityFixture) {
			e := flowEdge{a.yes, a.join}
			s := p.edges[e]
			s.supplies[0].value = a.none
			p.edges[e] = s
		}, "wrong value or slot"},
		{"invented-absent-payload", func(p *functionUnits, a *availabilityFixture) {
			op := a.no.Ops[0]
			s := p.ops[op]
			s.supplies = []unitSupply{{value: a.payload, mode: unitRetain}}
			p.ops[op] = s
		}, "supplies disagree"},
		{"missing-present-payload", func(p *functionUnits, a *availabilityFixture) {
			op := a.yes.Ops[0]
			s := p.ops[op]
			s.supplies = nil
			p.ops[op] = s
		}, "supplies disagree"},
		{"early-anchor-drop", func(p *functionUnits, a *availabilityFixture) {
			s := p.ops[a.get]
			s.drops = append(s.drops, a.state)
			p.ops[a.get] = s
		}, "outlives container anchor"},
		{"missing-conditional-drop", func(p *functionUnits, a *availabilityFixture) {
			s := p.returns[a.good]
			s.drops = nil
			p.returns[a.good] = s
		}, "unreleased"},
		{"false-immortal", func(p *functionUnits, a *availabilityFixture) {
			e := flowEdge{a.no, a.join}
			s := p.edges[e]
			s.supplies[0].mode = unitImmortal
			p.edges[e] = s
		}, "not certified immortal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := conditionalArrayDiamond(ParamBorrow)
			p := verifiedUnitPlan(t, a.f)
			tc.edit(p, a)
			// Cached planner facts are deliberately discarded by verification.
			p.lifetime = nil
			if err := verifyFunctionUnits(p, nil); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wanted %q, got %v", tc.want, err)
			}
		})
	}
}
