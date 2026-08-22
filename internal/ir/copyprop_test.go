package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// A tee whose slot is never read elsewhere is just `pop ; push`
// from the operand stack's POV — the propagation pass drops it.
func TestPropagateCopiesDropsDeadTee(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpConstI32, I32: 7},
			{Kind: OpTeeLocal, I32: 0}, // slot 0 has no other reads
			{Kind: OpConstI32, I32: 2},
			{Kind: OpMul},
			{Kind: OpReturn},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	PropagateCopies(p)
	for _, op := range fn.Ops {
		if op.Kind == OpTeeLocal {
			t.Fatalf("dead tee should have been dropped:\n%s", p)
		}
	}
}

// A tee on a slot that *is* read elsewhere stays — dropping it
// would leave the later load reading garbage.
func TestPropagateCopiesKeepsLiveTee(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpConstI32, I32: 5},
			{Kind: OpTeeLocal, I32: 0},
			{Kind: OpDrop},              // consume the tee's pushed value so the stack is balanced for the load
			{Kind: OpLoadLocal, I32: 0}, // later read of slot 0
			{Kind: OpReturn},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	PropagateCopies(p)
	hasTee := false
	for _, op := range fn.Ops {
		if op.Kind == OpTeeLocal {
			hasTee = true
		}
	}
	if !hasTee {
		t.Errorf("live tee must survive — slot has a later load:\n%s", p)
	}
}

// A dead OpStoreLocal (slot has no reads) becomes OpDrop. The
// store's operand still has to be consumed, so we can't just delete
// the op outright.
func TestPropagateCopiesReplacesDeadStoreWithDrop(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpConstI32, I32: 9},
			{Kind: OpStoreLocal, I32: 0}, // slot 0 never read
			{Kind: OpReturnVoid},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	PropagateCopies(p)
	hasDrop := false
	hasStore := false
	for _, op := range fn.Ops {
		if op.Kind == OpDrop {
			hasDrop = true
		}
		if op.Kind == OpStoreLocal {
			hasStore = true
		}
	}
	if !hasDrop {
		t.Errorf("dead store should have become OpDrop:\n%s", p)
	}
	if hasStore {
		t.Errorf("dead OpStoreLocal must be replaced, not kept:\n%s", p)
	}
}

// End-to-end check on the inliner's classic shape: `dbl(7)` lowers,
// inlines, fuses, and copy-propagates to a single `const 7 ; const
// 2 ; mul`. Once Fold runs the whole thing collapses to const 14.
func TestPropagateCopiesEnablesFoldToCollapseInlinedCall(t *testing.T) {
	p := lowerSource(t, `function dbl(x: i32): i32 { return x * 2; }
		function main(): i32 { return dbl(7); }`)
	Inline(p)
	FuseTee(p)
	PropagateCopies(p)
	Fold(p)
	main := findFunc(p, "main")
	found14 := false
	for _, op := range main.Ops {
		if op.Kind == OpConstI32 && op.I32 == 14 {
			found14 = true
		}
	}
	if !found14 {
		t.Errorf("expected `const.i32 14` after the full pipeline:\n%s", p)
	}
}

// The pass leaves Switch / ArrayLit / StructLit base-pointer
// scratches alone — those slots are read multiple times during
// helper expansion, so the read counts protect them.
func TestPropagateCopiesPreservesHelperSlots(t *testing.T) {
	p := lowerSource(t, `function f(): i32 {
		var a: i32[] = [10, 20, 30];
		return a[1];
	}`)
	PropagateCopies(p)
	// At least one OpStoreLocal (or OpTeeLocal) for the array
	// base and at least one OpLoadLocal of it must remain.
	fn := p.Funcs[0]
	stores := 0
	loads := 0
	for _, op := range fn.Ops {
		if op.Kind == OpStoreLocal || op.Kind == OpTeeLocal {
			stores++
		}
		if op.Kind == OpLoadLocal {
			loads++
		}
	}
	if stores == 0 || loads == 0 {
		t.Errorf("helper slot ops should have survived: stores=%d loads=%d:\n%s", stores, loads, p)
	}
}

// Idempotence: running the pass twice produces the same op list.
func TestPropagateCopiesIsIdempotent(t *testing.T) {
	p := lowerSource(t, `function dbl(x: i32): i32 { return x * 2; }
		function main(): i32 { return dbl(7); }`)
	Inline(p)
	FuseTee(p)
	PropagateCopies(p)
	before := p.String()
	PropagateCopies(p)
	after := p.String()
	if before != after {
		t.Errorf("PropagateCopies not idempotent:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// A dead store to a `dyn Trait` slot must be rewritten to a WIDE drop
// (Width: WidthString — the wasm codegen fans it out to two `drop`s),
// exactly like a dead two-word string store: the store consumed two
// operand values on wasm32, so a one-value drop would leave the stack
// imbalanced. Pinned at the pass layer; see slotIsTwoWord's dyn arm.
func TestPropagateCopiesDeadDynStoreDropsBothWords(t *testing.T) {
	fn := &Func{
		Name:   "f",
		Locals: []*ast.Var{{Name: "d", Type: ast.DynTraitType{Traits: []string{"Show"}}}},
		Ops: []Op{
			{Kind: OpConstI32, I32: 0},
			{Kind: OpConstI32, I32: 0},
			{Kind: OpStoreLocal, I32: 0}, // dead: slot 0 is never read
			{Kind: OpReturnVoid},
		},
	}
	p := &Program{Funcs: []*Func{fn}, PtrW: 4}
	PropagateCopies(p)
	for _, op := range fn.Ops {
		if op.Kind == OpStoreLocal {
			return // store kept: also fine (no imbalance introduced)
		}
	}
	for _, op := range fn.Ops {
		if op.Kind == OpDrop {
			if op.Width != WidthString {
				t.Errorf("dead dyn-slot store rewrote to a one-word drop (Width=%d) — wasm32 stack imbalance:\n%s", op.Width, p)
			}
			return
		}
	}
	t.Errorf("expected either the store or a wide drop to remain:\n%s", p)
}

// The two-word string ABI is not "wasm32 only": `ast.TwoWordOverride`
// opts arm64 (ptrW 8) into it, and there a dead store to a string slot
// consumed two operand values. Rewriting it to a one-word drop leaves
// half the value parked on the machine stack — #7303, where the arm64
// emitter pushed the low word of a match arm's unread `string` binding
// and only the epilogue's `mov sp, x29` swept it up.
func TestPropagateCopiesDeadStringStoreDropsBothWordsOnArm64(t *testing.T) {
	newFn := func() *Func {
		return &Func{
			Name:   "f",
			Locals: []*ast.Var{{Name: "s", Type: ast.StringType{}}},
			Ops: []Op{
				{Kind: OpConstI32, I32: 0},
				{Kind: OpConstI32, I32: 0},
				{Kind: OpStoreLocal, I32: 0}, // dead: slot 0 is never read
				{Kind: OpReturnVoid},
			},
		}
	}
	dropWidth := func(t *testing.T, p *Program) (int, bool) {
		t.Helper()
		for _, op := range p.Funcs[0].Ops {
			if op.Kind == OpDrop {
				return op.Width, true
			}
		}
		return 0, false
	}

	prev := ast.TwoWordOverride
	defer func() { ast.TwoWordOverride = prev }()

	ast.TwoWordOverride = true
	two := &Program{Funcs: []*Func{newFn()}, PtrW: 8}
	PropagateCopies(two)
	w, ok := dropWidth(t, two)
	if !ok {
		t.Fatalf("dead string store should have become OpDrop:\n%s", two)
	}
	if w != WidthString {
		t.Errorf("two-word target rewrote the dead string store to a one-word drop (Width=%d) — half the operand stays on the stack:\n%s", w, two)
	}

	ast.TwoWordOverride = false
	one := &Program{Funcs: []*Func{newFn()}, PtrW: 8}
	PropagateCopies(one)
	w, ok = dropWidth(t, one)
	if !ok {
		t.Fatalf("dead string store should have become OpDrop:\n%s", one)
	}
	if w == WidthString {
		t.Errorf("one-word-string target rewrote the dead string store to a two-word drop — it would pop an operand that was never pushed:\n%s", one)
	}
}
