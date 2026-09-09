package semir

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

type availabilityFixture struct {
	f                          *Func
	entry, yes, no, join, good *ssa.Block
	bad                        *ssa.Block
	payload, some, none, state ssa.Value
	has, get                   *ssa.Op
}

func availabilityDiamond(typ ast.Type) *availabilityFixture {
	f := newFunc("pilot", typ)
	a := &availabilityFixture{f: f}
	flag := f.addParam(ast.BoolType{}, ParamValue, ast.Position{})
	a.payload = f.addParam(typ, ParamValue, ast.Position{})
	a.entry, a.yes, a.no = f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock()
	a.join, a.good, a.bad = f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock()
	f.graph.SetBrIf(a.entry, flag, a.yes, a.no)
	a.some = f.addState(a.yes, ssa.OpStatePresent, typ, ast.Position{}, a.payload)
	a.none = f.addState(a.no, ssa.OpStateAbsent, typ, ast.Position{})
	// Deliberately differ from block allocation order.
	f.graph.SetBr(a.no, a.join)
	f.graph.SetBr(a.yes, a.join)
	a.state = f.addState(a.join, ssa.OpPhi, typ, ast.Position{}, a.none, a.some)
	has := f.addOp(a.join, ssa.OpStateHas, ast.BoolType{}, ast.Position{}, a.state)
	a.has = a.join.Ops[len(a.join.Ops)-1]
	f.graph.SetBrIf(a.join, has, a.good, a.bad)
	get := f.addOp(a.good, ssa.OpStateGet, typ, ast.Position{Line: 7, Col: 3}, a.state)
	a.get = a.good.Ops[len(a.good.Ops)-1]
	f.graph.SetRet(a.good, get)
	kind, fallback := ssa.OpConstInt, int64(17)
	if _, boolean := typ.(ast.BoolType); boolean {
		kind, fallback = ssa.OpConstBool, 1
	}
	other := f.addOp(a.bad, kind, typ, ast.Position{})
	a.bad.Ops[len(a.bad.Ops)-1].Imm = fallback
	f.graph.SetRet(a.bad, other)
	return a
}

func TestAvailabilityScalarJoinsHaveNoCountedUnits(t *testing.T) {
	a := availabilityDiamond(ast.NumberType{})
	if err := Verify(a.f); err != nil {
		t.Fatal(err)
	}
	p := singleProgram(a.f)
	plans, err := planProgramUnits(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range plans[a.f].ops {
		if len(step.supplies) != 0 || len(step.drops) != 0 || len(step.copyElements) != 0 {
			t.Fatal("scalar availability invented an ownership obligation")
		}
	}
	out, err := LowerARM64SSA(p)
	if err != nil {
		t.Fatal(err)
	}
	phis := 0
	for _, f := range out.Functions {
		for _, block := range f.Blocks {
			for _, op := range block.Ops {
				if stateOp(op.Kind) || op.Kind == ssa.OpAlloc || op.Kind == ssa.OpArrayMake || op.Kind == ssa.OpTupleMake {
					t.Fatal("availability was not lowered without an environment allocation")
				}
				if op.Kind == ssa.OpPhi {
					phis++
				}
			}
		}
	}
	if phis != 2 {
		t.Fatalf("state join needs one presence and one payload phi, got %d", phis)
	}
}

func TestAvailabilityRejectsInvalidTypesAndGuards(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*availabilityFixture)
		want string
	}{
		{"absent-with-payload", func(a *availabilityFixture) { a.no.Ops[0].Args = []ssa.Value{a.payload} }, "availability operand"},
		{"present-without-payload", func(a *availabilityFixture) { a.yes.Ops[0].Args = nil }, "availability operand"},
		{"present-type-mismatch", func(a *availabilityFixture) {
			info := a.f.values[a.some.ID]
			info.typ.source = ast.BoolType{}
			a.f.values[a.some.ID] = info
		}, "availability operand"},
		{"ordinary-producer", func(a *availabilityFixture) { a.no.Ops[0].Kind = ssa.OpConstInt }, "ordinary operation"},
		{"ordinary-consumer", func(a *availabilityFixture) { a.has.Kind = ssa.OpNot }, "ordinary operation"},
		{"mixed-state-join", func(a *availabilityFixture) { a.join.Ops[0].Args[0] = a.payload }, "semantic type"},
		{"bad-type-form", func(a *availabilityFixture) {
			info := a.f.values[a.none.ID]
			info.typ.form = 99
			a.f.values[a.none.ID] = info
		}, "invalid semantic type form"},
		{"reference-state", func(a *availabilityFixture) {
			info := a.f.values[a.none.ID]
			info.typ.source = ast.ArrayType{Elem: ast.NumberType{}}
			a.f.values[a.none.ID] = info
		}, "conditional unit support"},
		{"source-parameter", func(a *availabilityFixture) {
			info := a.f.values[a.payload.ID]
			info.typ.form = availabilityForm
			a.f.values[a.payload.ID] = info
		}, "source parameter"},
		{"source-return", func(a *availabilityFixture) { a.good.Term.Value = a.state }, "return"},
		{"unguarded-get", func(a *availabilityFixture) {
			a.good.Ops = nil
			a.join.Ops = append(a.join.Ops, a.get)
		}, "presence proof"},
		{"wrong-state-guard", func(a *availabilityFixture) {
			other := a.f.addState(a.entry, ssa.OpStateAbsent, a.f.result, ast.Position{})
			a.has.Args[0] = other
		}, "presence proof"},
		{"false-edge", func(a *availabilityFixture) {
			a.join.Term.True, a.join.Term.False = a.join.Term.False, a.join.Term.True
		}, "presence proof"},
		{"alternate-predecessor", func(a *availabilityFixture) {
			a.f.graph.SetBr(a.bad, a.good)
		}, "presence proof"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := availabilityDiamond(ast.NumberType{})
			tc.edit(a)
			if err := ssa.Verify(a.f.graph); err != nil {
				t.Fatalf("fixture must retain valid SSA: %v", err)
			}
			if err := Verify(a.f); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wanted %q, got %v", tc.want, err)
			}
		})
	}
}

func TestAvailabilityPresentCanBeReadDirectly(t *testing.T) {
	f := newFunc("pilot", ast.NumberType{})
	value := f.addParam(f.result, ParamValue, ast.Position{})
	entry := f.graph.NewBlock()
	state := f.addState(entry, ssa.OpStatePresent, f.result, ast.Position{}, value)
	get := f.addOp(entry, ssa.OpStateGet, f.result, ast.Position{}, state)
	f.graph.SetRet(entry, get)
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
	if _, err := LowerARM64SSA(singleProgram(f)); err != nil {
		t.Fatal(err)
	}
}

func TestAvailabilityOperationsRemainPreRC(t *testing.T) {
	for _, tc := range []struct {
		kind ssa.OpKind
		name string
	}{
		{ssa.OpStateAbsent, "state_absent"},
		{ssa.OpStatePresent, "state_present"},
		{ssa.OpStateHas, "state_has"},
		{ssa.OpStateGet, "state_get"},
	} {
		if tc.kind.String() != tc.name || ssa.IsPure(tc.kind) || !stateOp(tc.kind) {
			t.Fatalf("%s lost its semantic-only operation contract", tc.name)
		}
	}
}

func TestAvailabilityRejectsEmptyPhi(t *testing.T) {
	for _, state := range []bool{false, true} {
		f := newFunc("pilot", ast.NumberType{})
		entry := f.graph.NewBlock()
		var value ssa.Value
		if state {
			phi := f.addState(entry, ssa.OpPhi, f.result, ast.Position{})
			f.result = ast.BoolType{}
			value = f.addOp(entry, ssa.OpStateHas, f.result, ast.Position{}, phi)
		} else {
			value = f.addPhi(entry, f.result, ast.Position{})
		}
		f.graph.SetRet(entry, value)
		if err := Verify(f); err == nil {
			t.Fatalf("empty phi admitted, availability=%v", state)
		}
	}
}

func TestAvailabilityLoweringPreservesEffectArguments(t *testing.T) {
	a := availabilityDiamond(ast.NumberType{})
	p := singleProgram(a.f)
	sink := newFunc("sink", ast.VoidType{})
	sink.program = p
	sink.addParam(ast.NumberType{}, ParamValue, ast.Position{})
	sink.contract = funcContract{params: []ast.Type{ast.NumberType{}}, modes: []ParamMode{ParamValue}, result: ast.VoidType{}}
	sink.graph.SetRet(sink.graph.NewBlock(), ssa.Value{})
	p.funcs = append(p.funcs, sink)
	p.byName["sink"] = 2
	call := a.f.addEffect(a.join, ssa.OpSemanticCall, ast.Position{}, a.payload)
	call.Imm = 2
	out, err := LowerARM64SSA(p)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, block := range out.Functions[out.Symbols["pilot"]].Blocks {
		for _, op := range block.Ops {
			if op.Kind == ssa.OpCall && op.Str == out.Symbols["sink"] {
				found = true
				if len(op.Args) != 1 {
					t.Fatal("multi-lane phi wiring erased an effect-only call argument")
				}
			}
		}
	}
	if !found {
		t.Fatal("effect-only call disappeared")
	}
}

func availabilityChain(count int) *Func {
	f := newFunc("pilot", ast.NumberType{})
	value := f.addParam(f.result, ParamValue, ast.Position{})
	entry := f.graph.NewBlock()
	for i := 0; i < count; i++ {
		state := f.addState(entry, ssa.OpStatePresent, f.result, ast.Position{}, value)
		value = f.addOp(entry, ssa.OpStateGet, f.result, ast.Position{}, state)
	}
	f.graph.SetRet(entry, value)
	return f
}

func TestAvailabilityLongAliasChain(t *testing.T) {
	f := availabilityChain(1024)
	out, err := LowerARM64SSA(singleProgram(f))
	if err != nil {
		t.Fatal(err)
	}
	lowered := out.Functions[out.Symbols["pilot"]]
	if len(lowered.Blocks) != 1 || len(lowered.Blocks[0].Ops) != 0 || lowered.Blocks[0].Term.Value != lowered.Params[0] {
		t.Fatal("known-present alias chain must lower directly to its payload without flag instructions")
	}
}

func availabilityPresenceOnly() *availabilityFixture {
	a := availabilityDiamond(ast.NumberType{})
	a.f.result = ast.BoolType{}
	delete(a.f.values, a.get.Result.ID)
	delete(a.f.values, a.bad.Term.Value.ID)
	a.good.Ops, a.bad.Ops = nil, nil
	a.f.graph.SetRet(a.good, a.has.Result)
	a.f.graph.SetRet(a.bad, a.has.Result)
	return a
}

func TestAvailabilityPresenceOnlyOmitsPayloadLanes(t *testing.T) {
	a := availabilityPresenceOnly()
	if err := Verify(a.f); err != nil {
		t.Fatal(err)
	}
	demand := availabilityLaneDemand(a.f)
	for _, state := range []ssa.Value{a.none, a.some, a.state} {
		if demand[state.ID] != statePresenceLane {
			t.Fatal("presence-only use acquired a payload demand")
		}
	}
	out, err := LowerARM64SSA(singleProgram(a.f))
	if err != nil {
		t.Fatal(err)
	}
	phis := 0
	for _, block := range out.Functions[out.Symbols["pilot"]].Blocks {
		for _, op := range block.Ops {
			if op.Kind == ssa.OpConstInt {
				t.Fatal("presence-only state emitted an inactive payload word")
			}
			if op.Kind == ssa.OpPhi {
				phis++
			}
		}
	}
	if phis != 1 {
		t.Fatalf("presence-only join needs exactly one phi, got %d", phis)
	}
}

func BenchmarkAvailabilityScalarStates(b *testing.B) {
	for _, count := range []int{1, 64, 1024} {
		b.Run(fmt.Sprintf("states-%d", count), func(b *testing.B) {
			p := singleProgram(availabilityChain(count))
			if _, err := LowerARM64SSA(p); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := LowerARM64SSA(p); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
