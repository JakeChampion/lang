package e2eselfhost

import "testing"

// retStructFieldIRCases pin the move-on-return of a POINTER-shaped FIELD of a
// local struct on the self-host IR path (#4801). A function `return r.node` over a
// local `var r: P = ...` hands the field out to the caller, but the lowerer's exit
// dec-sweep still reclaimed `r` — emit_struct_field_drops' __struct_drop_<T> then
// DEEP-freed the returned field out from under the caller. It surfaced as a SIGSEGV
// only once a later allocation recycled the freed block: the watbin `wat_parse`
// (`var r = wat_parse_one(...); return r.node;`) returned a dangling SExpr tree, so
// emit_binary's enc_functype allocations recycled a freed tree node and the import
// walk dereferenced an encoder byte (0x7f) as a node pointer — the SIGABRT/exit-134
// the whole watbin/wit/component test family tripped on. The fix keeps the parent
// struct local out of the sweep (returned_moved_arr_slots' ExprFieldAccess arm), so
// the returned field survives; the box + any un-returned heap sibling fields LEAK
// (sound, never over-free), the struct-field sibling of the #3720 enum/array move.
// Each case is value-pinned against the native oracle.
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
function get(): Node { var r: P = mk(); return r.node; }
function main(): i32 {
    var t: Node = get();
    var s: i32 = t.text.len();
    var pad: i32[] = [];
    var k: i32 = 0;
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
        var inner: PS = parse_many(s, i + 1);
        var pos: i32 = inner.pos;
        if (pos < s.len() && s[pos] == 41) { pos = pos + 1; }
        return PS { node: inner.node, pos: pos };
    }
    return PS { node: Node { kind: 2, text: "leaf", items: [] }, pos: i + 1 };
}
function parse_many(s: string, i: i32): PS {
    var items: Node[] = [];
    var pos: i32 = i;
    while (pos < s.len() && s[pos] != 41) {
        var p: PS = parse_one(s, pos);
        items = items.append(p.node);
        pos = p.pos;
    }
    return PS { node: Node { kind: 0, text: "grp", items: items }, pos: pos };
}
function wat_parse(s: string): Node {
    var r: PS = parse_many(s, 0);
    return r.node;
}
function count(t: Node): i32 {
    var c: i32 = 1;
    var k: i32 = 0;
    while (k < t.items.len()) { c = c + count(t.items[k]); k = k + 1; }
    return c;
}
function main(): i32 {
    var root: Node = wat_parse("(ab)c");
    var pad: i32[] = [];
    var k: i32 = 0;
    while (k < 20) { pad = pad.append(1); k = k + 1; }
    return count(root) + pad.len();
}`, 25},
	// A STRING field return `return r.s` — the field is a heap string moved out; the
	// parent leaks (sound). Value-pinned so a mis-applied keep still reads correctly.
	{"string_field", `struct S { s: string, n: i32 }
function mk(): S { return S { s: "hello" + "!", n: 3 }; }
function get(): string { var r: S = mk(); return r.s; }
function main(): i32 {
    var x: string = get();
    var pad: i32[] = [];
    var k: i32 = 0;
    while (k < 30) { pad = pad.append(127); k = k + 1; }
    return x.len() + pad.len();
}`, 36},
	// An ARRAY field return `return r.xs` — the buffer moves out with the parent
	// leaking; walked after a recycling allocation.
	{"array_field", `struct A { xs: i32[], n: i32 }
function mk(): A { return A { xs: [10, 20, 30], n: 2 }; }
function get(): i32[] { var r: A = mk(); return r.xs; }
function main(): i32 {
    var v: i32[] = get();
    var pad: i32[] = [];
    var k: i32 = 0;
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
