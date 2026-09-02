package ir

import "testing"

// paramProjectionsSafe holds a match's payload bindings to the same rules as
// the parameter they alias, and then reads the match itself as non-retaining.
// Before that, `match (t)` refused every tree walk outright (#8057).

const projectionCreditSrc = `
enum Node { Tip, Bin(Node, i32, Node) }
struct Vec { len: i32, tail: i32[] }
struct Reg { m: Map[i32, Node] }

function depth(t: Node): i32 {
    match (t) {
        Tip => { return 0; },
        Bin(l, k, r) => { return k; }
    }
}
function rebuild(t: Node): Node {
    match (t) {
        Tip => { return Tip; },
        Bin(l, k, r) => { return Bin(l, k + 1, r); }
    }
}
function stash(t: Node, reg: Reg): i32 {
    match (t) {
        Tip => { return 0; },
        Bin(l, k, r) => { var m2: Map[i32, Node] = reg.m.insert(k, l); return m2.len(); }
    }
}
function tail_at(v: Vec, i: i32): i32 {
    if (i >= v.len) { return -1; }
    return v.tail[i];
}
function call_it(f: (i32) => i32, x: i32): i32 { return f(x); }
function main(): i32 {
    var m: Map[i32, Node] = map_new(4);
    var reg: Reg = Reg { m: m };
    var t: Node = Bin(Tip, 1, Tip);
    return depth(t) + depth(rebuild(t)) + stash(t, reg) + tail_at(Vec { len: 1, tail: [7] }, 0) +
        call_it((n: i32) => n + 1, 2);
}`

func TestMatchBindingsAreProjectionsOfTheParam(t *testing.T) {
	cases := map[string][]bool{
		// The match reads t; k is a scalar; l and r are unused.
		"depth": {true},
		// l and r are stored only as counted payloads of the new box.
		"rebuild": {true},
		// l reaches a builtin map set — an uncounted retention — so the
		// scrutinee read cannot be credited either.
		"stash": {false, false},
		// `v.tail[i]` copies a scalar out of an array field.
		"tail_at": {true, true},
		// A function value in callee position is loaded and dispatched.
		"call_it": {true, true},
	}
	for fn, want := range cases {
		got := paramCountedFor(t, projectionCreditSrc, fn)
		if len(got) != len(want) {
			t.Errorf("paramCountedRetain[%s] = %v, want %v", fn, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("paramCountedRetain[%s][%d] = %v, want %v (%v)", fn, i, got[i], want[i], got)
			}
		}
	}
}

// The op-level half of the closure-argument reclaim: a lambda in argument
// position is stashed and released through the pair's drop-fn pointer once
// the call has returned, so the pair and env it allocated per call are freed.
func TestClosureArgumentTempIsReleasedAfterTheCall(t *testing.T) {
	src := `
@noinline
function apply(x: i32, f: (i32) => i32): i32 { return f(x); }
function main(): i32 {
    var i: i32 = 3;
    return apply(i, (v: i32) => v + i);
}`
	p := lowerSourceWith(t, src, 8)
	main := funcNamed(p, "main")
	if main == nil {
		t.Fatal("no lowered main")
	}
	sawCall, released := false, false
	for _, op := range main.Ops {
		if op.Kind == OpCallDirect && op.Str == "apply" {
			sawCall = true
		}
		if sawCall && op.Kind == OpCallDirect && op.Str == "__drop_closure_value" {
			released = true
		}
	}
	if !released {
		t.Error("main never releases the lambda it passed to apply — the pair and env leak once per call")
	}
	if funcNamed(p, "__drop_closure_value") == nil {
		t.Error("__drop_closure_value is called but was not generated")
	}
}
