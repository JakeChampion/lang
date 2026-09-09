package semir

import (
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

// Count actually executed guarded writes across distinct iteration lifetimes.
// A snapshot before initialization must be absent on EVERY iteration, even
// though the previous iteration initialized and potentially replaced the place.
func guardedBindingEpochLoop(beforeInit bool) *Func {
	typ, pos := ast.NumberType{}, ast.Position{Line: 1, Col: 1}
	f := newFunc("pilot", typ)
	f.unpromotedBindings = true
	rounds := f.addParam(typ, ParamValue, pos)
	entry := f.graph.NewBlock()
	header, body, yes, no, latch, exit := f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock(), f.graph.NewBlock()
	b := builder{fn: f}
	root := b.newCleanupBoundary(nil, entry, nil, nil, pos)
	iteration := b.newCleanupBoundary(root, body, header, exit, pos)
	id := f.addBinding("epoch-local", typ, pos)
	f.bindings[id-1].boundary = iteration
	zero := f.addOp(entry, ssa.OpConstInt, typ, pos)
	one := f.addOp(entry, ssa.OpConstInt, typ, pos)
	entry.Ops[len(entry.Ops)-1].Imm = 1
	f.graph.SetBr(entry, header)
	i := f.addPhi(header, typ, pos, zero)
	iPhi := header.Ops[len(header.Ops)-1]
	writes := f.addPhi(header, typ, pos, zero)
	writesPhi := header.Ops[len(header.Ops)-1]
	more := f.addOp(header, ssa.OpLt, ast.BoolType{}, pos, i, rounds)
	f.graph.SetBrIf(header, more, body, exit)
	var snapshot ssa.Value
	if beforeInit {
		snapshot = f.snapshotBinding(body, id, pos)
	}
	f.writeBinding(body, ssa.OpBindingInit, id, one, pos)
	if !beforeInit {
		snapshot = f.snapshotBinding(body, id, pos)
	}
	has := f.addOp(body, ssa.OpStateHas, ast.BoolType{}, pos, snapshot)
	f.graph.SetBrIf(body, has, yes, no)
	f.replaceBindingGuarded(yes, id, snapshot, zero, pos)
	updated := f.addOp(yes, ssa.OpAdd, typ, pos, writes, one)
	f.graph.SetBr(yes, latch)
	f.graph.SetBr(no, latch)
	nextWrites := f.addPhi(latch, typ, pos, updated, writes)
	next := f.addOp(latch, ssa.OpAdd, typ, pos, i, one)
	f.graph.SetBr(latch, header)
	iPhi.Args = append(iPhi.Args, next)
	writesPhi.Args = append(writesPhi.Args, nextWrites)
	f.graph.SetRet(exit, writes)
	f.cleanupExits = []*cleanupExit{
		{from: iteration, through: iteration, start: latch, finish: latch, target: header, kind: cleanupContinue, ends: []cleanupEnd{{iteration, latch}}},
		{from: root, through: root, start: exit, finish: exit, kind: cleanupReturn, ends: []cleanupEnd{{root, exit}}},
	}
	return f
}

func TestGuardedBindingEpochPresenceAfterPromotion(t *testing.T) {
	for _, before := range []bool{false, true} {
		t.Run(fmt.Sprintf("snapshot-before-init-%t", before), func(t *testing.T) {
			f := guardedBindingEpochLoop(before)
			if err := Verify(f); err != nil {
				t.Fatal(err)
			}
			if err := promoteBindings(f); err != nil {
				t.Fatal(err)
			}
			if err := Verify(f); err != nil {
				t.Fatal(err)
			}
			for _, block := range f.graph.Blocks {
				for _, op := range block.Ops {
					if op.Kind == ssa.OpStateHas {
						want := uint8(2)
						if before {
							want = 1
						}
						if got := promotedPresence(f, op.Args[0]); got != want {
							t.Fatalf("fresh epoch snapshot presence=%b, want %b", got, want)
						}
						return
					}
				}
			}
			t.Fatal("missing snapshot presence observation")
		})
	}
}

func TestARM64TypedGuardedBindingEpochs(t *testing.T) {
	armLauncher(t)
	for _, before := range []bool{false, true} {
		for _, rounds := range []int64{0, 1, 2, 3, 64} {
			for _, optimize := range []bool{false, true} {
				t.Run(fmt.Sprintf("before-init-%t/rounds-%d/optimized-%t", before, rounds, optimize), func(t *testing.T) {
					f := guardedBindingEpochLoop(before)
					if err := promoteBindings(f); err != nil {
						t.Fatal(err)
					}
					out := lowerSemanticARM64(t, f)
					b := harnessBuilder(out)
					result := b.call(out.Symbols["pilot"], 64, true, b.constant(rounds))
					want := rounds
					if before {
						want = 0
					}
					bad := b.op(ssa.OpNe, 32, false, result, b.constant(want))
					b.f.SetRet(b.b, bad)
					requireARM64Success(t, out, b.f.Name, optimize)
				})
			}
		}
	}
}
