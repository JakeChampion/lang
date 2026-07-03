package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// #4476: every inline enum slot-drop in a function shares ONE tag-stash
// scratch slot. Two return sites each sweep the non-uniform enum local `b`
// through emitEnumSlotDrop's variant-plan tier (the tag-switch that reclaims
// a `Box{ Val(i32), Empty }` box), so the op stream carries two tag-stash
// stores — the pin is that both target the SAME slot, where the old
// per-invocation allocation minted a fresh slot per sweep. This is the
// precondition for converting the exit dec sweep to post-lowering insertion
// (no more scratch allocation interleaved with body lowering).
func TestEnumSlotDropSharesTagStash(t *testing.T) {
	ip := lowerForTest(t, `enum Box { Val(i32), Empty }
function f(c: i32): i32 {
	var b: Box = Val(c);
	if (c > 0) { return 1; }
	return 0;
}
function main(): i32 { return f(1); }`)
	f := funcByName(ip, "f")
	if f == nil {
		t.Fatal("f not lowered")
	}
	// Tag-stash signature: OpLoadLocal <enum slot>; OpLoad (tag at [data+0]);
	// OpStoreLocal <stash>. The stash is a scratch slot (>= params+locals).
	scratchBase := int32(len(f.Params) + len(f.Locals))
	targets := map[int32]int{}
	for i := 2; i < len(f.Ops); i++ {
		if f.Ops[i-2].Kind == ir.OpLoadLocal && f.Ops[i-1].Kind == ir.OpLoad &&
			f.Ops[i].Kind == ir.OpStoreLocal && f.Ops[i].I32 >= scratchBase {
			targets[f.Ops[i].I32]++
		}
	}
	total := 0
	for _, n := range targets {
		total += n
	}
	if total < 2 {
		t.Fatalf("expected >=2 tag-stash stores (two swept returns), got %d (targets %v)", total, targets)
	}
	if len(targets) != 1 {
		t.Errorf("tag stash not shared: %d distinct stash slots %v, want 1", len(targets), targets)
	}
}
