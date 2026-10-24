package e2ecompiler

import "testing"

var selfrefStructIRCases = []struct {
	name string
	main string
}{
	{"bind-scalar", `struct Node { v: i32, next: Node[] }
function main(): i32 { let n = Node { v: 5, next: [] }; return n.v; }`},
	{"empty-next-len", `struct Node { v: i32, next: Node[] }
function main(): i32 { let n = Node { v: 5, next: [] }; return n.next.len(); }`},
	{"one-child-len", `struct Node { v: i32, next: Node[] }
function main(): i32 { let leaf = Node { v: 1, next: [] }; let n = Node { v: 5, next: [leaf] }; return n.next.len(); }`},
	{"one-child-field", `struct Node { v: i32, next: Node[] }
function main(): i32 { let leaf = Node { v: 7, next: [] }; let n = Node { v: 5, next: [leaf] }; return n.next[0].v; }`},
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
	{"as-param", `struct Node { v: i32, next: Node[] }
function head_val(n: Node): i32 { return n.v; }
function main(): i32 { let n = Node { v: 42, next: [] }; return head_val(n); }`},
	{"mutual-rec", `struct A { tag: i32, bs: B[] }
struct B { val: i32, peers: A[] }
function main(): i32 {
    let b = B { val: 9, peers: [] };
    let a = A { tag: 3, bs: [b] };
    return a.bs[0].val + a.tag;
}`},
}

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
