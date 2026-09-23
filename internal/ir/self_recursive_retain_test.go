package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// A recursive walker passes its input back into the same parameter of the
// same function. The retain summaries are least fixpoints that start
// all-false, so that occurrence, which asks for the very entry being
// computed, could never be credited: every such walker was refused, and so
// was everything forwarding to it. lib/bre.fern's `add_thread` is one, and
// the refusal rode up through the regexp search into nl's read loop, where
// the 64 KiB buffer it searched in place stayed borrow-tainted and was never
// released, a whole buffer per read.
//
// The self-slot argument is now credited. A retention the recursion does
// make still happens at some other occurrence, which still refutes it: the
// strict summary refuses `pick` for its bare return. The rule reaches only
// a parameter's own slot, so `stash`, whose recursion swaps its two, stays
// refused by both.
func TestSelfRecursiveSlotArgumentIsCredited(t *testing.T) {
	src := `function walk(s: string, i: i32): i32 {
    if (i >= s.len()) { return 0; }
    return walk(s, i + 1) + (s[i] as i32);
}
function pick(s: string, d: i32): string {
    if (d == 0) { return s; }
    return pick(s, d - 1);
}
function stash(s: string, t: string, d: i32): string {
    if (d == 0) { return t; }
    return stash(t, s, d - 1);
}
function outer(s: string): i32 { return walk(s, 0); }
function main(): i32 { return outer("ab") + pick("c", 2).len() + stash("d", "e", 1).len(); }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	strict := inferParamCountedRetain(prog, info, nil)
	weak := inferParamNoUncountedAlias(prog, info, nil)
	cases := []struct {
		fn           string
		idx          int
		strict, weak bool
		why          string
	}{
		{"walk", 0, true, true, "read by length and by byte, and passed back into its own slot"},
		{"outer", 0, true, true, "forwards to walk, which is now credited"},
		{"pick", 0, false, true, "the bare return is refused by the strict summary only"},
		{"stash", 0, false, false, "s crosses into t's slot"},
		{"stash", 1, false, false, "t crosses into s's slot"},
	}
	for _, c := range cases {
		for _, sum := range []struct {
			name string
			tab  map[string][]bool
			want bool
		}{{"paramCountedRetain", strict, c.strict}, {"paramNoUncountedAlias", weak, c.weak}} {
			got := false
			if f := sum.tab[c.fn]; c.idx < len(f) {
				got = f[c.idx]
			}
			if got != sum.want {
				t.Errorf("%s[%s][%d] = %v, want %v (%s)", sum.name, c.fn, c.idx, got, sum.want, c.why)
			}
		}
	}
}
