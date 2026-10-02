package e2eselfhost

import "testing"

// selfrefStructIRCases widen the self-host IR subset to SELF-REFERENTIAL (and
// mutually-recursive) structs — `struct Node { v: i32, next: Node[] }` and the
// like, the shape behind linked lists / trees / ASTs.
//
// The leak-safety gate (fnsigs.decl_is_leaksafe_d) walks a struct's field type
// graph to decide whether it can take the IR path in leak mode (no RC; the boxes
// leak with the struct, matching the AST path's exit codes). It used a depth cap
// to avoid looping on cyclic type graphs, which also rejected legitimate
// self-referential structs — a `next: Node[]` field recurses into Node forever
// until the cap trips, bailing the whole module. The gate now
// threads a `visiting` set and treats a back-edge to a struct already on the
// proof path as leak-safe (a leak-only back-pointer introduces no unsafe field),
// so self-referential and mutually-recursive structs route the IR path while the
// outer struct's own fields are still each validated.
//
// Each case is oracle-checked against the interpreter and returns a value
// <= 120 (cf. the wasmtime exit-code gap #2908).
var selfrefStructIRCases = []struct {
	name string
	main string
}{
	// Bind a self-referential struct, read a scalar field.
	{"bind-scalar", `struct Node { v: i32, next: Node[] }
function main(): i32 { let n = Node { v: 5, next: [] }; return n.v; }`},
	// Empty self-referential array field length.
	{"empty-next-len", `struct Node { v: i32, next: Node[] }
function main(): i32 { let n = Node { v: 5, next: [] }; return n.next.len(); }`},
	// One child: array length + element scalar field read.
	{"one-child-len", `struct Node { v: i32, next: Node[] }
function main(): i32 { let leaf = Node { v: 1, next: [] }; let n = Node { v: 5, next: [leaf] }; return n.next.len(); }`},
	{"one-child-field", `struct Node { v: i32, next: Node[] }
function main(): i32 { let leaf = Node { v: 7, next: [] }; let n = Node { v: 5, next: [leaf] }; return n.next[0].v; }`},
	// Several children: sum element scalar fields in a loop.
	{"children-sum", `struct Node { v: i32, next: Node[] }
function main(): i32 {
    let a = Node { v: 10, next: [] };
    let b = Node { v: 20, next: [] };
    let c = Node { v: 30, next: [] };
    let root = Node { v: 1, next: [a, b, c] };
    let s = 0;
    for ch in root.next { s = s + ch.v; }
    return s + root.v;
}`},
	// Self-referential struct as a function parameter + return.
	{"as-param", `struct Node { v: i32, next: Node[] }
function head_val(n: Node): i32 { return n.v; }
function main(): i32 { let n = Node { v: 42, next: [] }; return head_val(n); }`},
	// Mutually-recursive structs: A holds B[], B holds A[].
	{"mutual-rec", `struct A { tag: i32, bs: B[] }
struct B { val: i32, peers: A[] }
function main(): i32 {
    let b = B { val: 9, peers: [] };
    let a = A { tag: 3, bs: [b] };
    return a.bs[0].val + a.tag;
}`},
}

// TestSelfHostSelfrefStructIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostSelfrefStructIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range selfrefStructIRCases {
		src := tc.main + "\n"
		want := interpExit(t, interpBin, src)
		for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("exited %d, want %d (interp oracle)\n%s", code, want, stderr)
				}
			})
		}
	}
}
