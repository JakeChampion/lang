package semir

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

type bindingSnapshotFixture struct {
	f                               *Func
	id                              BindingID
	entry, yes, no, join, good, bad *ssa.Block
	payload, snapshot               ssa.Value
}

func bindingSnapshotDiamond(typ ast.Type, mode ParamMode) *bindingSnapshotFixture {
	f := newFunc("pilot", typ)
	f.unpromotedBindings = true
	a := &bindingSnapshotFixture{f: f}
	flag := f.addParam(ast.BoolType{}, ParamValue, ast.Position{})
	a.payload = f.addParam(typ, mode, ast.Position{})
	a.id = f.addBinding("item", typ, ast.Position{Line: 2, Col: 3})
	a.entry, a.yes, a.no = f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock()
	a.join, a.good, a.bad = f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock()
	f.graph.SetBrIf(a.entry, flag, a.yes, a.no)
	f.writeBinding(a.yes, ssa.OpBindingInit, a.id, a.payload, ast.Position{Line: 3, Col: 4})
	f.graph.SetBr(a.no, a.join)
	f.graph.SetBr(a.yes, a.join)
	a.snapshot = f.snapshotBinding(a.join, a.id, ast.Position{Line: 6, Col: 2})
	has := f.addOp(a.join, ssa.OpStateHas, ast.BoolType{}, ast.Position{}, a.snapshot)
	f.graph.SetBrIf(a.join, has, a.good, a.bad)
	value := f.addOp(a.good, ssa.OpStateGet, typ, ast.Position{}, a.snapshot)
	f.graph.SetRet(a.good, value)
	kind := ssa.OpConstInt
	if referenceBearing(typ) {
		kind = ssa.OpArrayMake
	}
	fallback := f.addOp(a.bad, kind, typ, ast.Position{})
	f.graph.SetRet(a.bad, fallback)
	return a
}

func TestBindingSnapshotPromotionPreservesOptionalJoin(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		a := bindingSnapshotDiamond(ast.ArrayType{Elem: armU64}, ParamCounted)
		if reverse {
			slices.Reverse(a.f.graph.Blocks)
		}
		if err := Verify(a.f); err != nil {
			t.Fatal(err)
		}
		if err := promoteBindings(a.f); err != nil {
			t.Fatal(err)
		}
		if err := Verify(a.f); err != nil {
			t.Fatal(err)
		}
		phi := a.join.Ops[0]
		if phi.Kind != ssa.OpPhi || a.f.values[phi.Result.ID].typ.form != availabilityForm {
			t.Fatal("optional binding lost its complete state type")
		}
		defs := make(map[int32]*ssa.Op)
		for _, block := range a.f.graph.Blocks {
			for _, op := range block.Ops {
				if bindingOp(op.Kind) {
					t.Fatal("place operation escaped promotion")
				}
				defs[op.Result.ID] = op
			}
		}
		if defs[phi.Args[0].ID].Kind != ssa.OpStateAbsent || defs[phi.Args[1].ID].Kind != ssa.OpStatePresent {
			t.Fatal("snapshot phi lost predecessor order or fabricated a payload")
		}
		if _, err := LowerARM64SSA(singleProgram(a.f)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBindingSnapshotVerifierRejectsMalformedAndUnguarded(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*bindingSnapshotFixture)
		want string
	}{
		{"foreign-binding", func(a *bindingSnapshotFixture) { a.join.Ops[0].Imm++ }, "invalid identity"},
		{"wrong-phase", func(a *bindingSnapshotFixture) { a.f.unpromotedBindings = false }, "outside"},
		{"ordinary-result", func(a *bindingSnapshotFixture) {
			info := a.f.values[a.snapshot.ID]
			info.typ.form = sourceForm
			a.f.values[a.snapshot.ID] = info
		}, "snapshot type"},
		{"payload-operand", func(a *bindingSnapshotFixture) { a.join.Ops[0].Args = []ssa.Value{a.payload} }, "snapshot type"},
		{"unguarded-get", func(a *bindingSnapshotFixture) {
			a.join.Ops = append(a.join.Ops, a.good.Ops[0])
			a.good.Ops = nil
		}, "presence proof"},
		{"different-snapshot-guard", func(a *bindingSnapshotFixture) {
			other := a.f.snapshotBinding(a.join, a.id, ast.Position{})
			a.good.Ops[0].Args[0] = other
		}, "presence proof"},
		{"ordinary-read-still-rejects", func(a *bindingSnapshotFixture) {
			a.f.readBinding(a.good, a.id, ast.Position{})
		}, "requires initialization"},
		{"ordinary-replace-still-rejects", func(a *bindingSnapshotFixture) {
			a.f.writeBinding(a.good, ssa.OpBindingReplace, a.id, a.payload, ast.Position{})
		}, "requires initialization"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := bindingSnapshotDiamond(ast.NumberType{}, ParamValue)
			tc.edit(a)
			if err := ssa.Verify(a.f.graph); err != nil {
				t.Fatal(err)
			}
			if err := Verify(a.f); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wanted %q, got %v", tc.want, err)
			}
		})
	}
}

func TestBindingSnapshotOnlyMaterializesDemandedDefinitions(t *testing.T) {
	f, entry, id, value := bindingTestFunc()
	f.writeBinding(entry, ssa.OpBindingInit, id, value, ast.Position{})
	for range 64 {
		f.writeBinding(entry, ssa.OpBindingReplace, id, value, ast.Position{})
	}
	state := f.snapshotBinding(entry, id, ast.Position{})
	f.result = ast.BoolType{}
	f.graph.SetRet(entry, f.addOp(entry, ssa.OpStateHas, f.result, ast.Position{}, state))
	if err := promoteBindings(f); err != nil {
		t.Fatal(err)
	}
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, op := range entry.Ops {
		if op.Kind == ssa.OpStatePresent {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("wrapped %d definitions, want only the observed replacement", count)
	}
}

func TestBindingSnapshotIsSemanticOnly(t *testing.T) {
	if ssa.OpBindingSnapshot.String() != "binding_snapshot" || ssa.IsPure(ssa.OpBindingSnapshot) || !bindingOp(ssa.OpBindingSnapshot) {
		t.Fatal("binding snapshot lost its pre-promotion phase contract")
	}
}

func TestBindingSnapshotManyPhiIdentities(t *testing.T) {
	f, entry, _, payload := bindingTestFunc()
	flag := f.addParam(ast.BoolType{}, ParamValue, ast.Position{})
	yes, no, join := f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock()
	f.graph.SetBrIf(entry, flag, yes, no)
	f.graph.SetBr(no, join)
	f.graph.SetBr(yes, join)
	for range 128 {
		id := f.addBinding("observed", ast.NumberType{}, ast.Position{})
		f.writeBinding(yes, ssa.OpBindingInit, id, payload, ast.Position{})
		state := f.snapshotBinding(join, id, ast.Position{})
		f.addOp(join, ssa.OpStateHas, ast.BoolType{}, ast.Position{}, state)
	}
	f.graph.SetRet(join, payload)
	if err := promoteBindings(f); err != nil {
		t.Fatal(err)
	}
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, op := range join.Ops {
		if op.Kind == ssa.OpPhi {
			count++
		}
	}
	if count != 128 {
		t.Fatalf("got %d state phis, want 128", count)
	}
	if _, err := LowerARM64SSA(singleProgram(f)); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkBindingSnapshotPromotion(b *testing.B) {
	for _, writes := range []int{1, 64, 1024} {
		b.Run(fmt.Sprintf("writes-%d", writes), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				f, entry, id, value := bindingTestFunc()
				f.writeBinding(entry, ssa.OpBindingInit, id, value, ast.Position{})
				for i := 1; i < writes; i++ {
					f.writeBinding(entry, ssa.OpBindingReplace, id, value, ast.Position{})
				}
				f.result = ast.BoolType{}
				state := f.snapshotBinding(entry, id, ast.Position{})
				f.graph.SetRet(entry, f.addOp(entry, ssa.OpStateHas, f.result, ast.Position{}, state))
				if err := promoteBindings(f); err != nil {
					b.Fatal(err)
				}
				if _, err := LowerARM64SSA(singleProgram(f)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestBindingSnapshotBeforeInitializationIsAbsent(t *testing.T) {
	f, entry, id, _ := bindingTestFunc()
	f.result = ast.BoolType{}
	state := f.snapshotBinding(entry, id, ast.Position{})
	f.graph.SetRet(entry, f.addOp(entry, ssa.OpStateHas, f.result, ast.Position{}, state))
	if err := promoteBindings(f); err != nil {
		t.Fatal(err)
	}
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
	if entry.Ops[0].Kind != ssa.OpStateAbsent {
		t.Fatal("uninitialized snapshot invented a payload")
	}
}

func bindingSnapshotLoop() *Func {
	typ := ast.ArrayType{Elem: armU64}
	f := newFunc("pilot", typ)
	f.unpromotedBindings = true
	payload := f.addParam(typ, ParamCounted, ast.Position{})
	id := f.addBinding("item", typ, ast.Position{})
	entry, header, body, exit := f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock()
	f.graph.SetBr(entry, header)
	state := f.snapshotBinding(header, id, ast.Position{})
	has := f.addOp(header, ssa.OpStateHas, ast.BoolType{}, ast.Position{}, state)
	f.graph.SetBrIf(header, has, exit, body)
	f.writeBinding(body, ssa.OpBindingInit, id, payload, ast.Position{})
	f.graph.SetBr(body, header)
	f.graph.SetRet(exit, f.addOp(exit, ssa.OpStateGet, typ, ast.Position{}, state))
	return f
}

func bindingSnapshotReplacement() *Func {
	typ := ast.ArrayType{Elem: armU64}
	f := newFunc("pilot", typ)
	f.unpromotedBindings = true
	payload := f.addParam(typ, ParamCounted, ast.Position{})
	id := f.addBinding("item", typ, ast.Position{})
	entry, good, bad := f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock()
	f.writeBinding(entry, ssa.OpBindingInit, id, payload, ast.Position{})
	saved := f.snapshotBinding(entry, id, ast.Position{})
	other := f.addOp(entry, ssa.OpArrayMake, typ, ast.Position{})
	f.writeBinding(entry, ssa.OpBindingReplace, id, other, ast.Position{})
	has := f.addOp(entry, ssa.OpStateHas, ast.BoolType{}, ast.Position{}, saved)
	f.graph.SetBrIf(entry, has, good, bad)
	f.graph.SetRet(good, f.addOp(good, ssa.OpStateGet, typ, ast.Position{}, saved))
	f.graph.SetRet(bad, other)
	return f
}

func bindingSnapshotLifetime() *Func {
	f, body, exit, id, _ := bindingLifetimeFunc()
	f.graph.Name = "pilot"
	typ := ast.ArrayType{Elem: armU64}
	f.result, f.bindings[id-1].typ = typ, typ
	payload := f.addParam(typ, ParamCounted, ast.Position{})
	body.Ops[0].Args[0] = payload
	saved := f.snapshotBinding(body, id, ast.Position{})
	after := f.snapshotBinding(exit, id, ast.Position{})
	f.addOp(exit, ssa.OpStateHas, ast.BoolType{}, ast.Position{}, after)
	has := f.addOp(exit, ssa.OpStateHas, ast.BoolType{}, ast.Position{}, saved)
	good, bad := f.graph.NewBlock(), f.graph.NewBlock()
	f.graph.SetBrIf(exit, has, good, bad)
	f.graph.SetRet(good, f.addOp(good, ssa.OpStateGet, typ, ast.Position{}, saved))
	f.graph.SetRet(bad, f.addOp(bad, ssa.OpArrayMake, typ, ast.Position{}))
	root := f.boundaries[0]
	f.cleanupExits = f.cleanupExits[:1]
	for _, block := range []*ssa.Block{good, bad} {
		f.cleanupExits = append(f.cleanupExits, &cleanupExit{from: root, through: root, start: block, finish: block, kind: cleanupReturn, ends: []cleanupEnd{{root, block}}})
	}
	return f
}

func TestBindingSnapshotLoopsAndSavedReplacement(t *testing.T) {
	for _, build := range []func() *Func{bindingSnapshotLoop, bindingSnapshotReplacement, bindingSnapshotLifetime} {
		f := build()
		if err := promoteBindings(f); err != nil {
			t.Fatal(err)
		}
		if err := Verify(f); err != nil {
			t.Fatal(err)
		}
		if _, err := LowerARM64SSA(singleProgram(f)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBindingSnapshotLifetimeEndPreservesOldState(t *testing.T) {
	f, body, exit, id, _ := bindingLifetimeFunc()
	saved := f.snapshotBinding(body, id, ast.Position{})
	after := f.snapshotBinding(exit, id, ast.Position{})
	beforeHas := f.addOp(exit, ssa.OpStateHas, ast.BoolType{}, ast.Position{}, saved)
	afterHas := f.addOp(exit, ssa.OpStateHas, ast.BoolType{}, ast.Position{}, after)
	f.result = ast.BoolType{}
	f.graph.SetRet(exit, f.addOp(exit, ssa.OpEq, ast.BoolType{}, ast.Position{}, beforeHas, afterHas))
	if err := promoteBindings(f); err != nil {
		t.Fatal(err)
	}
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
	defs := make(map[int32]*ssa.Op)
	for _, block := range f.graph.Blocks {
		for _, op := range block.Ops {
			defs[op.Result.ID] = op
		}
	}
	if defs[defs[beforeHas.ID].Args[0].ID].Kind != ssa.OpStatePresent || defs[defs[afterHas.ID].Args[0].ID].Kind != ssa.OpStateAbsent {
		t.Fatal("lifetime end invalidated saved state or preserved a stale place")
	}
	if _, err := LowerARM64SSA(singleProgram(f)); err != nil {
		t.Fatal(err)
	}
}
