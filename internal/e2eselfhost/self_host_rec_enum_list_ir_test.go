package e2eselfhost

import "testing"

// recEnumListIRCases pin a RECURSIVE enum used as a multi-node heap data
// structure — a cons-list `enum List { Cons(i32, List), Nil }` whose `Cons`
// variant carries the enum type itself — on the self-host IR path (x86-64 +
// wasm). The existing recursive-enum coverage (self_host_rc_precise_drop's
// `Tree.Leaf(7)`) is a single shallow node whose match arm returns immediately;
// it never builds or traverses a multi-level chain. These cases exercise the
// distinct shape: a heap-boxed enum-payload CHAIN (each `Cons` boxes the next
// `List`), genuine deep structural recursion over that payload (a function that
// recurses on the enum value nested inside the enum), and the multi-node drop on
// function exit. All of it already lowers, so no compiler change — this is an
// observability pin against a regression off the IR path.
//
// Each case is oracle-checked against the interpreter; every result stays
// <= 120 (the wasm exit-code clamp, #2908).
const recEnumListIRPrelude = `enum List { Cons(i32, List), Nil }
function sum(l: List): i32 {
    match (l) {
        Cons(h, t) => { return h + sum(t); },
        Nil => { return 0; },
    }
}
function length(l: List): i32 {
    match (l) {
        Cons(h, t) => { return 1 + length(t); },
        Nil => { return 0; },
    }
}
function head_or(l: List, d: i32): i32 {
    match (l) {
        Cons(h, t) => { return h; },
        Nil => { return d; },
    }
}
`

var recEnumListIRCases = []struct {
	name string
	main string
	want int
}{
	// deep recursion summing a 3-node chain: 10 + 20 + 12 = 42.
	{"sum-3", `var l: List = Cons(10, Cons(20, Cons(12, Nil))); return sum(l);`, 42},
	// length of a 5-node chain.
	{"length-5", `var l: List = Cons(1, Cons(2, Cons(3, Cons(4, Cons(5, Nil))))); return length(l);`, 5},
	// sum over a 5-node chain: 1+2+3+4+5 = 15.
	{"sum-5", `var l: List = Cons(1, Cons(2, Cons(3, Cons(4, Cons(5, Nil))))); return sum(l);`, 15},
	// the Nil base case (empty list) returns 0; +3 keeps the exit code distinct.
	{"empty-sum", `var l: List = Nil; return sum(l) + 3;`, 3},
	// the Cons arm binds the head payload across the recursion boundary.
	{"head-or", `var l: List = Cons(7, Nil); return head_or(l, 99);`, 7},
	// the Nil arm returns the default.
	{"head-or-empty", `var l: List = Nil; return head_or(l, 42);`, 42},
}

func recEnumListIRSrc(mainBody string) string {
	return recEnumListIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostRecEnumListIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostRecEnumListIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range recEnumListIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, recEnumListIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
