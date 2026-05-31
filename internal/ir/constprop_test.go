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

// A slot bound to a constant before an empty block survives the
// block — no write inside, no branch out, no merge with a
// differently-valued predecessor. The load after the block folds.
func TestConstPropFlowsThroughEmptyBlock(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpConstI32, I32: 7},
			{Kind: OpStoreLocal, I32: 0},
			{Kind: OpBlock, I32: BlockTypeVoid},
			{Kind: OpEnd},
			{Kind: OpLoadLocal, I32: 0}, // should become const 7
			{Kind: OpReturn},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	ConstPropagate(p)
	for _, op := range fn.Ops {
		if op.Kind == OpLoadLocal {
			t.Fatalf("load of slot 0 should be propagated past the empty block:\n%s", p)
		}
	}
}

// A write inside a block invalidates the pre-block binding at the
// block's exit — the post-block load can't assume the entry value.
func TestConstPropInvalidatesAfterIntraBlockWrite(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpConstI32, I32: 7},
			{Kind: OpStoreLocal, I32: 0},
			{Kind: OpBlock, I32: BlockTypeVoid},
			{Kind: OpConstI32, I32: 9},
			{Kind: OpStoreLocal, I32: 0},
			{Kind: OpEnd},
			{Kind: OpLoadLocal, I32: 0}, // const 9 (rewritten by tracker)
			{Kind: OpReturn},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	ConstPropagate(p)
	// The post-block load should now be `const.i32 9` — the in-block
	// write rebinds the slot for the straight-line path.
	for _, op := range fn.Ops {
		if op.Kind == OpLoadLocal {
			t.Fatalf("intra-block write should rebind the slot:\n%s", p)
		}
	}
	var sawNine bool
	for _, op := range fn.Ops {
		if op.Kind == OpConstI32 && op.I32 == 9 {
			sawNine = true
		}
	}
	if !sawNine {
		t.Errorf("expected `const.i32 9` after the block:\n%s", p)
	}
}

// A loop body that writes a slot makes the slot's binding opaque
// after the loop: the body may have executed zero or more times,
// so neither the entry constant nor the in-body constant is
// guaranteed at the loop exit.
func TestConstPropInvalidatesAfterLoopWrite(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpConstI32, I32: 7},
			{Kind: OpStoreLocal, I32: 0},
			{Kind: OpLoop, I32: BlockTypeVoid},
			{Kind: OpConstI32, I32: 9},
			{Kind: OpStoreLocal, I32: 0},
			{Kind: OpEnd},
			{Kind: OpLoadLocal, I32: 0}, // must stay as a load
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
		t.Errorf("loop-written slot must not be propagated past the loop:\n%s", p)
	}
}

// Both arms of an if-else write the same constant to a slot — the
// slot has that value after the if regardless of which arm ran.
func TestConstPropMergesAgreeingArms(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpConstI32, I32: 1}, // condition
			{Kind: OpIf, I32: BlockTypeVoid},
			{Kind: OpConstI32, I32: 5},
			{Kind: OpStoreLocal, I32: 0},
			{Kind: OpElse},
			{Kind: OpConstI32, I32: 5},
			{Kind: OpStoreLocal, I32: 0},
			{Kind: OpEnd},
			{Kind: OpLoadLocal, I32: 0}, // both arms write 5 → const 5
			{Kind: OpReturn},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	ConstPropagate(p)
	for _, op := range fn.Ops {
		if op.Kind == OpLoadLocal {
			t.Fatalf("agreeing if-arms should merge to a known constant:\n%s", p)
		}
	}
}

// Disagreeing arms drop the binding at the merge point.
func TestConstPropDropsDisagreeingArms(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpConstI32, I32: 1}, // condition
			{Kind: OpIf, I32: BlockTypeVoid},
			{Kind: OpConstI32, I32: 5},
			{Kind: OpStoreLocal, I32: 0},
			{Kind: OpElse},
			{Kind: OpConstI32, I32: 6},
			{Kind: OpStoreLocal, I32: 0},
			{Kind: OpEnd},
			{Kind: OpLoadLocal, I32: 0}, // value unclear → load survives
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
		t.Errorf("disagreeing if-arms should leave the load:\n%s", p)
	}
}

// Constants flow INTO an if-arm: pre-if `store slot const` binds
// the slot, the load inside the arm gets substituted. The shape
// inlined parameter constants take after Inline emits its param
// binding store + the function body's wrapping OpBlock.
func TestConstPropFlowsIntoIfArm(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpConstI32, I32: 42},
			{Kind: OpStoreLocal, I32: 0},
			{Kind: OpConstI32, I32: 1}, // condition
			{Kind: OpIf, I32: BlockTypeVoid},
			{Kind: OpLoadLocal, I32: 0}, // should become const 42
			{Kind: OpDrop},
			{Kind: OpEnd},
			{Kind: OpReturnVoid},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	ConstPropagate(p)
	for _, op := range fn.Ops {
		if op.Kind == OpLoadLocal {
			t.Fatalf("pre-if constant should flow into the then-arm:\n%s", p)
		}
	}
}

// Constants also flow into the wrapper OpBlock the inliner emits
// around bodies with mid-body returns — this is the shape that
// unlocks Map specialization (inlined `keyKind=0` const survives
// past the wrapper block into the body's `if (keyKind == 0)`).
func TestConstPropFlowsIntoWrapperBlock(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpConstI32, I32: 0},
			{Kind: OpStoreLocal, I32: 0}, // simulated inlined param store
			{Kind: OpBlock, I32: BlockTypeI32},
			{Kind: OpLoadLocal, I32: 0}, // load keyKind → const 0
			{Kind: OpConstI32, I32: 0},
			{Kind: OpEq},
			{Kind: OpIf, I32: BlockTypeVoid},
			{Kind: OpConstI32, I32: 7},
			{Kind: OpBr, I32: 1}, // simulated return from then-arm
			{Kind: OpEnd},
			{Kind: OpConstI32, I32: 99}, // fall-through value
			{Kind: OpEnd},
			{Kind: OpReturn},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	ConstPropagate(p)
	for _, op := range fn.Ops {
		if op.Kind == OpLoadLocal {
			t.Fatalf("inlined-param constant should flow into wrapper block:\n%s", p)
		}
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

// End-to-end specialisation through Inline + OptimizeCleanup:
// calling a multi-branch dispatcher with a compile-time-constant
// kind argument lets ConstPropagate flow the constant past the
// inliner's wrapper block into each `kind == N` guard. Fold's
// pruneConstIf then drops every dead arm — the surviving arm's
// body is the first thing the caller sees after the wrapper open.
// This is the shape that unlocks Map specialization (calling
// __map_hash(k, 0) collapses to the i32-hash path alone).
//
// The assertion targets the linear-flow head of the inlined body:
// the surviving arm's ops must appear BEFORE any other `if (kind
// == N)` guard. (The post-arm dead code still lives in the IR
// inside an OpBlock wrap that pruneConstIf adds to preserve
// internal-`br` depths — those become unreachable and are dropped
// later by the wasm validator's structured-control-flow checks,
// not by DCE on this IR.)
func TestConstPropSpecialisesDispatchWithConstantTag(t *testing.T) {
	p := lowerSource(t, `function dispatch(k: i32, kind: i32): i32 {
		if (kind == 0) { return k * 2; }
		if (kind == 1) { return k + 100; }
		return k;
	}
	function caller(x: i32): i32 {
		return dispatch(x, 0);
	}`)
	Inline(p)
	FuseTee(p)
	OptimizeCleanup(p)
	EliminateDeadCode(p)
	caller := findFunc(p, "caller")
	if caller == nil {
		t.Fatal("caller not found")
	}
	// Walk to the first `if void` in the caller — if any survives,
	// the inliner / fold pair failed to specialise the dispatch and
	// the original guard chain is still here. Note: the wrapper
	// OpBlock and the prune-time OpBlock around the surviving arm
	// are both `block i32` / `block void` — those are EXPECTED.
	for _, op := range caller.Ops {
		if op.Kind == OpIf {
			t.Fatalf("expected all dispatch guards to be folded; found OpIf in:\n%s", p)
		}
	}
}

// Same shape as above but with the call site picking the middle
// arm (`kind == 1`) — the first if's `k * 2` body must be gone,
// the surviving body must be `k + 100`.
func TestConstPropSpecialisesDispatchSecondArm(t *testing.T) {
	p := lowerSource(t, `function dispatch(k: i32, kind: i32): i32 {
		if (kind == 0) { return k * 2; }
		if (kind == 1) { return k + 100; }
		return k;
	}
	function caller(x: i32): i32 {
		return dispatch(x, 1);
	}`)
	Inline(p)
	FuseTee(p)
	OptimizeCleanup(p)
	EliminateDeadCode(p)
	caller := findFunc(p, "caller")
	if caller == nil {
		t.Fatal("caller not found")
	}
	for _, op := range caller.Ops {
		if op.Kind == OpIf {
			t.Fatalf("expected all dispatch guards to be folded; found OpIf in:\n%s", p)
		}
	}
	var saw100 bool
	for _, op := range caller.Ops {
		if op.Kind == OpConstI32 && op.I32 == 100 {
			saw100 = true
		}
	}
	if !saw100 {
		t.Errorf("expected the surviving arm's const 100 to remain:\n%s", p)
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
