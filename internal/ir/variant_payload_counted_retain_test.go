package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// A FRESH call-result temp handed to a BORROWED parameter that the callee
// stores into a variant-constructor payload was never released: emitEnumNew
// inc's the payload (rc 2), and the caller's post-call release keys on
// paramCountedRetain, which had no rule crediting a variant-constructor store.
// `wrap(ins(l, k), k, r)` stranded the `ins(l, k)` node on every tree insert;
// the same for a heap string and an array temp. inferParamCountedRetain now
// credits a `Ctor(.., p, ..)` payload under exactly emitEnumNew's inc gate
// (variantCtorCountedIn), so a credit is never granted where no inc is emitted.
//
// `wrap` is also taken as a function value (`hold(wrap)`): a function reached
// through a function value borrows unconditionally (#7307), which keeps its
// params on the borrow model now that a string-carrying uniform enum is owned
// by default — the temp release under test is the BORROWED-position one.
const variantPayloadSrc = `enum T { Leaf, Node(T, string, T) }
enum A { ALeaf, ANode(A, i32[], A) }
enum M { MLeaf, MNode(M, Map[i32, i32]) }
@noinline
function mk(i: i32): T { return Node(Leaf, "k", Leaf); }
@noinline
function wrap(l: T, k: string, r: T): T { return Node(l, k, r); }
@noinline
function wrapa(l: A, xs: i32[], r: A): A { return ANode(l, xs, r); }
function scrut(l: T, k: string, r: T): T {
    match (l) {
        Leaf => { return Node(l, k, r); },
        Node(a, b, c) => { return Node(l, k, r); },
    }
}
function mapped(l: M, m: Map[i32, i32]): M { return MNode(l, m); }
function reuse(own s: T, p: T): T {
    match (s) {
        Node(a, k, b) => { return Node(p, k, b); },
        Leaf => { return Leaf; },
    }
}
function single(k: string): T { return Node(Leaf, k, Leaf); }
function round(i: i32): i32 {
    var t: T = wrap(mk(i), "k", Leaf);
    match (t) { Node(l, k, r) => { return k.len(); }, Leaf => { return 0; } }
}
function hold(f: (T, string, T) => T): i32 { return 0; }
function main(): i32 { return round(1) + hold(wrap); }`

func TestVariantPayloadStoreIsCountedRetain(t *testing.T) {
	prog, err := parser.Parse(variantPayloadSrc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	got := inferParamCountedRetain(prog, info)
	want := map[string][]bool{
		// Every pointer-shaped param is stored only as a payload: enum, string, array.
		"wrap":  {true, true, true},
		"wrapa": {true, true, true},
		// A match SCRUTINEE is a non-retaining read whose bindings are held
		// to the same rules; here they are unused, and l is then stored
		// only as a payload.
		"scrut": {true, true, true},
		// A Map-carrying enum stores its payloads UNCOUNTED (not
		// enumRcPayloadsEligible): no inc, so no credit.
		"mapped": {false, false},
		// The sole `return Ctor(..)` of an arm matching an `own` enum param is
		// a consuming-match reuse site, which stores without the inc.
		"reuse": {false, false},
	}
	for fn, w := range want {
		g := got[fn]
		if len(g) != len(w) {
			t.Errorf("paramCountedRetain[%s] = %v, want %v", fn, g, w)
			continue
		}
		for i := range w {
			if g[i] != w[i] {
				t.Errorf("paramCountedRetain[%s][%d] = %v, want %v (%v)", fn, i, g[i], w[i], g)
			}
		}
	}
	// A variant construction is a fresh rc=1 box: the summary that lets a
	// caller's `var nl = ins(l, k)` binding stay reclaimable.
	fresh := findReturnsFreshBox(prog, info, map[string]bool{}, map[string]bool{}, noOwnedParams)
	if !fresh["single"] || !fresh["wrap"] {
		t.Errorf("returnsFreshBox: single=%v wrap=%v, want both true — a variant construction of an rc-payload enum is the callee's own box", fresh["single"], fresh["wrap"])
	}
	if fresh["mapped"] {
		t.Error("returnsFreshBox[mapped] = true, want false — a Map-carrying enum's construction stores its payloads uncounted")
	}
}

// The op-level contract on both sides of the call: the callee inc's each
// payload (the retain the credit rests on), and the caller releases the temp it
// stashed for the `mk(i)` argument once `wrap` has returned — the release that
// was missing.
func TestVariantPayloadArgTempIsReleased(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		prog := lowerSourceWith(t, variantPayloadSrc, ptrW)
		wrap := funcNamed(prog, "wrap")
		incs := 0
		for _, op := range wrap.Ops {
			if op.Kind == OpRcInc || (op.Kind == OpCallDirect && op.Str == "__fern_str_inc") {
				incs++
			}
		}
		if incs != 3 {
			t.Errorf("ptrW=%d: wrap emits %d payload retains, want 3 — the counted-retain credit for a variant payload rests on emitEnumNew's inc", ptrW, incs)
		}
		round := funcNamed(prog, "round")
		// The `mk(i)` result is stashed in a slot right after the call; that
		// slot must be loaded and dropped after `wrap` returns.
		stash := int32(-1)
		wrapAt := -1
		for i, op := range round.Ops {
			if op.Kind == OpCallDirect && op.Str == "mk" && i+1 < len(round.Ops) && round.Ops[i+1].Kind == OpStoreLocal {
				stash = round.Ops[i+1].I32
			}
			if op.Kind == OpCallDirect && op.Str == "wrap" {
				wrapAt = i
			}
		}
		if stash < 0 || wrapAt < 0 {
			t.Fatalf("ptrW=%d: round does not stash the mk(i) temp (stash=%d, wrap at %d):\n%s", ptrW, stash, wrapAt, prog)
		}
		released := false
		for i := wrapAt; i+1 < len(round.Ops); i++ {
			if round.Ops[i].Kind == OpLoadLocal && round.Ops[i].I32 == stash &&
				round.Ops[i+1].Kind == OpCallDirect && round.Ops[i+1].Str == "__drop_enum_T" {
				released = true
			}
		}
		if !released {
			t.Errorf("ptrW=%d: round never drops the mk(i) temp after wrap returns — the payload store inc'd it to rc 2 and nothing spends the caller's reference", ptrW)
		}
	}
}
