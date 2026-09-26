package e2eselfhost

import "testing"

// genericReturnLocalCases match a generic enum value that came back through a
// generic function whose return type names its type parameters. The enum pass
// has to bind those parameters from the call's arguments to type the match,
// whether the call is the scrutinee itself, the initialiser of a local, nested,
// or reached through a tuple element (#10330). Each exit code is the native
// interpreter's.
var genericReturnLocalCases = []struct {
	name     string
	src      string
	expected int
}{
	{"local_from_identity", `enum Tree[T] { Leaf(T), Node(Tree[T], Tree[T]) }
function whole[C](c: C): C { return c; }
function main(): i32 {
    var t: Tree[string] = Leaf("xy");
    var w = whole(t);
    match (w) {
        Leaf(v) => { return v.len() * 10; },
        Node(l, r) => { return 90; }
    }
}`, 20},
	{"scrutinee_is_identity_call", `enum Tree[T] { Leaf(T), Node(Tree[T], Tree[T]) }
function whole[C](c: C): C { return c; }
function main(): i32 {
    var t: Tree[string] = Leaf("xyz");
    match (whole(t)) {
        Leaf(v) => { return v.len() * 10; },
        Node(l, r) => { return 90; }
    }
}`, 30},
	{"second_of_two_params", `enum Pair[A, B] { Both(A, B), Neither }
function second[X, Y](x: X, y: Y): Y { return y; }
function main(): i32 {
    var p: Pair[string, boolean] = Both("abcd", true);
    var q = second(7, p);
    match (q) {
        Both(s, b) => { if (b) { return s.len(); } return 8; },
        Neither => { return 50; }
    }
}`, 4},
	{"nested_identity_then_inner_match", `enum Tree[T] { Leaf(T), Node(Tree[T], Tree[T]) }
function whole[C](c: C): C { return c; }
function main(): i32 {
    var t: Tree[string] = Node(Leaf("a"), Leaf("bcd"));
    var w = whole(whole(t));
    match (w) {
        Leaf(v) => { return 90; },
        Node(l, r) => {
            match (r) {
                Leaf(v) => { return v.len(); },
                Node(x, y) => { return 80; }
            }
        }
    }
}`, 3},
	{"tuple_element_of_generic_return", `enum Tree[T] { Leaf(T), Node(Tree[T], Tree[T]) }
function dup[C](c: C): (C, C) { return (c, c); }
function main(): i32 {
    var t: Tree[string] = Leaf("abcdef");
    var d = dup(t);
    match (d.1) {
        Leaf(v) => { return v.len(); },
        Node(l, r) => { return 90; }
    }
}`, 6},
}

func TestSelfHostGenericReturnLocalIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, tc := range genericReturnLocalCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src, target, "FERN_STRICT_IR=1"); code != tc.expected {
					t.Errorf("exited %d, want %d\n%s", code, tc.expected, stderr)
				}
			})
		}
	}
}
