package e2ecompiler

import "testing"

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
	{"sum-3", `let l: List = Cons(10, Cons(20, Cons(12, Nil))); return sum(l);`, 42},
	{"length-5", `let l: List = Cons(1, Cons(2, Cons(3, Cons(4, Cons(5, Nil))))); return length(l);`, 5},
	{"sum-5", `let l: List = Cons(1, Cons(2, Cons(3, Cons(4, Cons(5, Nil))))); return sum(l);`, 15},
	{"empty-sum", `let l: List = Nil; return sum(l) + 3;`, 3},
	{"head-or", `let l: List = Cons(7, Nil); return head_or(l, 99);`, 7},
	{"head-or-empty", `let l: List = Nil; return head_or(l, 42);`, 42},
}

func recEnumListIRSrc(mainBody string) string {
	return recEnumListIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

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
