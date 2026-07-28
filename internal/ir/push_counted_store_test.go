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
	//
	// Raised 2 -> 4 when push joined Array_set as a receiver-only alias in
	// rhsTainted: `out`'s own buffer now deep-drops on both the reassignment
	// overwrite and the exit sweep instead of falling through to a flat,
	// never-freeing rc_dec. Verified at runtime, not just in op counts —
	// this shape still exits 3 (matching `fern -interp`) and frees rise
	// 600 -> 800 of 1000 allocs, live_bytes 16000 -> 6400. Strictly more
	// reclamation, same answer.
	if drops != 4 {
		t.Errorf("want 4 deep array drops (src + out, each overwrite + exit), got %d — the push-projection source must stay free-eligible:\n%s", drops, p)
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
	// Direct-ident element: `row` stays tainted (escapeOwned, since the
	// moveSites shape transfers instead of inc'ing). `out` no longer inherits
	// that taint — push is a receiver-only alias in rhsTainted, so the
	// element's taint reaches `row` alone, which is what escapeOwned is for.
	// `out` therefore deep-drops, freeing the moved-in element exactly once;
	// `row` stays tainted and is never swept, so there is no double free.
	// Confirmed at runtime: this shape exits 3 (matching `fern -interp`) with
	// frees 200 -> 400 of 600 allocs and live_bytes 16000 -> 6400.
	if deep != 2 {
		t.Errorf("want 2 deep array drops (out reclaims; row stays tainted so it is never swept), got %d:\n%s", deep, p2)
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
		if op.Kind == OpRcInc {
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

// #4402 opt 1 — dead-alias dup/drop cancellation: `var y = x` with both
// proven pure (x never reassigned/moved/matched-on, y never
// reassigned/returned) elides y's transfer inc AND y's exit-sweep dec as a
// net-zero pair; x stays exit-sweep-released and precise-drop/reuse-donor
// excluded.
func TestDeadAliasPairCancelled(t *testing.T) {
	p := lowerSource(t, `function work(k: i32): i32 {
    var x: i32[][] = [[k, k + 1]];
    var y: i32[][] = x;
    var e: i32[] = y[0];
    return e[0] + x[0][1];
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
	incs, deepDrops := 0, 0
	for _, op := range work.Ops {
		if op.Kind == OpRcInc {
			incs++
		}
		if op.Kind == OpCallDirect && strings.HasPrefix(op.Str, "__drop_arr_") {
			deepDrops++
		}
	}
	// One inc only (e = y[0], the element alias) — y's transfer inc is
	// cancelled. Two deep-drop CALL SITES, both x's (its decl-site
	// reinit drop — a null-guarded static no-op — and its exit sweep);
	// y contributes neither a reinit drop nor a sweep dec. The tainted
	// baseline emits 2 incs and 4 deep-drop sites.
	if incs != 1 {
		t.Errorf("want 1 rc_inc (element alias only; y's transfer inc cancelled), got %d:\n%s", incs, p)
	}
	if deepDrops != 2 {
		t.Errorf("want 2 deep-drop sites (x reinit + x sweep; y is a borrowed view), got %d:\n%s", deepDrops, p)
	}
	// Reassigned alias: cancellation must NOT fire (y is rebound), and
	// since x is still used after `var y = x`, the move machinery doesn't
	// claim the site either — y keeps its ordinary transfer inc.
	p2 := lowerSource(t, `function keep(k: i32): i32 {
    var x: i32[][] = [[k]];
    var y: i32[][] = x;
    y = [[k + 1]];
    return x[0][0] + y[0][0];
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
	kIncs := 0
	for _, op := range keep.Ops {
		if op.Kind == OpRcInc {
			kIncs++
		}
	}
	if kIncs < 1 {
		t.Errorf("reassigned alias must keep its transfer inc, got %d incs:\n%s", kIncs, p2)
	}
	// (A returned alias needs no assertion here: `var y = x; return y`
	// is claimed by move-on-alias + move-on-return — zero incs, zero
	// sweeps, fully transferred — and the borrowed-alias gates exclude
	// returned names anyway.)
}
