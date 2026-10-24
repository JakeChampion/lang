package e2ecompiler

import "testing"

var genericEnumCtorLocalCases = []struct {
	name     string
	src      string
	expected int
}{
	{"struct_lit_from_payload", `struct P[T] { b: T }
enum Tree[T] { Leaf(T), Node(Tree[T], Tree[T]) }
function main(): i32 {
    let t = Leaf("xyz");
    match (t) {
        Leaf(v) => { let q = P { b: v }; return q.b.len(); },
        Node(l, r) => { return 9; }
    }
}`, 3},
	{"struct_lit_from_qualified_payload", `struct P[T] { b: T }
enum Tree[T] { Leaf(T), Node(Tree[T], Tree[T]) }
function main(): i32 {
    let t = Tree.Leaf("xyzw");
    match (t) {
        Leaf(v) => { let r = P { b: v }; return r.b.len(); },
        Node(l, r) => { return 90; }
    }
}`, 4},
	{"struct_lit_from_two_param_payload", `struct Q[A, B] { a: A, b: B }
enum Pair[A, B] { Both(A, B), Neither }
function main(): i32 {
    let p = Both("ab", true);
    let n: i32 = 0;
    match (p) {
        Both(x, y) => { let q = Q { a: x, b: y }; if (q.b) { n = n + q.a.len(); } },
        Neither => { n = 50; }
    }
    return n;
}`, 2},
	{"match_qualified_ctor_local", `enum Tree[T] { Leaf(T), Node(Tree[T], Tree[T]) }
function main(): i32 {
    let t = Tree.Leaf("xyzw");
    match (t) {
        Leaf(v) => { return v.len(); },
        Node(l, r) => { return 90; }
    }
}`, 4},
	{"match_two_param_ctor_local", `enum Pair[A, B] { Both(A, B), Neither }
function main(): i32 {
    let p = Both("ab", true);
    match (p) {
        Both(x, y) => { if (y) { return x.len(); } return 7; },
        Neither => { return 50; }
    }
}`, 2},
}

func TestSelfHostGenericEnumCtorLocalIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, tc := range genericEnumCtorLocalCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src, target, "FERN_STRICT_IR=1"); code != tc.expected {
					t.Errorf("exited %d, want %d\n%s", code, tc.expected, stderr)
				}
			})
		}
	}
}
