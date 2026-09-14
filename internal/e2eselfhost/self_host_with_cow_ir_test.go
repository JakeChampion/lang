package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// withCowIRCases pin the rc-guarded copy-on-write behind the self-host's
// `a = a.with(i, v)` self-reassign on a scalar-element array.
//
// The in-place-versus-clone choice used to be a STATIC scan of the function
// body for alias shapes (a bare-ident bind, a container literal element). Two
// things were wrong with that. The scan missed most of the ways a buffer gets a
// second holder — an append or `.with` into a nested array, an enum or Option
// payload, a callee that stores or returns its parameter, an element read out
// of a container, a foreach over the array — so every one of those shapes
// wrote through the alias. And a static answer cannot separate the first
// iteration of a loop from the rest, so a slot the scan did flag cloned the
// whole buffer on every update (`core/bigint`'s limb loops: 4.78M allocations
// against native's 15k on `od -t fL`).
//
// Now the slot's count is read at run time, as native's __fern_arr_cow_inplace
// reads it: a sole owner writes in place, a shared buffer is copied once and the
// copy is the sole owner from then on. A slot whose ownership is the hidden
// "$ownflag" bit (a borrowed parameter or a foreach / match projection the body
// rebinds) copies while it holds the borrow and reads the count once it owns a
// replacement.
//
// Each case is oracle-checked against the interpreter under FERN_STRICT_IR, and
// its allocation census must balance at live 0 within `maxAllocs` — a loop of
// a hundred updates that allocated a hundred times is the volume this removes,
// and an in-place write under a wrong uniqueness answer is a wrong exit code,
// not a census reading.
var withCowIRCases = []struct {
	name      string
	src       string
	maxAllocs int64
}{
	// A sole owner: every update in place, nothing allocated past the literal.
	{"unique-local", `function main(): i32 {
    var a: i32[] = [1, 2, 3];
    var i: i32 = 0;
    while (i < 100) { a = a.with(i % 3, 9 + i); i = i + 1; }
    if (a[0] + a[1] + a[2] != 321) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 1},
	// `var b = a`: the first update copies, the other ninety-nine write the copy.
	{"local-alias", `function main(): i32 {
    var a: i32[] = [1, 2, 3];
    var b: i32[] = a;
    var i: i32 = 0;
    while (i < 100) { a = a.with(0, 9 + i); i = i + 1; }
    if (b[0] != 1) { return 1; }
    if (a[0] != 108) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 2},
	{"assign-alias", `function main(): i32 {
    var a: i32[] = [1, 2, 3];
    var b: i32[] = [];
    b = a;
    var i: i32 = 0;
    while (i < 100) { a = a.with(0, 9 + i); i = i + 1; }
    if (b[0] != 1) { return 1; }
    if (a[0] != 108) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 3},
	// The alias is mutated and the original read back.
	{"alias-mutated", `function main(): i32 {
    var a: i32[] = [1, 2, 3];
    var b: i32[] = a;
    var i: i32 = 0;
    while (i < 100) { b = b.with(0, 9 + i); i = i + 1; }
    if (a[0] != 1) { return 1; }
    if (b[0] != 108) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 2},
	// core/bigint's `__bi_mul_small` shape: a local bound from a borrowed param.
	{"param-alias", `@noinline
function churn(p: u64[]): u64 {
    var cur: u64[] = p;
    var i: i32 = 0;
    while (i < 100) { cur = cur.with(i % 3, i as u64); i = i + 1; }
    return cur[0] + cur[1] + cur[2];
}
function main(): i32 {
    var a: u64[] = [1u64, 2u64, 3u64];
    var s: u64 = churn(a);
    if (a[0] != 1u64 || a[1] != 2u64 || a[2] != 3u64) { return 1; }
    if (s != 294u64) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 2},
	// `BigInt.to_string`'s shape: a local bound from a struct field.
	{"field-alias-local", `struct Box { mag: u64[], n: i32 }
function main(): i32 {
    var b: Box = Box { mag: [1u64, 2u64, 3u64], n: 0 };
    var cur: u64[] = b.mag;
    var i: i32 = 0;
    while (i < 100) { cur = cur.with(i % 3, i as u64); i = i + 1; }
    if (b.mag[0] != 1u64 || b.mag[1] != 2u64 || b.mag[2] != 3u64) { return 1; }
    if (cur[0] + cur[1] + cur[2] != 294u64) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 3},
	{"field-alias-param", `struct Box { mag: u64[], n: i32 }
@noinline
function churn(b: Box): u64 {
    var cur: u64[] = b.mag;
    var i: i32 = 0;
    while (i < 100) { cur = cur.with(i % 3, i as u64); i = i + 1; }
    return cur[0] + cur[1] + cur[2];
}
function main(): i32 {
    var b: Box = Box { mag: [1u64, 2u64, 3u64], n: 0 };
    var s: u64 = churn(b);
    if (b.mag[0] != 1u64 || b.mag[1] != 2u64 || b.mag[2] != 3u64) { return 1; }
    if (s != 294u64) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 3},
	// A borrowed parameter updated directly: the borrow is copied once, the
	// owned replacement is written in place, and the frame releases it at exit
	// because an i32 result cannot hand it back.
	{"param-direct", `@noinline
function churn(p: i32[], n: i32): i32 {
    var i: i32 = 0;
    while (i < n) { p = p.with(i % 3, i); i = i + 1; }
    return p[0] + p[1] + p[2];
}
function main(): i32 {
    var a: i32[] = [1, 2, 3];
    var s: i32 = churn(a, 100);
    if (a[0] != 1 || a[1] != 2 || a[2] != 3) { return 1; }
    if (s != 294) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 2},
	// The same, handing the replacement back — and a zero-update call hands the
	// borrow back untouched.
	{"param-direct-u64-returned", `@noinline
function churn(p: u64[], n: i32): u64[] {
    var i: i32 = 0;
    while (i < n) { p = p.with(i % 3, i as u64); i = i + 1; }
    return p;
}
function main(): i32 {
    var a: u64[] = [1u64, 2u64, 3u64];
    var r: u64[] = churn(a, 100);
    if (a[0] != 1u64 || a[1] != 2u64 || a[2] != 3u64) { return 1; }
    if (r[0] + r[1] + r[2] != 294u64) { return 2; }
    var r2: u64[] = churn(a, 0);
    if (r2[0] != 1u64) { return 3; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 2},
	{"param-direct-f64", `@noinline
function churn(p: f64[], n: i32): f64 {
    var i: i32 = 0;
    while (i < n) { p = p.with(i % 2, (i as f64) + 0.5); i = i + 1; }
    return p[0] + p[1];
}
function main(): i32 {
    var a: f64[] = [1.0, 2.0];
    var s: f64 = churn(a, 100);
    if (a[0] != 1.0 || a[1] != 2.0) { return 1; }
    if (s != 198.0) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 2},
	// Every element width the scalar path admits, each with a live alias.
	{"widths", `function main(): i32 {
    var a: u8[] = [1u8, 2u8];
    var b: u8[] = a;
    var c: f64[] = [1.5, 2.5];
    var d: f64[] = c;
    var e: i64[] = [10000000000i64, 2i64];
    var f: i64[] = e;
    var g: boolean[] = [true, false];
    var h: boolean[] = g;
    var i: i32 = 0;
    while (i < 100) {
        a = a.with(0, 9u8);
        c = c.with(0, 9.5);
        e = e.with(0, 90000000000i64);
        g = g.with(0, false);
        i = i + 1;
    }
    if (b[0] != 1u8) { return 1; }
    if (d[0] != 1.5) { return 2; }
    if (f[0] != 10000000000i64) { return 3; }
    if (!h[0]) { return 4; }
    if (a[0] != 9u8 || c[0] != 9.5 || e[0] != 90000000000i64 || g[0]) { return 5; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 8},
	// Holders the static alias scan credited.
	{"struct-lit-alias", `struct H { xs: i32[] }
function main(): i32 {
    var a: i32[] = [1, 2, 3];
    var h: H = H { xs: a };
    var i: i32 = 0;
    while (i < 100) { a = a.with(0, 9 + i); i = i + 1; }
    if (h.xs[0] != 1) { return 1; }
    if (a[0] != 108) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 3},
	{"tuple-lit-alias", `function main(): i32 {
    var a: i32[] = [1, 2, 3];
    var t: (i32[], i32) = (a, 7);
    var i: i32 = 0;
    while (i < 100) { a = a.with(0, 9 + i); i = i + 1; }
    if (t.0[0] != 1) { return 1; }
    if (a[0] != 108) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 3},
	{"match-tuple-alias", `function main(): i32 {
    var a: i32[] = [1, 2, 3];
    var t: (i32[], i32) = (a, 7);
    var i: i32 = 0;
    while (i < 100) { a = a.with(0, 9 + i); i = i + 1; }
    match (t) {
        (xs, n) => { if (xs[0] != 1 || n != 7) { return 1; } }
    }
    if (a[0] != 108) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 3},
	{"nested-block-alias", `function main(): i32 {
    var a: i32[] = [1, 2, 3];
    var keep: i32 = 0;
    if (a[0] == 1) {
        var b: i32[] = a;
        var i: i32 = 0;
        while (i < 100) { a = a.with(0, 9 + i); i = i + 1; }
        keep = b[0];
    }
    if (keep != 1) { return 1; }
    if (a[0] != 108) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 2},
	// A holder created AFTER each update and kept across the back edge: the
	// buffer is shared at every update, so every update copies — the volume is
	// the program's, not the compiler's.
	{"backedge-alias", `function main(): i32 {
    var a: i32[] = [1, 2, 3];
    var keep: i32[] = [];
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < 100) {
        if (i > 0) { sum = sum + keep[0]; }
        a = a.with(0, 9 + i);
        var b: i32[] = a;
        keep = b;
        i = i + 1;
    }
    if (sum != 5742) { return 1; }
    if (a[0] != 108) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 101},
	// Holders the static alias scan did NOT credit: each of these wrote through
	// the alias before the count was read at run time.
	{"enum-payload-alias", `enum E { Hold(i32[]), Empty }
function main(): i32 {
    var a: i32[] = [1, 2, 3];
    var e: E = E.Hold(a);
    var i: i32 = 0;
    while (i < 100) { a = a.with(0, 9 + i); i = i + 1; }
    match (e) {
        E.Hold(xs) => { if (xs[0] != 1) { return 1; } },
        E.Empty => { return 3; }
    }
    if (a[0] != 108) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 3},
	{"option-payload-alias", `function main(): i32 {
    var a: i32[] = [1, 2, 3];
    var o: Option[i32[]] = Some(a);
    var i: i32 = 0;
    while (i < 100) { a = a.with(0, 9 + i); i = i + 1; }
    match (o) {
        Some(xs) => { if (xs[0] != 1) { return 1; } },
        None => { return 3; }
    }
    if (a[0] != 108) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 3},
	{"returned-param-alias", `@noinline
function id(xs: i32[]): i32[] { return xs; }
function main(): i32 {
    var a: i32[] = [1, 2, 3];
    var b: i32[] = id(a);
    var i: i32 = 0;
    while (i < 100) { a = a.with(0, 9 + i); i = i + 1; }
    if (b[0] != 1) { return 1; }
    if (a[0] != 108) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 2},
	{"callee-struct-alias", `struct H { xs: i32[] }
@noinline
function mk(xs: i32[]): H { return H { xs: xs }; }
function main(): i32 {
    var a: i32[] = [1, 2, 3];
    var h: H = mk(a);
    var i: i32 = 0;
    while (i < 100) { a = a.with(0, 9 + i); i = i + 1; }
    if (h.xs[0] != 1) { return 1; }
    if (a[0] != 108) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 3},
	{"call-returns-field-alias", `struct H { xs: i32[] }
@noinline
function get(h: H): i32[] { return h.xs; }
function main(): i32 {
    var h: H = H { xs: [1, 2, 3] };
    var a: i32[] = get(h);
    var i: i32 = 0;
    while (i < 100) { a = a.with(0, 9 + i); i = i + 1; }
    if (h.xs[0] != 1) { return 1; }
    if (a[0] != 108) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 3},
	{"method-returns-field-alias", `struct H { xs: i32[] }
@noinline
function (h: H) get(): i32[] { return h.xs; }
function main(): i32 {
    var h: H = H { xs: [1, 2, 3] };
    var a: i32[] = h.get();
    var i: i32 = 0;
    while (i < 100) { a = a.with(0, 9 + i); i = i + 1; }
    if (h.xs[0] != 1) { return 1; }
    if (a[0] != 108) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 3},
	{"elem-read-alias", `function main(): i32 {
    var outer: i32[][] = [[1, 2, 3], [4, 5]];
    var a: i32[] = outer[0];
    var i: i32 = 0;
    while (i < 100) { a = a.with(0, 9 + i); i = i + 1; }
    if (outer[0][0] != 1) { return 1; }
    if (a[0] != 108) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 4},
	// A row stored into a nested array by `.with` is held by the container, so
	// the container takes a count of it — the `.with` twin of the append arm's
	// retain (the row is not released: the nested array is a leak-only class).
	{"elem-store-alias", `function main(): i32 {
    var a: i32[] = [1, 2, 3];
    var outer: i32[][] = [[0], [0]];
    outer = outer.with(0, a);
    var i: i32 = 0;
    while (i < 100) { a = a.with(0, 9 + i); i = i + 1; }
    if (outer[0][0] != 1) { return 1; }
    if (a[0] != 108) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	// A foreach over the array the body rebinds reads the value held at entry.
	{"foreach-iter-rebound", `function main(): i32 {
    var a: i32[] = [1, 2, 3];
    var seen: i32 = 0;
    for x in a {
        seen = seen + x;
        a = a.with(0, 9);
        a = a.with(1, 9);
        a = a.with(2, 9);
    }
    if (seen != 6) { return 1; }
    if (a[0] != 9) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	// Projections the body rebinds: the hidden ownership flag copies the borrow
	// once and writes the owned replacement in place.
	{"foreach-row-projection", `function main(): i32 {
    var outer: i32[][] = [[1, 2, 3], [4, 5, 6]];
    var sum: i32 = 0;
    for row in outer {
        var i: i32 = 0;
        while (i < 100) { row = row.with(0, 9 + i); i = i + 1; }
        sum = sum + row[0];
    }
    if (outer[0][0] != 1 || outer[1][0] != 4) { return 1; }
    if (sum != 216) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 5},
	{"foreach-row-projection-u64", `function main(): i32 {
    var outer: u64[][] = [[1u64, 2u64], [3u64, 4u64]];
    var sum: u64 = 0u64;
    for row in outer {
        var i: i32 = 0;
        while (i < 50) { row = row.with(0, (i as u64) + 10u64); i = i + 1; }
        sum = sum + row[0] + row[1];
    }
    if (outer[0][0] != 1u64 || outer[1][0] != 3u64) { return 1; }
    if (sum != 124u64) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 5},
	{"enum-payload-projection", `enum E { Hold(i32[]), Empty }
function main(): i32 {
    var e: E = E.Hold([1, 2, 3]);
    var got: i32 = 0;
    match (e) {
        E.Hold(xs) => {
            var i: i32 = 0;
            while (i < 100) { xs = xs.with(0, 9 + i); i = i + 1; }
            got = xs[0];
        },
        E.Empty => { return 3; }
    }
    match (e) {
        E.Hold(ys) => { if (ys[0] != 1) { return 1; } },
        E.Empty => { return 4; }
    }
    if (got != 108) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 3},
	{"option-call-payload-projection", `@noinline
function mk(): Option[i32[]] { return Some([1, 2, 3]); }
function main(): i32 {
    var got: i32 = 0;
    match (mk()) {
        Some(xs) => {
            var i: i32 = 0;
            while (i < 100) { xs = xs.with(0, 9 + i); i = i + 1; }
            got = xs[0];
        },
        None => { return 3; }
    }
    if (got != 108) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 3},
	// An if-expression handing back a local: the value block is inlined, so
	// its mention of `a` is no capture and `a` stays a plain slot, and the
	// ident leaf takes the alias retain so `b` owns the reference its exit
	// sweep releases. Before: `a` was boxed into a capture cell for the whole
	// function and every update cloned into the cell with no release, 103 / 3.
	{"if-expression-alias", `function main(): i32 {
    var a: i32[] = [1, 2, 3];
    var c: i32[] = [4];
    var b: i32[] = if (a[0] == 1) { a } else { c };
    var i: i32 = 0;
    while (i < 100) { a = a.with(0, 9 + i); i = i + 1; }
    if (b[0] != 1) { return 1; }
    if (a[0] != 108) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 3},
	{"if-expression-fresh-arm", `@noinline
function mk(): i32[] { return [7, 8]; }
function main(): i32 {
    var a: i32[] = [1, 2, 3];
    var b: i32[] = if (a[0] == 1) { a } else { mk() };
    var i: i32 = 0;
    while (i < 100) { a = a.with(0, 9 + i); i = i + 1; }
    if (b[0] != 1) { return 1; }
    if (a[0] != 108) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 2},
	// The underflow is read after the owning frame's exit sweep, which is where
	// an unretained if-expression result was released a second time.
	{"if-expression-alias-exit-sweep", `@noinline
function exercise(): i32 {
    var a: i32[] = [1, 2, 3];
    var c: i32[] = [4];
    var b: i32[] = if (a[0] == 1) { a } else { c };
    if (b[0] != 1) { return 1; }
    if (a[0] != 1) { return 2; }
    return 0;
}
function main(): i32 {
    var r: i32 = exercise();
    if (r != 0) { return r; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 2},
	// The same second holder read out of a struct field, a nested-array element
	// and a tuple element: each is the container-read retain the `var` ladder
	// applies, and each exited 99 from the sweep before the leaf took it.
	{"if-expression-field-leaf-exit-sweep", `struct Box { mag: u64[], n: i32 }
@noinline
function exercise(): i32 {
    var box: Box = Box { mag: [1u64, 2u64], n: 0 };
    var d: u64[] = [4u64];
    var b: u64[] = if (box.n == 0) { box.mag } else { d };
    if (b[0] != 1u64) { return 1; }
    if (box.mag[0] != 1u64) { return 2; }
    return 0;
}
function main(): i32 {
    var r: i32 = exercise();
    if (r != 0) { return r; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 3},
	{"if-expression-i32-field-leaf-exit-sweep", `struct Box { items: i32[], n: i32 }
@noinline
function exercise(): i32 {
    var box: Box = Box { items: [1, 2], n: 0 };
    var d: i32[] = [4];
    var b: i32[] = if (box.n == 0) { box.items } else { d };
    if (b[0] != 1) { return 1; }
    if (box.items[0] != 1) { return 2; }
    return 0;
}
function main(): i32 {
    var r: i32 = exercise();
    if (r != 0) { return r; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 3},
	{"if-expression-boolean-field-leaf-exit-sweep", `struct Box { flags: boolean[], n: i32 }
@noinline
function exercise(): i32 {
    var box: Box = Box { flags: [true, false], n: 0 };
    var d: boolean[] = [false];
    var b: boolean[] = if (box.n == 0) { box.flags } else { d };
    if (!b[0]) { return 1; }
    if (!box.flags[0]) { return 2; }
    return 0;
}
function main(): i32 {
    var r: i32 = exercise();
    if (r != 0) { return r; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 3},
	{"if-expression-index-leaf-exit-sweep", `@noinline
function exercise(): i32 {
    var g: i32[][] = [[1, 2], [3]];
    var d: i32[] = [4];
    var b: i32[] = if (g.len() == 2) { g[0] } else { d };
    if (b[0] != 1) { return 1; }
    if (g[0][0] != 1) { return 2; }
    return 0;
}
function main(): i32 {
    var r: i32 = exercise();
    if (r != 0) { return r; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 4},
	// The indexed array is a nested-array STRUCT FIELD. The uncounted leaf let
	// the binding's sweep free a row the struct still holds; the struct is
	// handed back and a caller that allocates over the freed row reads 7 for
	// 1. Nested arrays are a leak-only class, so the exit code is the pin.
	{"if-expression-field-index-leaf-handback", `struct Box { grid: i32[][], n: i32 }
@noinline
function mk(): Box {
    var box: Box = Box { grid: [[1, 2], [3]], n: 2 };
    var d: i32[] = [4];
    var b: i32[] = if (box.n == 2) { box.grid[0] } else { d };
    if (b[0] != 1) { return Box { grid: [], n: 0 }; }
    return box;
}
@noinline
function churn(): i32 {
    var j1: i32[] = [7, 7];
    var j2: i32[] = [8, 8];
    var j3: i32[] = [9, 9];
    return j1[0] + j2[0] + j3[0];
}
function main(): i32 {
    var box: Box = mk();
    if (churn() != 24) { return 3; }
    if (box.grid[0][0] != 1 || box.grid[0][1] != 2) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	// A doubly-indexed element, the same read one level deeper.
	{"if-expression-nested-index-leaf-handback", `@noinline
function mk(): i32[][][] {
    var m: i32[][][] = [[[1, 2], [3]], [[4]]];
    var d: i32[] = [5];
    var b: i32[] = if (m.len() == 2) { m[0][1] } else { d };
    if (b[0] != 3) { return []; }
    return m;
}
@noinline
function churn(): i32 {
    var j1: i32[] = [7];
    var j2: i32[] = [8];
    var j3: i32[] = [9];
    return j1[0] + j2[0] + j3[0];
}
function main(): i32 {
    var m: i32[][][] = mk();
    if (churn() != 24) { return 3; }
    if (m[0][1][0] != 3) { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"if-expression-tuple-leaf-exit-sweep", `@noinline
function exercise(): i32 {
    var t: (i32[], i32) = ([1, 2], 7);
    var d: i32[] = [4];
    var b: i32[] = if (t.1 == 7) { t.0 } else { d };
    if (b[0] != 1) { return 1; }
    if (t.0[0] != 1) { return 2; }
    return 0;
}
function main(): i32 {
    var r: i32 = exercise();
    if (r != 0) { return r; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 3},
	// A string[] keeps the static clone: its elements are counted references
	// the scalar path does not admit.
	{"string-elems-excluded", `function main(): i32 {
    var a: string[] = ["x" + "1", "y" + "2"];
    var b: string[] = a;
    var i: i32 = 0;
    while (i < 3) { a = a.with(0, "z" + "3"); i = i + 1; }
    if (b[0] != "x1") { return 1; }
    if (a[0] != "z3") { return 2; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
}

// withCowCensus reads a run's leakcheck summary and holds it to the case's
// contract: balanced at live 0, and no more allocations than the bound. A zero
// bound pins the exit code only — the case's holder is a leak-only class whose
// census this change does not own.
func withCowCensus(t *testing.T, name, stderr string, maxAllocs int64) {
	t.Helper()
	if maxAllocs == 0 {
		return
	}
	summary := leakSummaryLine(stderr)
	if summary == "" {
		t.Fatalf("%s: no leakcheck summary on stderr: %q", name, stderr)
	}
	var allocs, frees, live int64
	if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
		t.Fatalf("%s: parse %q: %v", name, summary, err)
	}
	if allocs != frees || live != 0 {
		t.Errorf("%s: %s — the superseded buffer must be released as the copy replaces it", name, summary)
	}
	if allocs > maxAllocs {
		t.Errorf("%s: %s — allocs above %d: one copy per update instead of one per share", name, summary, maxAllocs)
	}
}

func withCowStrictLeakEnv() []string {
	return []string{"PATH=/usr/bin:/bin", "FERN_STRICT_IR=1", "FERN_LEAKCHECK=1"}
}

func TestSelfHostWithCowIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	for _, tc := range withCowIRCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			if want != 0 {
				t.Fatalf("interpreter = %d, want 0: the case does not hold on the oracle", want)
			}
			asm := hevCompile(t, runner, driverBin, tc.src, withCowStrictLeakEnv())
			bin := buildBin(t, gcc, dir, "withcow_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, bin)
			if exit != want {
				t.Fatalf("exit = %d, want %d (interp oracle)\n%s", exit, want, stderr)
			}
			withCowCensus(t, tc.name, stderr, tc.maxAllocs)
		})
	}
}

func TestSelfHostWithCowIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")
	for _, tc := range withCowIRCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			if want != 0 {
				t.Fatalf("interpreter = %d, want 0: the case does not hold on the oracle", want)
			}
			asm := string(runCaptureEnv(t, x86runner, driverBin, []byte(tc.src), withCowStrictLeakEnv(), "-target", "arm64-linux"))
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, "withcow_"+tc.name, asm)
			cmd := runArm64Bin(qemu, bin)
			var errBuf strings.Builder
			cmd.Stderr = &errBuf
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Fatalf("exit = %d, want %d (interp oracle)\n%s", code, want, errBuf.String())
			}
			withCowCensus(t, tc.name, errBuf.String(), tc.maxAllocs)
		})
	}
}

func TestSelfHostWithCowIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm .with copy-on-write e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "wasmdriver")
	for _, tc := range withCowIRCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			if want != 0 {
				t.Fatalf("interpreter = %d, want 0: the case does not hold on the oracle", want)
			}
			wat := wasmLcCompile(t, runner, driverBin, tc.src, []string{"FERN_STRICT_IR=1", "FERN_LEAKCHECK=1"})
			stderr, exit := wasmLcRun(t, dir, "withcow_"+tc.name, wat)
			if exit != want {
				t.Fatalf("exit = %d, want %d (interp oracle)\n%s", exit, want, stderr)
			}
			withCowCensus(t, tc.name, stderr, tc.maxAllocs)
		})
	}
}
