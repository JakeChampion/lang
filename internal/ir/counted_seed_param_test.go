package ir

import "testing"

// `var cur = p;` followed by `cur = advance(cur)` is a COUNTED alias, not
// a borrow: the *ast.Var lowering emits the transfer inc for a reassigned
// binding seeded from a parameter, so the local owns a reference of its
// own and the caller's argument is retained counted.
// computeFreeEligible has read that binding this way since #6403 (its
// countedSeed map); the counted-retain summaries refused it, which is the
// refusal that ungrounded every threaded walker in the self-host checker.

func TestParamSeededIntoAReassignedLocalIsCounted(t *testing.T) {
	cases := []struct {
		name string
		src  string
		fn   string
	}{
		{"string", `function step(x: string): string { return x + "."; }
function walk(n: i32, s: string): i32 {
    var cur: string = s;
    var i: i32 = 0;
    while (i < n) { cur = step(cur); i = i + 1; }
    return cur.len();
}`, "walk"},
		{"array", `function step(x: string[]): string[] { return x.append("."); }
function walk(n: i32, s: string[]): i32 {
    var cur: string[] = s;
    var i: i32 = 0;
    while (i < n) { cur = step(cur); i = i + 1; }
    return cur.len();
}`, "walk"},
		{"struct", `struct Scope { names: string[], depth: i32 }
function step(x: Scope): Scope { return Scope { names: x.names, depth: x.depth + 1 }; }
function walk(n: i32, s: Scope): i32 {
    var cur: Scope = s;
    var i: i32 = 0;
    while (i < n) { cur = step(cur); i = i + 1; }
    return cur.depth;
}`, "walk"},
	}
	for _, c := range cases {
		got := paramCountedFor(t, c.src+"\nfunction main(): i32 { return 0; }", c.fn)
		if len(got) != 2 || !got[1] {
			t.Errorf("%s: paramCountedRetain[%s] = %v, want [_ true] — the reassigned "+
				"binding emits the transfer inc, so the parameter is retained counted",
				c.name, c.fn, got)
		}
	}
}

// The two conditions are countedSeed's, and both are load-bearing.
//
// A binding that is NEVER reassigned gets no transfer inc — it holds the
// borrow and nothing else — so crediting it would let the caller free a
// buffer the callee is still reading through.
func TestParamSeededIntoAConstantLocalStaysUncredited(t *testing.T) {
	src := `function hold(s: string[]): string[] {
    var cur: string[] = s;
    return cur;
}
function main(): i32 { return 0; }`
	if got := paramCountedFor(t, src, "hold"); len(got) == 1 && got[0] {
		t.Errorf("paramCountedRetain[hold] = %v, but `cur` is never reassigned, so the "+
			"binding is a borrow with no inc behind it", got)
	}
}

// A name declared twice would have one verdict governing a slot two
// bindings share, which is what localNameUnique rules out for
// computeFreeEligible's own copy of this rule.
func TestParamSeededIntoADuplicatedNameStaysUncredited(t *testing.T) {
	src := `function walk(n: i32, s: string[]): i32 {
    var total: i32 = 0;
    if (n > 0) {
        var cur: string[] = s;
        cur = cur.append("x");
        total = total + cur.len();
    }
    if (n > 1) {
        var cur: string[] = s;
        total = total + cur.len();
    }
    return total;
}
function main(): i32 { return 0; }`
	if got := paramCountedFor(t, src, "walk"); len(got) == 2 && got[1] {
		t.Errorf("paramCountedRetain[walk] = %v, but `cur` is declared twice — one "+
			"verdict cannot govern both bindings", got)
	}
}

// The grounding effect: crediting the binding grounds the whole
// forwarding chain, because the summary is a least fixpoint that only
// ever adds credits.
func TestSeedCreditGroundsTheForwardingChain(t *testing.T) {
	src := `struct Scope { names: string[], depth: i32 }
function advance(x: Scope): Scope { return Scope { names: x.names, depth: x.depth + 1 }; }
function block(n: i32, s: Scope): i32 {
    var cur: Scope = s;
    var i: i32 = 0;
    while (i < n) { cur = advance(cur); i = i + 1; }
    return cur.depth;
}
function outer(n: i32, s: Scope): i32 { return block(n, s); }
function main(): i32 { return 0; }`
	if got := paramCountedFor(t, src, "outer"); len(got) != 2 || !got[1] {
		t.Errorf("paramCountedRetain[outer] = %v, want [_ true] — forwarding to a "+
			"grounded position retains nothing new", got)
	}
}
