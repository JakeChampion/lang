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

// A projection of any depth handed to a counted position, an operand of an
// operator, and the scrutinee of a match all read the parameter without
// retaining it: `r.headers.names` is appended into a fresh field,
// `h.names[i] == key` compares an element, and `match (c.tails[at])` binds
// the element's payload under the rules the parameter is held to.
// Both refused the parameter before, and the refusal cascaded to every caller
// handing such a function a fresh argument, which was then never released:
// five blocks per response through `with_header` on the serve loop's own
// refusal path.
const nestedProjectionCreditSrc = `
struct H { names: string[], values: string[] }
struct R { status: i32, headers: H }

function retitle(r: R, n: string): R {
    return R { ...r, headers: H { names: r.headers.names.append(n), values: r.headers.values.append(n) } };
}
function has(h: H, key: string): boolean {
    var i: i32 = 0;
    while (i < h.names.len()) {
        if (h.names[i] == key) { return true; }
        i = i + 1;
    }
    return false;
}
function label(r: R, sep: string): string {
    return r.headers.names[0] + sep;
}
enum Tail { NoTail, Some_(string) }
struct Conns { fds: i32[], tails: Tail[] }
function tail_len(c: Conns, at: i32): i32 {
    match (c.tails[at]) {
        Some_(s) => { return s.len(); },
        _ => { return 0; }
    }
    return 0;
}
function main(): i32 {
    var t: string[] = [];
    var r: R = R { status: 1, headers: H { names: t, values: [] } };
    if (has(retitle(r, "x").headers, "x")) { return label(r, ":").len(); }
    return tail_len(Conns { fds: [1], tails: [Some_("t")] }, 0);
}`

func TestNestedProjectionsAndOperandsAreNonRetainingReads(t *testing.T) {
	cases := map[string][]bool{
		"retitle": {true, true},
		"has":     {true, true},
		"label":   {true, true},
		// The match is over an element of a field; its binding is confined
		// to a scalar read.
		"tail_len": {true, true},
	}
	for fn, want := range cases {
		got := paramCountedFor(t, nestedProjectionCreditSrc, fn)
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
