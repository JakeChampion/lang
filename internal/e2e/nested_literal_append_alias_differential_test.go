// Differential regression for #6665: an array field aliased into the same
// container literal that appends to it must see the PRE-append buffer.
//
// `o = S { xs: o.xs.append(i), inner: I { tag: i, data: o.xs } }` evaluates
// both fields against the container `o` still names, so `data` is the array
// one shorter than `xs`. Native took the rc==1 in-place grow — the field
// receiver carried no last-use information the way a bare ident does — and
// `data` observed the growth: interp returned 24 where native returned 34.
//
// The alias does not have to be a nested literal (a flat sibling field, a
// call argument, and the whole container passed on all do it), does not have
// to be in the same statement (a later read of the same place, or a struct
// alias bound before the append, do it too), and needs a buffer with spare
// CAPACITY to show at all — a literal-built array reallocates on the first
// grow and hides it, which is why the loop builds one first. Each case prints
// its result so the oracle (interp) and all three backends are compared by
// stdout.
package e2e

import "testing"

func TestNestedLiteralAppendAliasDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping nested-literal append-alias differential in -short mode")
	}
	cases := []struct {
		name, src string
	}{
		// The #6665 repro. Per call: 10 + 9 + 2 + 7 = 28 (native: 29).
		{"nested_literal_alias", `import "std/i32";
struct I { tag: i32, data: i32[] }
struct S { xs: i32[], inner: I, n: i32 }
function work(k: i32): i32 {
    var o: S = S { xs: [1, 2], inner: I { tag: 0, data: [9, 8] }, n: 0 };
    var i: i32 = 0;
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
		// FLAT sibling field, no nesting — the alias is one field over.
		{"flat_sibling_alias", `import "std/i32";
struct S { xs: i32[], ys: i32[], n: i32 }
function work(k: i32): i32 {
    var o: S = S { xs: [1, 2], ys: [9, 8], n: 0 };
    var i: i32 = 0;
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
		// The alias is built by a CALL inside the same literal.
		{"alias_through_call_arg", `import "std/i32";
struct I { tag: i32, data: i32[] }
struct S { xs: i32[], inner: I, n: i32 }
function mk(tag: i32, d: i32[]): I { return I { tag: tag, data: d }; }
function work(k: i32): i32 {
    var o: S = S { xs: [1, 2], inner: I { tag: 0, data: [9, 8] }, n: 0 };
    var i: i32 = 0;
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
		// The WHOLE container is handed to a call in the same literal, which
		// reaches the field itself.
		{"alias_through_whole_container", `import "std/i32";
struct I { tag: i32, data: i32[] }
struct S { xs: i32[], inner: I, n: i32 }
function mkFromS(tag: i32, s: S): I { return I { tag: tag, data: s.xs }; }
function work(k: i32): i32 {
    var o: S = S { xs: [1, 2], inner: I { tag: 0, data: [9, 8] }, n: 0 };
    var i: i32 = 0;
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
		// A two-hop place: the append and the alias both read `o.a.b`.
		{"nested_field_chain", `import "std/i32";
struct A { b: i32[] }
struct S { a: A, ys: i32[], n: i32 }
function work(k: i32): i32 {
    var o: S = S { a: A { b: [1, 2] }, ys: [9, 8], n: 0 };
    var i: i32 = 0;
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
		// No rebinding at all: the container survives the append, so a LATER
		// statement reading the same place sees the pre-append length.
		{"later_statement_reads_place", `import "std/i32";
struct S { xs: i32[], n: i32 }
function work(k: i32): i32 {
    var o: S = S { xs: [1, 2], n: 0 };
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < k) {
        var q: S = S { xs: o.xs.append(i), n: i };
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
		// The container is rebound, but a STRUCT ALIAS taken before the append
		// still names the old one — the rebinding forwards the name, not the
		// reference.
		{"struct_alias_outlives_rebind", `import "std/i32";
struct S { xs: i32[], n: i32 }
function work(k: i32): i32 {
    var o: S = S { xs: [1, 2], n: 0 };
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < k) {
        var old: S = o;
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
		// Guard: a sibling read of a DISJOINT field does not alias the grown
		// buffer, so this shape must keep the in-place grow (its O(1) is what
		// TestWASMSelfReassignFieldBounded pins) and still be correct.
		{"disjoint_sibling_guard", `import "std/i32";
struct S { xs: i32[], ys: i32[], n: i32 }
function work(k: i32): i32 {
    var o: S = S { xs: [1, 2], ys: [9, 8], n: 0 };
    var i: i32 = 0;
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
		// Guard: the struct-update SPREAD form the self-host assemblers emit
		// with — `a = A { ...a, code: a.code.append(v) }`. The spread copies
		// every field EXCEPT the one it overrides, so it is not an alias of the
		// grown buffer and the append stays in place.
		{"struct_update_spread_guard", `import "std/i32";
struct Asm { code: i32[], fix_offs: i32[], names: i32[] }
function emit(a: Asm, opcode: i32, w: i32): Asm {
    if (w != 0) { a = Asm { ...a, code: a.code.append(9) }; }
    a = Asm { ...a, code: a.code.append(opcode) };
    var patch_off: i32 = a.code.len() - 1;
    a = Asm { ...a, fix_offs: a.fix_offs.append(patch_off) };
    return a;
}
function main(): i32 {
    var a: Asm = Asm { code: [], fix_offs: [], names: [] };
    var i: i32 = 0;
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
