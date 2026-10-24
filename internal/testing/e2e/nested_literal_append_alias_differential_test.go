package e2e

import "testing"

func TestNestedLiteralAppendAliasDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping nested-literal append-alias differential in -short mode")
	}
	cases := []struct {
		name, src string
	}{
		{"nested_literal_alias", `import "std/i32";
struct I { tag: i32, data: i32[] }
struct S { xs: i32[], inner: I, n: i32 }
function work(k: i32): i32 {
    let o: S = S { xs: [1, 2], inner: I { tag: 0, data: [9, 8] }, n: 0 };
    let i: i32 = 0;
    while (i < k) {
        o = S { xs: o.xs.append(i), inner: I { tag: i, data: o.xs }, n: i };
        i = i + 1;
    }
    return o.xs.len() + o.inner.data.len() + o.inner.data[1] + o.inner.tag;
}
function main(): i32 {
    print(work(8).to_string());   // 28
    return 0;
}`},
		{"flat_sibling_alias", `import "std/i32";
struct S { xs: i32[], ys: i32[], n: i32 }
function work(k: i32): i32 {
    let o: S = S { xs: [1, 2], ys: [9, 8], n: 0 };
    let i: i32 = 0;
    while (i < k) {
        o = S { xs: o.xs.append(i), ys: o.xs, n: i };
        i = i + 1;
    }
    return o.xs.len() * 100 + o.ys.len() * 10 + o.n;
}
function main(): i32 {
    print(work(8).to_string());   // 1097
    return 0;
}`},
		{"alias_through_call_arg", `import "std/i32";
struct I { tag: i32, data: i32[] }
struct S { xs: i32[], inner: I, n: i32 }
function mk(tag: i32, d: i32[]): I { return I { tag: tag, data: d }; }
function work(k: i32): i32 {
    let o: S = S { xs: [1, 2], inner: I { tag: 0, data: [9, 8] }, n: 0 };
    let i: i32 = 0;
    while (i < k) {
        o = S { xs: o.xs.append(i), inner: mk(i, o.xs), n: i };
        i = i + 1;
    }
    return o.xs.len() * 100 + o.inner.data.len() * 10 + o.inner.tag;
}
function main(): i32 {
    print(work(8).to_string());   // 1097
    return 0;
}`},
		{"alias_through_whole_container", `import "std/i32";
struct I { tag: i32, data: i32[] }
struct S { xs: i32[], inner: I, n: i32 }
function mkFromS(tag: i32, s: S): I { return I { tag: tag, data: s.xs }; }
function work(k: i32): i32 {
    let o: S = S { xs: [1, 2], inner: I { tag: 0, data: [9, 8] }, n: 0 };
    let i: i32 = 0;
    while (i < k) {
        o = S { xs: o.xs.append(i), inner: mkFromS(i, o), n: i };
        i = i + 1;
    }
    return o.xs.len() * 100 + o.inner.data.len() * 10 + o.inner.tag;
}
function main(): i32 {
    print(work(8).to_string());   // 1097
    return 0;
}`},
		{"nested_field_chain", `import "std/i32";
struct A { b: i32[] }
struct S { a: A, ys: i32[], n: i32 }
function work(k: i32): i32 {
    let o: S = S { a: A { b: [1, 2] }, ys: [9, 8], n: 0 };
    let i: i32 = 0;
    while (i < k) {
        o = S { a: A { b: o.a.b.append(i) }, ys: o.a.b, n: i };
        i = i + 1;
    }
    return o.a.b.len() * 100 + o.ys.len() * 10 + o.n;
}
function main(): i32 {
    print(work(8).to_string());   // 1097
    return 0;
}`},
		{"later_statement_reads_place", `import "std/i32";
struct S { xs: i32[], n: i32 }
function work(k: i32): i32 {
    let o: S = S { xs: [1, 2], n: 0 };
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < k) {
        let q: S = S { xs: o.xs.append(i), n: i };
        acc = acc + o.xs.len();
        o = q;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    print(work(8).to_string());   // 44
    return 0;
}`},
		{"struct_alias_outlives_rebind", `import "std/i32";
struct S { xs: i32[], n: i32 }
function work(k: i32): i32 {
    let o: S = S { xs: [1, 2], n: 0 };
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < k) {
        let old: S = o;
        o = S { xs: o.xs.append(i), n: i };
        acc = acc + old.xs.len();
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    print(work(8).to_string());   // 44
    return 0;
}`},
		{"disjoint_sibling_guard", `import "std/i32";
struct S { xs: i32[], ys: i32[], n: i32 }
function work(k: i32): i32 {
    let o: S = S { xs: [1, 2], ys: [9, 8], n: 0 };
    let i: i32 = 0;
    while (i < k) {
        o = S { xs: o.xs.append(i), ys: o.ys, n: i };
        i = i + 1;
    }
    return o.xs.len() * 100 + o.ys.len() * 10 + o.n;
}
function main(): i32 {
    print(work(8).to_string());   // 1027
    return 0;
}`},
		{"struct_update_spread_guard", `import "std/i32";
struct Asm { code: i32[], fix_offs: i32[], names: i32[] }
function emit(a: Asm, opcode: i32, w: i32): Asm {
    if (w != 0) { a = Asm { ...a, code: a.code.append(9) }; }
    a = Asm { ...a, code: a.code.append(opcode) };
    let patch_off: i32 = a.code.len() - 1;
    a = Asm { ...a, fix_offs: a.fix_offs.append(patch_off) };
    return a;
}
function main(): i32 {
    let a: Asm = Asm { code: [], fix_offs: [], names: [] };
    let i: i32 = 0;
    while (i < 8) { a = emit(a, i, i % 2); i = i + 1; }
    print((a.code.len() * 10 + a.fix_offs.len()).to_string());   // 128
    return 0;
}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertNumProgramAgrees(t, tc.src)
		})
	}
}
