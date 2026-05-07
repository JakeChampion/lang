package ir

import "testing"

// Adjacent OpStoreLocal X / OpLoadLocal X collapses to a single
// OpTeeLocal X — the same slot index, position taken from the
// store (closer to the original "bind" line in source).
func TestFuseTeeMergesAdjacentStoreLoad(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpConstI32, I32: 7},
			{Kind: OpStoreLocal, I32: 0},
			{Kind: OpLoadLocal, I32: 0},
			{Kind: OpReturn},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	FuseTee(p)
	if len(fn.Ops) != 3 {
		t.Fatalf("expected 3 ops after fuse, got %d:\n%s", len(fn.Ops), p)
	}
	if fn.Ops[1].Kind != OpTeeLocal || fn.Ops[1].I32 != 0 {
		t.Errorf("op[1] = %s %d, want OpTeeLocal 0", fn.Ops[1].Kind, fn.Ops[1].I32)
	}
}

// Different slots are NOT fused — a store to slot 0 followed by a
// load of slot 1 stays as two ops.
func TestFuseTeeKeepsCrossSlotAdjacency(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpStoreLocal, I32: 0},
			{Kind: OpLoadLocal, I32: 1},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	FuseTee(p)
	if len(fn.Ops) != 2 {
		t.Fatalf("expected 2 ops, got %d:\n%s", len(fn.Ops), p)
	}
	if fn.Ops[0].Kind != OpStoreLocal || fn.Ops[1].Kind != OpLoadLocal {
		t.Errorf("ops should remain split, got [%s, %s]", fn.Ops[0].Kind, fn.Ops[1].Kind)
	}
}

// An intervening op breaks adjacency — the store/load pair stays
// untouched. Only fuse when nothing sits between them.
func TestFuseTeeRequiresImmediateAdjacency(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpStoreLocal, I32: 0},
			{Kind: OpConstI32, I32: 99}, // intervening
			{Kind: OpLoadLocal, I32: 0},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	FuseTee(p)
	if len(fn.Ops) != 3 {
		t.Errorf("expected fuse to be skipped (intervening op), got %d ops:\n%s", len(fn.Ops), p)
	}
}

// FuseTee runs after Inline in the production pipeline. Inlining a
// `dbl(7)` style call produces an arg-bind store followed by an
// immediate load of the same slot; FuseTee collapses those into
// `local.tee` so codegen emits one WAT op instead of two.
func TestFuseTeeAfterInlinedCall(t *testing.T) {
	p := lowerSource(t, `function dbl(x: number): number { return x * 2; }
		function main(): number { return dbl(7); }`)
	Inline(p)
	FuseTee(p)
	main := findFunc(p, "main")
	hasTee := false
	hasStoreThenLoad := false
	for i, op := range main.Ops {
		if op.Kind == OpTeeLocal {
			hasTee = true
		}
		if i+1 < len(main.Ops) &&
			op.Kind == OpStoreLocal &&
			main.Ops[i+1].Kind == OpLoadLocal &&
			op.I32 == main.Ops[i+1].I32 {
			hasStoreThenLoad = true
		}
	}
	if !hasTee {
		t.Errorf("expected an OpTeeLocal in inlined main:\n%s", p)
	}
	if hasStoreThenLoad {
		t.Errorf("FuseTee left an unfused store/load pair:\n%s", p)
	}
}

// Multiple separate adjacencies in the same function each get
// fused independently.
func TestFuseTeeHandlesMultipleSites(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpConstI32, I32: 1},
			{Kind: OpStoreLocal, I32: 0},
			{Kind: OpLoadLocal, I32: 0},
			{Kind: OpConstI32, I32: 2},
			{Kind: OpStoreLocal, I32: 1},
			{Kind: OpLoadLocal, I32: 1},
			{Kind: OpAdd},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	FuseTee(p)
	tees := 0
	for _, op := range fn.Ops {
		if op.Kind == OpTeeLocal {
			tees++
		}
	}
	if tees != 2 {
		t.Errorf("expected 2 OpTeeLocal, got %d:\n%s", tees, p)
	}
}

// FuseTee is idempotent — a second pass produces the same op list.
func TestFuseTeeIsIdempotent(t *testing.T) {
	p := lowerSource(t, `function f(): number {
		var x: number = 5;
		return x + 1;
	}`)
	FuseTee(p)
	before := p.String()
	FuseTee(p)
	after := p.String()
	if before != after {
		t.Errorf("FuseTee not idempotent:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
