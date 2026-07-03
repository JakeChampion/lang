package ir

import (
	"strings"
	"testing"
)

// #4399 sink 1 — Array_push is a counted store: emitArrayPush inc's an
// aliased pointer-shaped element (needsRcIncOnAlias) and the buffer's deep
// drop decs it, so computeFreeEligible's push arm weakened from full escape
// (taint the projection chain's root) to escapeOwned (direct Ident only).
// This pins the verdict at the op level: the PROJECTION SOURCE of a pushed
// element is free-eligible, so its exit-sweep dec routes through a deep-
// dropping __drop_arr_* walk instead of the never-freeing plain dec.
func TestArrayPushProjectionSourceFreeEligible(t *testing.T) {
	p := lowerSource(t, `function work(k: i32): i32 {
    var src: i32[][] = [[k, k + 1], [k + 2]];
    var out: i32[][] = [];
    out = out.append(src[0]);
    var e: i32[] = out[0];
    return e[0] + e[1];
}`)
	var work *Func
	for _, f := range p.Funcs {
		if f.Name == "work" {
			work = f
		}
	}
	if work == nil {
		t.Fatalf("work not lowered")
	}
	drops := 0
	for _, op := range work.Ops {
		if op.Kind == OpCallDirect && strings.HasPrefix(op.Str, "__drop_arr_") {
			drops++
		}
	}
	// Two owned ptr-element arrays sweep deeply: `out` (always was) and
	// `src` (the point of #4399 sink 1 — tainted to a plain, never-freeing
	// dec before the counted-store migration).
	if drops != 2 {
		t.Errorf("want 2 deep array drops (src + out), got %d — the push-projection source must stay free-eligible:\n%s", drops, p)
	}
	// A direct-Ident element keeps the taint (escapeOwned): its moveSites
	// shape transfers instead of inc'ing — same rule as StructLit fields.
	p2 := lowerSource(t, `function keep(k: i32): i32 {
    var row: i32[] = [k, k + 1];
    var out: i32[][] = [];
    out = out.append(row);
    var e: i32[] = out[0];
    return e[0];
}`)
	var keep *Func
	for _, f := range p2.Funcs {
		if f.Name == "keep" {
			keep = f
		}
	}
	if keep == nil {
		t.Fatalf("keep not lowered")
	}
	deep := 0
	for _, op := range keep.Ops {
		if op.Kind == OpCallDirect && strings.HasPrefix(op.Str, "__drop_arr_") {
			deep++
		}
	}
	// Direct-ident element: row stays tainted (escapeOwned), and `out` is
	// tainted transitively — rhsTainted sees the append call carrying the
	// tainted row — so NEITHER array deep-drops. The conservative
	// direct-ident baseline is unchanged by the sink migration.
	if deep != 0 {
		t.Errorf("want 0 deep array drops (direct-ident element keeps row and, transitively, out tainted), got %d:\n%s", deep, p2)
	}
}
