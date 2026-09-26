package e2eselfhost

import "testing"

// genericEnumCtorLocalCases bind an unannotated local from a generic enum's
// variant construction and then match it. Both monomorphisers have to type the
// local from the payload: the struct pass so a literal in an arm can infer its
// own instantiation (#10302), and the enum pass so the arms rewrite to the
// instantiated variants, for a qualified `Tree.Leaf` and a two-parameter enum.
// Each exit code is the native interpreter's.
var genericEnumCtorLocalCases = []struct {
	name     string
	src      string
	expected int
}{
	{"struct_lit_from_payload", `struct P[T] { b: T }
enum Tree[T] { Leaf(T), Node(Tree[T], Tree[T]) }
function main(): i32 {
    var t = Leaf("xyz");
    match (t) {
        Leaf(v) => { var q = P { b: v }; return q.b.len(); },
        Node(l, r) => { return 9; }
    }
}`, 3},
	{"struct_lit_from_qualified_payload", `struct P[T] { b: T }
enum Tree[T] { Leaf(T), Node(Tree[T], Tree[T]) }
function main(): i32 {
    var t = Tree.Leaf("xyzw");
    match (t) {
        Leaf(v) => { var r = P { b: v }; return r.b.len(); },
        Node(l, r) => { return 90; }
    }
}`, 4},
	{"struct_lit_from_two_param_payload", `struct Q[A, B] { a: A, b: B }
enum Pair[A, B] { Both(A, B), Neither }
function main(): i32 {
    var p = Both("ab", true);
    var n: i32 = 0;
    match (p) {
        Both(x, y) => { var q = Q { a: x, b: y }; if (q.b) { n = n + q.a.len(); } },
        Neither => { n = 50; }
    }
    return n;
}`, 2},
	{"match_qualified_ctor_local", `enum Tree[T] { Leaf(T), Node(Tree[T], Tree[T]) }
function main(): i32 {
    var t = Tree.Leaf("xyzw");
    match (t) {
        Leaf(v) => { return v.len(); },
        Node(l, r) => { return 90; }
    }
}`, 4},
	{"match_two_param_ctor_local", `enum Pair[A, B] { Both(A, B), Neither }
function main(): i32 {
    var p = Both("ab", true);
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
