package ir

import "testing"

// A non-adjacent store / load with no other slot use: ConstPropagate
// rewrites the load to the stored constant. The intervening op
// (here, a print call) keeps FuseTee from collapsing the pair.
func TestConstPropReplacesNonAdjacentLoad(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpConstI32, I32: 7},
			{Kind: OpStoreLocal, I32: 0},
			{Kind: OpConstStr, Str: "hi"},
			{Kind: OpCallDirect, Str: "print", I32: 1},
			{Kind: OpLoadLocal, I32: 0}, // <-- should become const 7
			{Kind: OpReturn},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	ConstPropagate(p)
	// The load must have been rewritten to a const.
	rewritten := false
	for _, op := range fn.Ops {
		if op.Kind == OpLoadLocal {
			t.Fatalf("expected OpLoadLocal to be replaced, still present:\n%s", p)
		}
	}
	for _, op := range fn.Ops {
		if op.Kind == OpConstI32 && op.I32 == 7 {
			rewritten = true
		}
	}
	if !rewritten {
		t.Errorf("expected propagated `const.i32 7`:\n%s", p)
	}
}

// A control-flow op (block / if / loop / br …) clears the
// constant table — values flowing through different predecessors
// can't be assumed equal. The load survives untouched.
func TestConstPropInvalidatesAcrossControlFlow(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpConstI32, I32: 7},
			{Kind: OpStoreLocal, I32: 0},
			{Kind: OpBlock, I32: BlockTypeVoid},
			{Kind: OpEnd},
			{Kind: OpLoadLocal, I32: 0}, // not a known const after the block
			{Kind: OpReturn},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	ConstPropagate(p)
	hasLoad := false
	for _, op := range fn.Ops {
		if op.Kind == OpLoadLocal {
			hasLoad = true
		}
	}
	if !hasLoad {
		t.Errorf("load after a block should not be propagated:\n%s", p)
	}
}

// Tee binds the slot to a constant when the previous op is itself
// a const. Subsequent loads of that slot get the constant inlined.
func TestConstPropTracksAcrossTee(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpConstI32, I32: 11},
			{Kind: OpTeeLocal, I32: 0},  // slot 0 = 11, also leaves 11 on stack
			{Kind: OpDrop},              // discard the tee's pushed value
			{Kind: OpLoadLocal, I32: 0}, // should become const 11
			{Kind: OpReturn},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	ConstPropagate(p)
	for _, op := range fn.Ops {
		if op.Kind == OpLoadLocal {
			t.Fatalf("expected the load to be propagated:\n%s", p)
		}
	}
}

// A non-constant value (the result of an arithmetic op) sitting
// just before the store leaves the slot opaque — later loads
// stay as OpLoadLocal.
func TestConstPropSkipsNonConstStore(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpConstI32, I32: 1},
			{Kind: OpConstI32, I32: 2},
			{Kind: OpAdd}, // produces a runtime-only value
			{Kind: OpStoreLocal, I32: 0},
			{Kind: OpLoadLocal, I32: 0},
			{Kind: OpReturn},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	ConstPropagate(p)
	hasLoad := false
	for _, op := range fn.Ops {
		if op.Kind == OpLoadLocal {
			hasLoad = true
		}
	}
	if !hasLoad {
		t.Errorf("non-constant store must leave the load alone:\n%s", p)
	}
}

// End-to-end through the full optimisation pipeline: a `var x = 7;
// return x + 3;` style body collapses to a single `const.i32 10` —
// the store / load pair fuses to a tee, PropagateCopies drops the
// dead tee, and Fold collapses the resulting `const 7 ; const 3 ;
// add`. ConstPropagate isn't strictly required for this case, but
// the test guards against pipeline regression.
func TestConstPropEnablesEndToEndCollapse(t *testing.T) {
	p := lowerSource(t, `function f(): i32 {
		var x: i32 = 7;
		return x + 3;
	}`)
	Inline(p)
	FuseTee(p)
	PropagateCopies(p)
	ConstPropagate(p)
	Fold(p)
	PropagateCopies(p)
	fn := findFunc(p, "f")
	found := false
	for _, op := range fn.Ops {
		if op.Kind == OpConstI32 && op.I32 == 10 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected `const.i32 10` after the full pipeline:\n%s", p)
	}
}

// Multi-load case: a slot bound to a constant and read twice gets
// each load rewritten to the constant. The cleanup driver iterates
// PropagateCopies / ConstPropagate / Fold to a fixed point so the
// cascade — dead tee dropped, constants now adjacent, fold
// collapses — settles in one pipeline call.
func TestConstPropMultiLoadCollapses(t *testing.T) {
	p := lowerSource(t, `function f(): i32 {
		var x: i32 = 5;
		return x + x;
	}`)
	Inline(p)
	FuseTee(p)
	OptimizeCleanup(p)
	fn := findFunc(p, "f")
	hasLoad := false
	hasResultConst := false
	for _, op := range fn.Ops {
		if op.Kind == OpLoadLocal {
			hasLoad = true
		}
		if op.Kind == OpConstI32 && op.I32 == 10 {
			hasResultConst = true
		}
	}
	if hasLoad {
		t.Errorf("expected every load of x to be propagated:\n%s", p)
	}
	if !hasResultConst {
		t.Errorf("expected `const.i32 10` (folded `5 + 5`):\n%s", p)
	}
}

// i64 constants flow through locals the same way i32 constants
// do. The fold pipeline collapses `var x: i64 = 7i64; return x +
// 3i64` to a single OpConstI64 10 via the same tee + propagate +
// fold dance.
func TestConstPropTracksI64(t *testing.T) {
	p := lowerSource(t, `function f(): i64 {
		var x: i64 = 7i64;
		return x + 3i64;
	}`)
	Inline(p)
	FuseTee(p)
	OptimizeCleanup(p)
	fn := findFunc(p, "f")
	found := false
	for _, op := range fn.Ops {
		if op.Kind == OpConstI64 && op.I64 == 10 {
			found = true
		}
		if op.Kind == OpLoadLocal {
			t.Errorf("expected i64 load of x to be propagated:\n%s", p)
		}
	}
	if !found {
		t.Errorf("expected `const.i64 10` after the i64 pipeline:\n%s", p)
	}
}

// const + drop pair (left over after dead-store rewriting) gets
// folded away — the const has no side effects and the drop just
// cleans up after it.
func TestFoldRemovesConstDropPair(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpConstI32, I32: 99},
			{Kind: OpDrop},
			{Kind: OpReturnVoid},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	Fold(p)
	if len(fn.Ops) != 1 || fn.Ops[0].Kind != OpReturnVoid {
		t.Errorf("expected only ReturnVoid after const+drop fold, got:\n%s", p)
	}
}

// Idempotence: a second pass produces identical output.
func TestConstPropIsIdempotent(t *testing.T) {
	p := lowerSource(t, `function f(): i32 {
		var x: i32 = 7;
		return x + 3;
	}`)
	Inline(p)
	FuseTee(p)
	PropagateCopies(p)
	ConstPropagate(p)
	before := p.String()
	ConstPropagate(p)
	after := p.String()
	if before != after {
		t.Errorf("ConstPropagate not idempotent:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
