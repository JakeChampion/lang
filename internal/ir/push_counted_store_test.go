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

// #4399 sink 2 — Array_set (`.with` / `.set`) is a counted store for
// POINTER-SHAPED elements only: emitArraySet inc's an aliased element,
// drops the overwritten one, and the CoW copy retains — so a projection
// source reclaims. STRING elements are excluded from that machinery (no
// inc, no old-element drop), so their taint arm must survive unchanged.
func TestArraySetProjectionSourceFreeEligible(t *testing.T) {
	p := lowerSource(t, `function work(k: i32): i32 {
    var src: i32[][] = [[k, k + 1], [k + 2]];
    var out: i32[][] = [[k]];
    out = out.with(0, src[0]);
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
	// 4 vs the tainted baseline's 2: src joins the deep-drop set at both
	// its exit sweep and the .with site's overwrite/reinit drops.
	if drops != 4 {
		t.Errorf("want 4 deep array drops (src free-eligible; baseline was 2), got %d:\n%s", drops, p)
	}
	// String elements stay on the full-escape taint: emitArraySet's
	// counted-store machinery excludes them, so the source container must
	// keep its never-freeing plain dec.
	p2 := lowerSource(t, `function strs(): i32 {
    var src: string[][] = [["a"], ["b"]];
    var out: string[][] = [["c"]];
    out = out.with(0, src[0]);
    return out[0].len();
}`)
	var strs *Func
	for _, f := range p2.Funcs {
		if f.Name == "strs" {
			strs = f
		}
	}
	if strs == nil {
		t.Fatalf("strs not lowered")
	}
	for _, op := range strs.Ops {
		if op.Kind == OpCallDirect && strings.HasPrefix(op.Str, "__fern_drop_arr_str") {
			// A deep string-array drop of src here would over-release: the
			// .with store never inc'd the element.
			strDeep := 0
			for _, o := range strs.Ops {
				if o.Kind == OpCallDirect && strings.HasPrefix(o.Str, "__fern_drop_arr_str") {
					strDeep++
				}
			}
			if strDeep > 1 {
				t.Errorf("string[][] .with source must stay tainted (max 1 deep drop for out), got %d:\n%s", strDeep, p2)
			}
			break
		}
	}
}

// #4399 sink 4a — if-expr yields are counted stores for the
// needsRcIncOnAlias shapes: each arm incs an aliased pointer-shaped yield
// (fresh arm values move out uninc'd), so the source locals stay
// reclaimable and only slice-view yields keep the escape taint.
func TestIfExprYieldSourceFreeEligible(t *testing.T) {
	p := lowerSource(t, `function pick(c: boolean, k: i32): i32 {
    var a: i32[][] = [[k]];
    var b2: i32[][] = [[k + 1]];
    var v: i32[][] = if (c) { a } else { b2 };
    return v[0][0];
}`)
	var pick *Func
	for _, f := range p.Funcs {
		if f.Name == "pick" {
			pick = f
		}
	}
	if pick == nil {
		t.Fatalf("pick not lowered")
	}
	drops, incs := 0, 0
	for _, op := range pick.Ops {
		if op.Kind == OpCallDirect && strings.HasPrefix(op.Str, "__drop_arr_") {
			drops++
		}
		if op.Kind == OpCallDirect && op.Str == "__fern_rc_inc" {
			incs++
		}
	}
	// a, b2, and v all become reclaimable (tainted baseline: 0 deep drops,
	// 0 incs). The 6 = each local's release appears at both its
	// precise/overwrite site and the exit-sweep path; the e2e balance
	// tests pin that the is_unique-gated releases stay exact at runtime.
	if drops != 6 {
		t.Errorf("want 6 deep array drops (a + b2 + v, tainted baseline 0), got %d:\n%s", drops, p)
	}
	if incs < 2 {
		t.Errorf("want >= 2 arm alias incs, got %d:\n%s", incs, p)
	}
}
