package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// zeroSlotFunc builds one function whose slot `slot` is zero-initialised
// in the prologue, then guarded by `local.load slot ; rc_is_unique ; if
// … end`. nparams says how many leading slots are parameters; body is
// spliced between the guard's OpIf and its OpEnd.
func zeroSlotFunc(nparams int, slot int32, prologue []Op, guardIn []Op) *Func {
	fn := &Func{Name: "f"}
	for i := 0; i < nparams; i++ {
		fn.Params = append(fn.Params, ast.Param{Name: "p"})
	}
	fn.Ops = append(fn.Ops, Op{Kind: OpConstI32, I32: 0}, Op{Kind: OpStoreLocal, I32: slot})
	fn.Ops = append(fn.Ops, prologue...)
	fn.Ops = append(fn.Ops, guardIn...)
	fn.Ops = append(fn.Ops,
		Op{Kind: OpLoadLocal, I32: slot},
		Op{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1},
		Op{Kind: OpIf},
		Op{Kind: OpConstI32, I32: 7},
		Op{Kind: OpDrop},
		Op{Kind: OpEnd},
	)
	if len(guardIn) > 0 {
		fn.Ops = append(fn.Ops, Op{Kind: OpEnd}) // close the loop/block wrapper
	}
	fn.Ops = append(fn.Ops, Op{Kind: OpReturn})
	return fn
}

func guardFolded(fn *Func) bool {
	for _, o := range fn.Ops {
		if o.Kind == OpRcIsUnique {
			return false
		}
	}
	return true
}

// The slot's only write before the guard is the prologue's zero-init, so
// the guard tests null and the drop body it gates is unreachable.
func TestPruneZeroSlotGuardsFolds(t *testing.T) {
	fn := zeroSlotFunc(0, 3, nil, nil)
	p := &Program{Funcs: []*Func{fn}}
	if !PruneZeroSlotGuards(p) {
		t.Fatal("expected the guard to be pruned")
	}
	if !guardFolded(fn) {
		t.Errorf("guard survived:\n%s", p)
	}
	// Fold then deletes the block the constant conditions.
	Fold(p)
	for _, o := range fn.Ops {
		if o.Kind == OpIf {
			t.Errorf("pruneConstIf did not take the block:\n%s", p)
		}
	}
}

// A PARAMETER arrives with its argument, never a zero — the slot index
// being below len(Params) is the only thing separating the two, and
// eliding a parameter's drop would leak it.
func TestPruneZeroSlotGuardsRefusesParam(t *testing.T) {
	fn := zeroSlotFunc(4, 1, nil, nil)
	p := &Program{Funcs: []*Func{fn}}
	if PruneZeroSlotGuards(p) {
		t.Error("a parameter slot must not be pruned")
	}
	if guardFolded(fn) {
		t.Errorf("guard on a parameter was folded:\n%s", p)
	}
}

// A real write before the guard means the slot may hold a live box, and
// its drop has to run.
func TestPruneZeroSlotGuardsRefusesRealStore(t *testing.T) {
	prologue := []Op{{Kind: OpConstI32, I32: 42}, {Kind: OpStoreLocal, I32: 3}}
	fn := zeroSlotFunc(0, 3, prologue, nil)
	p := &Program{Funcs: []*Func{fn}}
	if PruneZeroSlotGuards(p) {
		t.Error("a slot written before the guard must not be pruned")
	}
}

// A tee writes the slot just as a store does. Scanning only OpStoreLocal
// would read this slot as never written and prune a live drop.
func TestPruneZeroSlotGuardsRefusesTee(t *testing.T) {
	prologue := []Op{{Kind: OpConstI32, I32: 42}, {Kind: OpTeeLocal, I32: 3}, {Kind: OpDrop}}
	fn := zeroSlotFunc(0, 3, prologue, nil)
	p := &Program{Funcs: []*Func{fn}}
	if PruneZeroSlotGuards(p) {
		t.Error("a slot tee'd before the guard must not be pruned")
	}
}

// Inside a loop the order argument fails: the back edge carries a write
// that sits LATER in op order round to an earlier guard.
func TestPruneZeroSlotGuardsRefusesInLoop(t *testing.T) {
	fn := zeroSlotFunc(0, 3, nil, []Op{{Kind: OpLoop}})
	// the write is after the guard in op order, reachable via the back edge
	fn.Ops = append(fn.Ops, Op{Kind: OpConstI32, I32: 42}, Op{Kind: OpStoreLocal, I32: 3})
	p := &Program{Funcs: []*Func{fn}}
	if PruneZeroSlotGuards(p) {
		t.Error("a guard inside a loop must not be pruned")
	}
}
