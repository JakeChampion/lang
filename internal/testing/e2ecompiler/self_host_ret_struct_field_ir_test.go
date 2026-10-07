package e2ecompiler

import "testing"

// retStructFieldIRCases pin the move-on-return of a POINTER-shaped FIELD of a
// local struct on the self-host IR path (#4801). `return r.node` over a local
// `let r: P = ...` hands the field out to the caller, so releasing `r` at exit
// must not free the returned field. When it did, the failure surfaced only once
// a later allocation recycled the freed block: the watbin `wat_parse`
// (`let r = wat_parse_one(...); return r.node;`) returned a dangling SExpr tree,
// and the import walk dereferenced an encoder byte (0x7f) as a node pointer —
// the SIGABRT/exit-134 the whole watbin/wit/component test family tripped on.
// Each case is value-pinned.
var retStructFieldIRCases = []struct {
	name string
	src  string
	want int
}{
	// The core trigger: get() returns r.node — a nested STRUCT field carrying its
	// own subtree — then main walks the tree ACROSS an allocation that recycles the
	// freed node. Pre-fix: SIGSEGV; the exact watbin wat_parse shape distilled.
	{"tree_struct_field", `struct Node { kind: i32, text: string, items: Node[] }
struct P { node: Node, pos: i32 }
function mk(): P { return P { node: Node { kind: 1, text: "ab", items: [Node { kind: 2, text: "x", items: [] }, Node { kind: 2, text: "y", items: [] }] }, pos: 7 }; }
function get(): Node { let r: P = mk(); return r.node; }
function main(): i32 {
    let t: Node = get();
    let s: i32 = t.text.len();
    let pad: i32[] = [];
    let k: i32 = 0;
    while (k < 40) { pad = pad.append(127); k = k + 1; }
    s = s + t.items.len() + t.items[0].text.len() + t.items[1].text.len() + pad.len();
    return s;
}`, 46},
	// A mutually-recursive parser returning r.node (a struct FIELD, not an enum
	// payload) — the wat_parse_one / wat_parse shape watbin actually uses.
	{"recursive_tree_field", `struct Node { kind: i32, text: string, items: Node[] }
struct PS { node: Node, pos: i32 }
function parse_one(s: string, i: i32): PS {
    if (s[i] == 40) {
        let inner: PS = parse_many(s, i + 1);
        let pos: i32 = inner.pos;
        if (pos < s.len() && s[pos] == 41) { pos = pos + 1; }
        return PS { node: inner.node, pos: pos };
    }
    return PS { node: Node { kind: 2, text: "leaf", items: [] }, pos: i + 1 };
}
function parse_many(s: string, i: i32): PS {
    let items: Node[] = [];
    let pos: i32 = i;
    while (pos < s.len() && s[pos] != 41) {
        let p: PS = parse_one(s, pos);
        items = items.append(p.node);
        pos = p.pos;
    }
    return PS { node: Node { kind: 0, text: "grp", items: items }, pos: pos };
}
function wat_parse(s: string): Node {
    let r: PS = parse_many(s, 0);
    return r.node;
}
function count(t: Node): i32 {
    let c: i32 = 1;
    let k: i32 = 0;
    while (k < t.items.len()) { c = c + count(t.items[k]); k = k + 1; }
    return c;
}
function main(): i32 {
    let root: Node = wat_parse("(ab)c");
    let pad: i32[] = [];
    let k: i32 = 0;
    while (k < 20) { pad = pad.append(1); k = k + 1; }
    return count(root) + pad.len();
}`, 25},
	// A STRING field return `return r.s` — the field is a heap string moved out; the
	// parent leaks (sound). Value-pinned so a mis-applied keep still reads correctly.
	{"string_field", `struct S { s: string, n: i32 }
function mk(): S { return S { s: "hello" + "!", n: 3 }; }
function get(): string { let r: S = mk(); return r.s; }
function main(): i32 {
    let x: string = get();
    let pad: i32[] = [];
    let k: i32 = 0;
    while (k < 30) { pad = pad.append(127); k = k + 1; }
    return x.len() + pad.len();
}`, 36},
	// An ARRAY field return `return r.xs` — the buffer moves out with the parent
	// leaking; walked after a recycling allocation.
	{"array_field", `struct A { xs: i32[], n: i32 }
function mk(): A { return A { xs: [10, 20, 30], n: 2 }; }
function get(): i32[] { let r: A = mk(); return r.xs; }
function main(): i32 {
    let v: i32[] = get();
    let pad: i32[] = [];
    let k: i32 = 0;
    while (k < 25) { pad = pad.append(127); k = k + 1; }
    return v.len() + v[0] + v[2] + pad.len();
}`, 68},
}

// TestSelfHostRetStructFieldIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostRetStructFieldIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range retStructFieldIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src+"\n", target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
