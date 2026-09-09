package semir

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

func guardedBindingDiamond(mode ParamMode, saved bool) (*bindingSnapshotFixture, *ssa.Op) {
	typ := ast.ArrayType{Elem: armU64}
	a := bindingSnapshotDiamond(typ, mode)
	f := a.f
	old := a.good.Ops[0].Result
	element := f.addOp(a.good, ssa.OpConstInt, armU64, ast.Position{})
	a.good.Ops[len(a.good.Ops)-1].Imm = 41
	replacement := f.addOp(a.good, ssa.OpArrayAppend, typ, ast.Position{}, old, element)
	write := f.replaceBindingGuarded(a.good, a.id, a.snapshot, replacement, ast.Position{Line: 8, Col: 3})
	latest := f.snapshotBinding(a.good, a.id, ast.Position{})
	has := f.addOp(a.good, ssa.OpStateHas, ast.BoolType{}, ast.Position{}, latest)
	yes, no := f.graph.NewBlock(), f.graph.NewBlock()
	f.graph.SetBrIf(a.good, has, yes, no)
	value := f.addOp(yes, ssa.OpStateGet, typ, ast.Position{}, latest)
	if saved {
		value = old
	}
	f.graph.SetRet(yes, value)
	f.graph.SetRet(no, f.addOp(no, ssa.OpArrayMake, typ, ast.Position{}))
	return a, write
}

func TestGuardedBindingReplacementPromotesSequentialValues(t *testing.T) {
	for _, mode := range []ParamMode{ParamBorrow, ParamCounted} {
		for _, saved := range []bool{false, true} {
			t.Run(fmt.Sprintf("mode-%d/saved-%t", mode, saved), func(t *testing.T) {
				a, write := guardedBindingDiamond(mode, saved)
				want := write.Args[1]
				if err := Verify(a.f); err != nil {
					t.Fatal(err)
				}
				if err := promoteBindings(a.f); err != nil {
					t.Fatal(err)
				}
				if err := Verify(a.f); err != nil {
					t.Fatal(err)
				}
				found := false
				for _, block := range a.f.graph.Blocks {
					for _, op := range block.Ops {
						if bindingOp(op.Kind) {
							t.Fatal("guarded write escaped place promotion")
						}
						found = found || op.Kind == ssa.OpStatePresent && op.Args[0] == want
					}
				}
				if !found {
					t.Fatal("later snapshot lost the replacement payload")
				}
				if _, err := LowerARM64SSA(singleProgram(a.f)); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestGuardedBindingReplacementRejectsInvalidWitnesses(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		edit       func(*bindingSnapshotFixture, *ssa.Op)
	}{
		{"foreign-binding", "invalid identity", func(a *bindingSnapshotFixture, op *ssa.Op) { op.Imm = 999 }},
		{"wrong-phase", "outside", func(a *bindingSnapshotFixture, op *ssa.Op) { a.f.unpromotedBindings = false }},
		{"missing-value", "operands or types", func(a *bindingSnapshotFixture, op *ssa.Op) { op.Args = op.Args[:1] }},
		{"swapped-operands", "operands or types", func(a *bindingSnapshotFixture, op *ssa.Op) { op.Args[0], op.Args[1] = op.Args[1], op.Args[0] }},
		{"different-binding", "same binding", func(a *bindingSnapshotFixture, op *ssa.Op) {
			op.Imm = int64(a.f.addBinding("other", a.f.bindings[a.id-1].typ, ast.Position{}))
		}},
		{"different-snapshot", "presence proof", func(a *bindingSnapshotFixture, op *ssa.Op) {
			op.Args[0] = a.f.snapshotBinding(a.join, a.id, ast.Position{})
		}},
		{"false-edge", "presence proof", func(a *bindingSnapshotFixture, op *ssa.Op) {
			a.good.Ops = removeGuardedOp(a.good.Ops, op)
			// Use a parameter payload so the edit preserves SSA dominance.
			op.Args[1] = a.payload
			a.bad.Ops = append(a.bad.Ops, op)
		}},
		{"no-guard", "presence proof", func(a *bindingSnapshotFixture, op *ssa.Op) {
			a.good.Ops = removeGuardedOp(a.good.Ops, op)
			op.Args[1] = a.payload
			a.join.Ops = append(a.join.Ops, op)
		}},
		{"arbitrary-present", "same binding", func(a *bindingSnapshotFixture, op *ssa.Op) {
			state := a.f.addState(a.join, ssa.OpStatePresent, a.f.bindings[a.id-1].typ, ast.Position{}, a.payload)
			op.Args[0] = state
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, write := guardedBindingDiamond(ParamBorrow, false)
			tc.edit(a, write)
			if err := ssa.Verify(a.f.graph); err != nil {
				t.Fatalf("fixture must preserve structural SSA: %v", err)
			}
			if err := Verify(a.f); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func removeGuardedOp(ops []*ssa.Op, target *ssa.Op) []*ssa.Op {
	return slices.DeleteFunc(ops, func(op *ssa.Op) bool { return op == target })
}

func TestGuardedBindingReplacementAllowsEarlierReplacement(t *testing.T) {
	a, first := guardedBindingDiamond(ParamBorrow, false)
	typ := a.f.bindings[a.id-1].typ
	secondValue := a.f.addParam(typ, ParamBorrow, ast.Position{})
	second := a.f.replaceBindingGuarded(a.good, a.id, a.snapshot, secondValue, ast.Position{})
	a.good.Ops = a.good.Ops[:len(a.good.Ops)-1]
	a.good.Ops = slices.Insert(a.good.Ops, slices.Index(a.good.Ops, first)+1, second)
	if err := promoteBindings(a.f); err != nil {
		t.Fatal(err)
	}
	if err := Verify(a.f); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, op := range a.good.Ops {
		found = found || op.Kind == ssa.OpStatePresent && op.Args[0] == secondValue
	}
	if !found {
		t.Fatal("old availability witness prevented publishing the latest replacement")
	}
	if _, err := LowerARM64SSA(singleProgram(a.f)); err != nil {
		t.Fatal(err)
	}
}

func TestGuardedBindingReplacementPromotesOrdinaryReadPayload(t *testing.T) {
	a, write := guardedBindingDiamond(ParamBorrow, false)
	id := a.f.addBinding("reader", a.f.bindings[a.id-1].typ, ast.Position{})
	a.f.writeBinding(a.entry, ssa.OpBindingInit, id, a.payload, ast.Position{})
	read := a.f.readBinding(a.good, id, ast.Position{})
	readOp := a.good.Ops[len(a.good.Ops)-1]
	a.good.Ops = a.good.Ops[:len(a.good.Ops)-1]
	a.good.Ops = slices.Insert(a.good.Ops, slices.Index(a.good.Ops, write), readOp)
	write.Args[1] = read
	if err := promoteBindings(a.f); err != nil {
		t.Fatal(err)
	}
	if err := Verify(a.f); err != nil {
		t.Fatal(err)
	}
	if _, err := LowerARM64SSA(singleProgram(a.f)); err != nil {
		t.Fatal(err)
	}
}

func guardedBindingReplacementLoop(saved bool) *Func {
	num := ast.NumberType{}
	typ := ast.ArrayType{Elem: num}
	f := newFunc("pilot", typ)
	f.unpromotedBindings = true
	limit := f.addParam(num, ParamValue, ast.Position{})
	present := f.addParam(ast.BoolType{}, ParamValue, ast.Position{})
	id := f.addBinding("items", typ, ast.Position{})
	g := f.graph
	entry, init, absent, join := g.NewBlock(), g.NewBlock(), g.NewBlock(), g.NewBlock()
	header, body, replace, latch := g.NewBlock(), g.NewBlock(), g.NewBlock(), g.NewBlock()
	exit, good, bad := g.NewBlock(), g.NewBlock(), g.NewBlock()
	zero := f.addOp(entry, ssa.OpConstInt, num, ast.Position{})
	one := f.addOp(entry, ssa.OpConstInt, num, ast.Position{})
	entry.Ops[len(entry.Ops)-1].Imm = 1
	g.SetBrIf(entry, present, init, absent)
	array := f.addOp(init, ssa.OpArrayMake, typ, ast.Position{}, zero)
	f.writeBinding(init, ssa.OpBindingInit, id, array, ast.Position{})
	g.SetBr(init, join)
	g.SetBr(absent, join)
	var original ssa.Value
	if saved {
		original = f.snapshotBinding(join, id, ast.Position{})
	}
	g.SetBr(join, header)
	g.SetBr(latch, header)
	i := f.addPhi(header, num, ast.Position{}, zero, zero)
	more := f.addOp(header, ssa.OpLt, ast.BoolType{}, ast.Position{}, i, limit)
	g.SetBrIf(header, more, body, exit)
	witness := f.snapshotBinding(body, id, ast.Position{})
	has := f.addOp(body, ssa.OpStateHas, ast.BoolType{}, ast.Position{}, witness)
	g.SetBrIf(body, has, replace, latch)
	old := f.addOp(replace, ssa.OpStateGet, typ, ast.Position{}, witness)
	replacement := f.addOp(replace, ssa.OpArrayAppend, typ, ast.Position{}, old, i)
	f.replaceBindingGuarded(replace, id, witness, replacement, ast.Position{})
	g.SetBr(replace, latch)
	next := f.addOp(latch, ssa.OpAdd, num, ast.Position{}, i, one)
	header.Ops[0].Args[1] = next
	result := original
	if !saved {
		result = f.snapshotBinding(exit, id, ast.Position{})
	}
	hasResult := f.addOp(exit, ssa.OpStateHas, ast.BoolType{}, ast.Position{}, result)
	g.SetBrIf(exit, hasResult, good, bad)
	g.SetRet(good, f.addOp(good, ssa.OpStateGet, typ, ast.Position{}, result))
	g.SetRet(bad, f.addOp(bad, ssa.OpArrayMake, typ, ast.Position{}))
	return f
}

func TestGuardedBindingReplacementRefreshesLoopWitness(t *testing.T) {
	f, body, exit, id, value := bindingLifetimeFunc()
	snapshot := f.snapshotBinding(body, id, ast.Position{})
	has := f.addOp(body, ssa.OpStateHas, ast.BoolType{}, ast.Position{}, snapshot)
	yes, no := f.graph.NewBlock(), f.graph.NewBlock()
	f.graph.SetBrIf(body, has, yes, no)
	exit.Preds = nil
	f.replaceBindingGuarded(yes, id, snapshot, value, ast.Position{})
	f.graph.SetBr(yes, body)
	f.graph.SetBr(no, exit)
	iteration := f.boundaries[1]
	f.cleanupExits[0] = &cleanupExit{from: iteration, through: iteration, start: yes, finish: yes, target: body, kind: cleanupContinue, ends: []cleanupEnd{{iteration, yes}}}
	f.cleanupExits = append(f.cleanupExits, &cleanupExit{from: iteration, through: iteration, start: no, finish: no, target: exit, kind: cleanupBreak, ends: []cleanupEnd{{iteration, no}}})
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
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

func TestGuardedBindingReplacementRejectsExpiredWitness(t *testing.T) {
	f, body, exit, id, value := bindingLifetimeFunc()
	snapshot := f.snapshotBinding(body, id, ast.Position{})
	has := f.addOp(exit, ssa.OpStateHas, ast.BoolType{}, ast.Position{}, snapshot)
	yes, no := f.graph.NewBlock(), f.graph.NewBlock()
	f.graph.SetBrIf(exit, has, yes, no)
	f.replaceBindingGuarded(yes, id, snapshot, value, ast.Position{Line: 12, Col: 4})
	f.graph.SetRet(yes, value)
	f.graph.SetRet(no, value)
	root := f.boundaries[0]
	f.cleanupExits = f.cleanupExits[:1]
	for _, block := range []*ssa.Block{yes, no} {
		f.cleanupExits = append(f.cleanupExits, &cleanupExit{from: root, through: root, start: block, finish: block, kind: cleanupReturn, ends: []cleanupEnd{{root, block}}})
	}
	if err := ssa.Verify(f.graph); err != nil {
		t.Fatal(err)
	}
	if err := Verify(f); err == nil || !strings.Contains(err.Error(), "12:4: guarded replacement witness crosses a binding lifetime end") {
		t.Fatalf("old snapshot resurrected an ended place: %v", err)
	}
}

func TestGuardedBindingReplacementRejectsGuardBypass(t *testing.T) {
	f, entry, id, value := bindingTestFunc()
	flag := f.addParam(ast.BoolType{}, ParamValue, ast.Position{})
	f.writeBinding(entry, ssa.OpBindingInit, id, value, ast.Position{})
	witness := f.snapshotBinding(entry, id, ast.Position{})
	test, bypass, good, bad := f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock()
	f.graph.SetBrIf(entry, flag, test, bypass)
	has := f.addOp(test, ssa.OpStateHas, ast.BoolType{}, ast.Position{}, witness)
	f.graph.SetBrIf(test, has, good, bad)
	f.graph.SetBr(bypass, good)
	f.replaceBindingGuarded(good, id, witness, value, ast.Position{Line: 25, Col: 2})
	f.graph.SetRet(good, value)
	f.graph.SetRet(bad, value)
	if err := ssa.Verify(f.graph); err != nil {
		t.Fatal(err)
	}
	if err := Verify(f); err == nil || !strings.Contains(err.Error(), "25:2: availability payload lacks a presence proof") {
		t.Fatalf("a second predecessor bypassed the required witness guard: %v", err)
	}
}

func TestGuardedBindingReplacementDoesNotInitializeAbsentPaths(t *testing.T) {
	a, _ := guardedBindingDiamond(ParamBorrow, false)
	a.f.readBinding(a.good, a.id, ast.Position{Line: 19, Col: 2})
	if err := Verify(a.f); err == nil || !strings.Contains(err.Error(), "19:2: read or replacement requires initialization") {
		t.Fatalf("guarded write weakened ordinary must-initialization: %v", err)
	}
	if ssa.IsPure(ssa.OpBindingReplaceGuarded) || ssa.OpBindingReplaceGuarded.String() != "binding_replace_guarded" {
		t.Fatal("guarded replacement lost its semantic effect contract")
	}
}

func guardedBindingChain(count int) *Func {
	a := bindingSnapshotDiamond(ast.NumberType{}, ParamValue)
	for _, op := range a.good.Ops {
		delete(a.f.values, op.Result.ID)
	}
	a.good.Ops = nil
	current := a.good
	for i := 0; i < count; i++ {
		a.f.replaceBindingGuarded(current, a.id, a.snapshot, a.payload, ast.Position{})
		if i+1 < count {
			next := a.f.graph.NewBlock()
			a.f.graph.SetBr(current, next)
			current = next
		}
	}
	a.f.graph.SetRet(current, a.payload)
	return a.f
}

func BenchmarkGuardedBindingWitnesses(b *testing.B) {
	for _, count := range []int{1, 64, 1024} {
		b.Run(fmt.Sprintf("writes-%d", count), func(b *testing.B) {
			f := guardedBindingChain(count)
			if err := Verify(f); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := Verify(f); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
