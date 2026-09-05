package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// #8224: a struct-FIELD-receiver `.append` used to clone the whole array before
// growing the clone, so `S { ...s, xs: s.xs.append(v) }` — the state-threading
// shape every self-host emitter is built out of — was O(n) per append and O(n^2)
// per built array. field_append_inplace_sites_of now proves, per function, which
// of those appends no later read can observe and lets them grow the field's own
// buffer.
//
// The two halves are only sound together, so both are pinned here. The CALLEE
// half is the admission (the shape cases below). The CALLER half is the #4873
// grow bracket extended to a struct argument's array-field buffers: a callee
// that may grow them in place must not do so through a container the caller
// still reads. Every case here is differential — the expectation is the
// interpreter's answer, never a written-down number — because it is the ANSWER
// that diverges when either half is missing.
//
// SPARE CAPACITY is what makes an in-place grow possible at all, so each case
// threads its container through several appends before the read it checks.
//
// `S { ...s, xs: s.xs.with(i, v) }` rides the same admission (#8419): the store
// lands in the field's own buffer, gated on the buffer's own count since
// arr_set has no gate of its own, and the caller half brackets it exactly as
// it brackets a grow. The `with-` cases below pin both halves for the store.
var selfHostFieldAppendCases = []struct {
	name string
	src  string
}{
	// The measured shape: a receiver method returning a spread literal that
	// overrides the appended field. Threading it is the whole point, and the
	// answer must not change.
	{"spread-threading", `
struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(op: i32): St {
    var nctrl: i32 = s.ctrl;
    if (op == 1) { nctrl = s.ctrl + 1; }
    return St { ...s, ops: s.ops.append(op), ctrl: nctrl };
}
function main(): i32 {
    var s: St = St { ops: [], ctrl: 0 };
    var i: i32 = 0;
    while (i < 30) { s = s.emit(i); i = i + 1; }
    var sum: i32 = 0;
    for v in s.ops { sum = sum + v; }
    return s.ops.len() + s.ctrl + (sum % 7);
}`},
	// The caller-side hole through a METHOD receiver: `a` survives the call, so
	// its buffer must not grow under it.
	{"method-recv-survives", `
struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(op: i32): St { return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl }; }
function main(): i32 {
    var a: St = St { ops: [], ctrl: 0 };
    var i: i32 = 0;
    while (i < 5) { a = a.emit(i); i = i + 1; }
    var b: St = a.emit(9);
    return a.ops.len() * 10 + b.ops.len() + b.ops[5];
}`},
	// The same hole through a free function's struct PARAMETER.
	{"free-param-survives", `
struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(op: i32): St { return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl }; }
function bump(s: St, v: i32): St { return St { ...s, ops: s.ops.append(v), ctrl: s.ctrl }; }
function main(): i32 {
    var a: St = St { ops: [], ctrl: 0 };
    var i: i32 = 0;
    while (i < 5) { a = a.emit(i); i = i + 1; }
    var c: St = bump(a, 7);
    return a.ops.len() * 10 + c.ops.len() + c.ops[5];
}`},
	// Transitive: bump2 rebinds its own parameter from the call, which is the
	// dying shape the bracket exempts — so the growth escapes to ITS caller and
	// the may-grow flag has to propagate through the fixpoint.
	{"transitive-pass-through", `
struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(op: i32): St { return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl }; }
function bump(s: St, v: i32): St { return St { ...s, ops: s.ops.append(v), ctrl: s.ctrl }; }
function bump2(s: St, v: i32): St { s = bump(s, v); return s; }
function main(): i32 {
    var a: St = St { ops: [], ctrl: 0 };
    var i: i32 = 0;
    while (i < 5) { a = a.emit(i); i = i + 1; }
    var d: St = bump2(a, 6);
    return a.ops.len() * 10 + d.ops.len() + d.ops[5];
}`},
	// The SOLE-OCCURRENCE death (#6048): `p` is read exactly once in bump3's
	// whole body, so the caller-side bracket skips it there — and the growth
	// escapes to bump3's OWN caller, which means the may-grow set has to
	// propagate through that shape as well as through the self-reassign one.
	{"sole-occurrence-pass-through", `
struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(op: i32): St { return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl }; }
function bump(s: St, v: i32): St { return St { ...s, ops: s.ops.append(v), ctrl: s.ctrl }; }
function bump3(p: St): St { var t: St = bump(p, 4); return t; }
function main(): i32 {
    var a: St = St { ops: [], ctrl: 0 };
    var i: i32 = 0;
    while (i < 5) { a = a.emit(i); i = i + 1; }
    var e: St = bump3(a);
    return a.ops.len() * 10 + e.ops.len() + e.ops[5];
}`},
	// The bracket's release must name the buffer its retain named. A callee that
	// hands back the caller's own box — an empty pass-through — leaves that box
	// freed by the dying-donor release and handed straight back to the next
	// call's result-box allocation, so a release that re-reads `b.ops` reads the
	// buffer that call just grew (#8224).
	{"passthrough-then-bracketed-grow", `
struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(op: i32): St { return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl + 1 }; }
function passthru(n: i32, s: St): St {
    var t: St = s;
    var i: i32 = 0;
    while (i < n) { t = t.emit(i); i = i + 1; }
    return t;
}
function outer(a: St): i32 {
    var b: St = passthru(0, a);
    var c: St = b.emit(9);
    return b.ops.len() * 10 + c.ops.len() + c.ops[6];
}
function main(): i32 {
    var a: St = St { ops: [], ctrl: 0 };
    var i: i32 = 0;
    while (i < 6) { a = a.emit(i); i = i + 1; }
    return outer(a);
}`},
	// A struct local bound from a FIELD READ is not a container this frame owns,
	// so the dying `aug = f(aug)` rebind must NOT be exempt from the bracket:
	// growing one of aug's array fields in place reaches through it into `sg`,
	// two levels deep, where the one-level may-grow-fields mask cannot follow.
	// The `.with` on one of the two co-indexed arrays is what makes the damage
	// visible rather than merely wrong — it re-clones `next` at cap == len, so
	// the next append reallocs `next` while `rows` still has spare capacity and
	// grows in place, and the container is left with two arrays one entry apart
	// (#8224; the shape is irlower's own `var aug = sg.struct_ret_fns`).
	{"field-read-alias-refuses-exemption", `
struct Reg { rows: i32[], next: i32[] }
struct Sigs { reg: Reg, tag: i32 }
function append_row(r: Reg, v: i32): Reg {
    var rows: i32[] = r.rows.append(v);
    var next: i32[] = r.next.append(0 - 1);
    next = next.with(0, v);
    return Reg { rows: rows, next: next };
}
function grow_from_field(sg: Sigs): i32 {
    var aug: Reg = sg.reg;
    var i: i32 = 0;
    while (i < 5) { aug = append_row(aug, i); i = i + 1; }
    if (sg.reg.rows.len() != 3) { return 71; }
    if (sg.reg.next.len() != 3) { return 72; }
    if (aug.rows.len() != 8) { return 73; }
    if (aug.next.len() != 8) { return 74; }
    return 7;
}
function main(): i32 {
    var rows: i32[] = [];
    var next: i32[] = [];
    var k: i32 = 0;
    while (k < 3) { rows = rows.append(k); next = next.append(0 - 1); k = k + 1; }
    var sg: Sigs = Sigs { reg: Reg { rows: rows, next: next }, tag: 0 };
    return grow_from_field(sg);
}`},
	// A struct argument reached through a FIELD chain: the bracket has to walk
	// the container's field hops to the inner struct's buffer.
	{"nested-field-argument", `
struct Inner { xs: i32[] }
struct Outer { inner: Inner, tag: i32 }
function push(i: Inner, v: i32): Inner { return Inner { xs: i.xs.append(v) }; }
function main(): i32 {
    var o: Outer = Outer { inner: Inner { xs: [] }, tag: 0 };
    var k: i32 = 0;
    while (k < 5) { o = Outer { ...o, inner: push(o.inner, k) }; k = k + 1; }
    var again: Inner = push(o.inner, 8);
    return o.inner.xs.len() * 10 + again.xs.len() + again.xs[5];
}`},
	// The same field READ AGAIN inside the literal the append feeds: the clone
	// must stay, or `n` reads the grown length.
	{"same-field-read-forces-clone", `
struct St { ops: i32[], n: i32 }
function grow(s: St, v: i32): St { return St { ops: s.ops.append(v), n: s.ops.len() }; }
function main(): i32 {
    var a: St = St { ops: [], n: 0 };
    var i: i32 = 0;
    while (i < 5) { a = St { ops: a.ops.append(i), n: a.n }; i = i + 1; }
    var b: St = grow(a, 9);
    return a.ops.len() * 10 + b.ops.len() + b.n;
}`},
	// A BARE read of the container in the same expression hands the whole thing
	// over, so the buffer stays readable through it.
	{"bare-read-forces-clone", `
struct St { ops: i32[], n: i32 }
function total(s: St): i32 { var t: i32 = 0; for v in s.ops { t = t + v; } return t; }
function grow(s: St, v: i32): St { return St { ops: s.ops.append(v), n: total(s) }; }
function main(): i32 {
    var a: St = St { ops: [], n: 0 };
    var i: i32 = 0;
    while (i < 5) { a = St { ops: a.ops.append(i), n: a.n }; i = i + 1; }
    var b: St = grow(a, 9);
    return a.ops.len() * 10 + b.ops.len() + b.n;
}`},
	// A pointer-element field: the grown buffer holds string boxes the container
	// still owns, so this pins the element retain the clone form also pays.
	{"string-field", `
struct Bag { names: string[], n: i32 }
function (b: Bag) add(s: string): Bag { return Bag { ...b, names: b.names.append(s), n: b.n + 1 }; }
function main(): i32 {
    var b: Bag = Bag { names: [], n: 0 };
    var i: i32 = 0;
    while (i < 6) { b = b.add("ab"); i = i + 1; }
    var c: Bag = b.add("cdef");
    var t: i32 = 0;
    for s in b.names { t = t + s.len(); }
    for s in c.names { t = t + s.len(); }
    return t + b.names.len() + c.names.len();
}`},
	// A local — not a parameter — as the container: its box is this frame's, so
	// the analysis must refuse and the clone must stay.
	{"local-container-refused", `
struct St { ops: i32[], n: i32 }
function main(): i32 {
    var a: St = St { ops: [], n: 0 };
    var i: i32 = 0;
    while (i < 5) { a = St { ops: a.ops.append(i), n: a.n }; i = i + 1; }
    var keep: St = a;
    var b: St = St { ops: a.ops.append(9), n: a.n };
    return keep.ops.len() * 10 + b.ops.len() + a.ops.len();
}`},
	// The RETURN-position death (#8254). `return f(a, v)` kills a's BINDING, so
	// the caller-side grow bracket is withdrawn — but not this frame's claim on
	// the buffers inside a: the exit sweep still deep-drops a's rc fields, and it
	// runs after the call. With no bracket the callee grows a.ops in place and
	// hands the result the very buffer that sweep frees. It stays invisible until
	// the freed block is handed out again, which is what the appends after the
	// call do here. `f` takes no spread, so its parameter reads as a pure borrow
	// and stays borrowable — which is exactly what keeps a credited and its drop
	// deep.
	{"return-position-death-frees-the-grown-buffer", `
struct St { ops: i32[], n: i32 }
function f(s: St, v: i32): St { return St { ops: s.ops.append(v), n: 1 }; }
function mk(n: i32): St {
    var a: St = St { ops: [], n: 0 };
    var i: i32 = 0;
    while (i < n) { a = St { ops: a.ops.append(i + 1), n: 0 }; i = i + 1; }
    return f(a, 999);
}
function main(): i32 {
    var r: St = mk(9);
    var junk: i32[] = [];
    var k: i32 = 0;
    while (k < 20) { junk = junk.append(0 - 5); k = k + 1; }
    var t: i32 = 0;
    for v in r.ops { t = t + v; }
    return (t + junk.len()) % 251;
}`},
	// The same death with the container threaded through a spread literal, which
	// keeps the whole shape reachable from the hot form.
	{"return-position-death", `
struct St { ops: i32[], ctrl: i32 }
function bump(s: St, v: i32): St { return St { ...s, ops: s.ops.append(v), ctrl: s.ctrl + 1 }; }
function make(n: i32): St {
    var a: St = St { ops: [], ctrl: 0 };
    var i: i32 = 0;
    while (i < n) { a = bump(a, i); i = i + 1; }
    return bump(a, 99);
}
function main(): i32 {
    var r: St = make(6);
    var t: i32 = 0;
    for v in r.ops { t = t + v; }
    return r.ops.len() * 10 + (t % 97) + r.ctrl;
}`},
	// An `own` container root: the callee, not the caller, holds the box. What
	// lets the grown buffer travel out uncounted is #8274's move-out — the
	// identity arm stores NULL into the source field, so the `own` param's exit
	// OWNREL walk finds nothing to free. It is NOT that parameters are exempt
	// from the sweep: the params loop runs before the from-n_params loops and
	// does emit a deep field drop under the box's rc==1 gate (#8254).
	//
	// This case's own route is the SPREAD base, which `moves_fields_expr` marks
	// moved, so its row is box-only `OWNRELB:` — it does not exercise the deep
	// walk. `own_self_reassign_move` in conformance is the case that does.
	{"own-param-container", `
struct St { ops: i32[], ctrl: i32 }
function bump(own s: St, v: i32): St { return St { ...s, ops: s.ops.append(v), ctrl: s.ctrl + 1 }; }
function main(): i32 {
    var a: St = St { ops: [], ctrl: 0 };
    var i: i32 = 0;
    while (i < 10) { a = bump(a, i); i = i + 1; }
    var t: i32 = 0;
    for v in a.ops { t = t + v; }
    return a.ops.len() + a.ctrl + (t % 29);
}`},
	// A struct-ELEMENT field, whose reclaim walks each element box before the
	// buffer (__field_reclaim_<T>'s arrarr_free arm). The cow compare that skips
	// a pointer-equal field is what keeps that walk off the grown buffer.
	{"struct-array-field", `
struct Row { k: i32, v: i32 }
struct Tab { rows: Row[], n: i32 }
function (t: Tab) put(k: i32, v: i32): Tab { return Tab { ...t, rows: t.rows.append(Row { k: k, v: v }), n: t.n + 1 }; }
function main(): i32 {
    var t: Tab = Tab { rows: [], n: 0 };
    var i: i32 = 0;
    while (i < 12) { t = t.put(i, i * 2); i = i + 1; }
    var s: i32 = 0;
    for r in t.rows { s = s + r.k + r.v; }
    return t.rows.len() + t.n + (s % 53);
}`},
	// An i64[] field takes the 8-byte-slot push helper, which wasm dispatches
	// separately from the i32 one.
	{"i64-field", `
struct W { xs: i64[], n: i32 }
function (w: W) put(v: i64): W { return W { ...w, xs: w.xs.append(v), n: w.n + 1 }; }
function main(): i32 {
    var w: W = W { xs: [], n: 0 };
    var i: i32 = 0;
    while (i < 6) { w = w.put(3i64); i = i + 1; }
    var t: i64 = 0i64;
    for v in w.xs { t = t + v; }
    return (t as i32) + w.xs.len() + w.n;
}`},
	// The grow MOVES the field out of the root's box. Each shape below releases
	// that box, or the value, by a path that finds the field: the pre-move
	// lowering left both naming one rc 1 buffer, and the first release freed it
	// under the other (#8224). Struct elements, so a freed buffer's element boxes
	// are recycled by `churn` and the read that follows sees 7 where it wrote
	// 1..4 — or __rc_underflow() reports the double free.
	//
	// A return-position receiver whose literal-init local keeps its DEEP sweep:
	// the receiver-borrow registry clears `emit` as a pure borrow, so `ms` is not
	// NODEEP, and `return ms.emit(4)` deep-drops it after the call.
	{"return-position-deep-swept-receiver", `
struct P { x: i32 }
struct S { ops: P[], n: i32 }
function (self: S) emit(v: i32): S { return S { ops: self.ops.append(P { x: v }), n: self.n + 1 }; }
function build(): S {
    var ms: S = S { ops: [], n: 0 };
    ms = ms.emit(1);
    ms = ms.emit(2);
    ms = ms.emit(3);
    return ms.emit(4);
}
function churn(k: i32): i32 {
    var a: P[] = [];
    var i: i32 = 0;
    while (i < k) { a = a.append(P { x: 7 }); i = i + 1; }
    return a.len();
}
function main(): i32 {
    var r: S = build();
    var c: i32 = churn(64);
    if (r.ops.len() != 4 || c != 64 || r.n != 4) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return r.ops[0].x * 27 + r.ops[1].x * 9 + r.ops[2].x * 3 + r.ops[3].x;
}`},
	// An OWN parameter is released by the callee's own exit sweep, deep
	// (own_struct_param_release_rows_of). The conformance case
	// own_self_reassign_move hung on this shape.
	{"own-param-exit-release", `
struct P { x: i32 }
struct S { ops: P[], n: i32 }
function push(own b: S, v: i32): S {
    var ys: P[] = b.ops.append(P { x: v });
    return S { ops: ys, n: b.n + 1 };
}
function churn(k: i32): i32 {
    var a: P[] = [];
    var i: i32 = 0;
    while (i < k) { a = a.append(P { x: 7 }); i = i + 1; }
    return a.len();
}
function main(): i32 {
    var a: S = S { ops: [], n: 0 };
    var i: i32 = 1;
    while (i < 5) { a = push(a, i); i = i + 1; }
    var c: i32 = churn(64);
    if (a.ops.len() != 4 || c != 64 || a.n != 4) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return a.ops[0].x * 27 + a.ops[1].x * 9 + a.ops[2].x * 3 + a.ops[3].x;
}`},
	// The same exit release, reached through a callee that grows the own
	// param's field on the caller's behalf: `s` occurs once in g, so no bracket
	// forces the copy there, and g's sweep still deep-drops s.
	{"own-param-passed-once-to-grower", `
struct P { x: i32 }
struct S { ops: P[], n: i32 }
function h(s: S, v: i32): S { return S { ops: s.ops.append(P { x: v }), n: s.n + 1 }; }
function g(own s: S, v: i32): S { return h(s, v); }
function churn(k: i32): i32 {
    var a: P[] = [];
    var i: i32 = 0;
    while (i < k) { a = a.append(P { x: 7 }); i = i + 1; }
    return a.len();
}
function main(): i32 {
    var a: S = S { ops: [], n: 0 };
    var i: i32 = 1;
    while (i < 5) { a = g(a, i); i = i + 1; }
    var c: i32 = churn(64);
    if (a.ops.len() != 4 || c != 64 || a.n != 4) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return a.ops[0].x * 27 + a.ops[1].x * 9 + a.ops[2].x * 3 + a.ops[3].x;
}`},
	// The receiver spelling of the previous case.
	{"own-param-receiver-of-grower", `
struct P { x: i32 }
struct S { ops: P[], n: i32 }
function (self: S) emit(v: i32): S { return S { ops: self.ops.append(P { x: v }), n: self.n + 1 }; }
function g(own s: S, v: i32): S { return s.emit(v); }
function churn(k: i32): i32 {
    var a: P[] = [];
    var i: i32 = 0;
    while (i < k) { a = a.append(P { x: 7 }); i = i + 1; }
    return a.len();
}
function main(): i32 {
    var a: S = S { ops: [], n: 0 };
    var i: i32 = 1;
    while (i < 5) { a = g(a, i); i = i + 1; }
    var c: i32 = churn(64);
    if (a.ops.len() != 4 || c != 64 || a.n != 4) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return a.ops[0].x * 27 + a.ops[1].x * 9 + a.ops[2].x * 3 + a.ops[3].x;
}`},
	// The VALUE's holder releases: `ys` is swept at f's exit, and the caller's
	// rebind then reclaims the same buffer out of the superseded box's field.
	{"value-local-swept-before-caller-reclaim", `
struct P { x: i32 }
struct S { ops: P[], n: i32 }
function f(s: S, v: i32): S {
    var ys: P[] = s.ops.append(P { x: v });
    var n: i32 = ys.len();
    return S { ops: [], n: n };
}
function grow(s: S, v: i32): S { return S { ops: s.ops.append(P { x: v }), n: s.n + 1 }; }
function churn(k: i32): i32 {
    var a: P[] = [];
    var i: i32 = 0;
    while (i < k) { a = a.append(P { x: 7 }); i = i + 1; }
    return a.len();
}
function main(): i32 {
    var ms: S = S { ops: [], n: 0 };
    ms = grow(ms, 1);
    ms = grow(ms, 2);
    ms = grow(ms, 3);
    ms = f(ms, 4);
    var c: i32 = churn(64);
    if (c != 64 || ms.ops.len() != 0) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return ms.n;
}`},
	// A second NAME for the root's box: `var cur = s` inside a walk that
	// recurses with `cur` and rebinds it per statement, the checker's slc_walk
	// shape. The self-reassign exempts the bracket and the alias scan does not
	// see a bare ident of a parameter, so the callee's move would null `names`
	// in a box the outer frames still read (segfault in the self-compiled
	// checker's Scope.lookup). The box's count is what tells the site to copy.
	{"aliased-root-box-copies", `
struct Sc { names: i32[], n: i32 }
function (s: Sc) bind(v: i32): Sc {
    var ns: i32[] = s.names.append(v);
    return Sc { names: ns, n: s.n + 1 };
}
function step(v: i32, s: Sc): Sc { return s.bind(v); }
function walk(s: Sc, depth: i32): i32 {
    var cur: Sc = s;
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        if (depth > 0) { acc = acc + walk(cur, depth - 1); }
        cur = step(i, cur);
        i = i + 1;
    }
    return acc + cur.names.len() * 3 + cur.n;
}
function main(): i32 {
    var s0: Sc = Sc { names: [], n: 0 };
    s0 = step(1, s0);
    s0 = step(2, s0);
    s0 = step(3, s0);
    var r: i32 = walk(s0, 2);
    if (s0.names.len() != 3) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return (r + s0.names[2]) % 200;
}`},
	// The `.with` twin's measured shape: the x86 assembler's label table threads
	// three bucket arrays through `X86Asm { ...a, lab_head: a.lab_head.with(b, at) }`
	// per label, and each one cloned the array (#8419).
	{"with-spread-threading", `
struct St { tab: i32[], n: i32 }
function (s: St) put(i: i32, v: i32): St { return St { ...s, tab: s.tab.with(i, v), n: s.n + 1 }; }
function main(): i32 {
    var s: St = St { tab: [0, 0, 0, 0, 0, 0, 0, 0], n: 0 };
    var i: i32 = 0;
    while (i < 40) { s = s.put(i % 8, i); i = i + 1; }
    var sum: i32 = 0;
    for v in s.tab { sum = sum + v; }
    return (s.n + sum) % 113;
}`},
	// The caller-side hole for the store: `a` survives the call, so its buffer
	// must not be written under it.
	{"with-method-recv-survives", `
struct St { tab: i32[], n: i32 }
function (s: St) put(i: i32, v: i32): St { return St { ...s, tab: s.tab.with(i, v), n: s.n + 1 }; }
function main(): i32 {
    var a: St = St { tab: [1, 2, 3, 4], n: 0 };
    a = a.put(0, 5);
    var b: St = a.put(2, 9);
    return a.tab[2] * 10 + b.tab[2] + a.tab[0];
}`},
	// The label table itself: an `own` root, a rebind in each branch of an
	// `if` and a third after it — three body-scope hosts, each of its own
	// field — and a duplicate name still resolves to its first entry.
	{"with-own-param-label-table", `
struct Tab { names: string[], head: i32[], tail: i32[], next: i32[] }
function bucket(name: string): i32 { return name.len() % 4; }
function add(own a: Tab, name: string): Tab {
    var at: i32 = a.names.len();
    var b: i32 = bucket(name);
    var prev: i32 = a.tail[b];
    a = Tab { ...a, names: a.names.append(name) };
    a = Tab { ...a, next: a.next.append(0 - 1) };
    if (prev < 0) {
        a = Tab { ...a, head: a.head.with(b, at) };
    } else {
        a = Tab { ...a, next: a.next.with(prev, at) };
    }
    a = Tab { ...a, tail: a.tail.with(b, at) };
    return a;
}
function lookup(t: Tab, name: string): i32 {
    var i: i32 = t.head[bucket(name)];
    while (i >= 0) {
        if (t.names[i] == name) { return i; }
        i = t.next[i];
    }
    return 0 - 1;
}
function main(): i32 {
    var t: Tab = Tab { names: [], head: [0 - 1, 0 - 1, 0 - 1, 0 - 1], tail: [0 - 1, 0 - 1, 0 - 1, 0 - 1], next: [] };
    t = add(t, "a");
    t = add(t, "bb");
    t = add(t, "ccc");
    t = add(t, "dddd");
    t = add(t, "eeeee");
    t = add(t, "ff");
    t = add(t, "a");
    return lookup(t, "a") * 100 + lookup(t, "eeeee") * 10 + lookup(t, "ff") + lookup(t, "zz") + 1;
}`},
	// The same field READ AGAIN inside the literal the store feeds: the clone
	// must stay, or `n` reads the new element.
	{"with-same-field-read-forces-clone", `
struct St { tab: i32[], n: i32 }
function put(s: St, v: i32): St { return St { tab: s.tab.with(0, v), n: s.tab[0] }; }
function main(): i32 {
    var a: St = St { tab: [3, 4], n: 0 };
    var b: St = put(a, 9);
    return (a.tab[0] * 100 + b.tab[0] * 10 + b.n) % 101;
}`},
	// An i64[] field takes the 8-byte store.
	{"with-i64-field", `
struct W { xs: i64[], n: i32 }
function (w: W) put(i: i32, v: i64): W { return W { ...w, xs: w.xs.with(i, v), n: w.n + 1 }; }
function main(): i32 {
    var w: W = W { xs: [0i64, 0i64, 0i64], n: 0 };
    var i: i32 = 0;
    while (i < 9) { w = w.put(i % 3, (i as i64) * 5i64); i = i + 1; }
    var t: i64 = 0i64;
    for v in w.xs { t = t + v; }
    return (t as i32) + w.n;
}`},
	// A second NAME for the root's box, with a store instead of a grow: the
	// box's count is what tells the site to copy.
	{"with-aliased-root-box-copies", `
struct Sc { tab: i32[], n: i32 }
function (s: Sc) put(v: i32): Sc { return Sc { ...s, tab: s.tab.with(0, v), n: s.n + 1 }; }
function step(v: i32, s: Sc): Sc { return s.put(v); }
function walk(s: Sc, depth: i32): i32 {
    var cur: Sc = s;
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        if (depth > 0) { acc = acc + walk(cur, depth - 1); }
        cur = step(i + 10 * depth, cur);
        i = i + 1;
    }
    return acc + cur.tab[0] + cur.n;
}
function main(): i32 {
    var s0: Sc = Sc { tab: [7, 7], n: 0 };
    s0 = step(1, s0);
    var r: i32 = walk(s0, 2);
    if (s0.tab[0] != 1) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return r % 113;
}`},
	// The hash-index shape (#8485): the bucket array's LENGTH and one of its
	// ELEMENTS are read before the store that rewrites a third slot. Both reads
	// are sequenced before it and yield scalars, so the store lands in the
	// field's own buffer — and the answers must not move.
	{"with-prior-scalar-field-reads", `
struct RefSet { names: string[], head: i32[], next: i32[] }
function bucket(name: string, n: i32): i32 { return name.len() % n; }
function add(rs: RefSet, name: string): RefSet {
    var bk: i32 = bucket(name, rs.head.len());
    var names: string[] = rs.names.append(name);
    var next: i32[] = rs.next.append(rs.head[bk]);
    var head: i32[] = rs.head.with(bk, names.len() - 1);
    return RefSet { names: names, head: head, next: next };
}
function has(rs: RefSet, name: string): boolean {
    var i: i32 = rs.head[bucket(name, rs.head.len())];
    while (i >= 0) {
        if (rs.names[i] == name) { return true; }
        i = rs.next[i];
    }
    return false;
}
function main(): i32 {
    var rs: RefSet = RefSet { names: [], head: [0 - 1, 0 - 1, 0 - 1, 0 - 1, 0 - 1], next: [] };
    rs = add(rs, "a");
    rs = add(rs, "bb");
    rs = add(rs, "ccc");
    rs = add(rs, "dddd");
    rs = add(rs, "eeeee");
    rs = add(rs, "ffffff");
    rs = add(rs, "a");
    var hits: i32 = 0;
    if (has(rs, "a")) { hits = hits + 1; }
    if (has(rs, "eeeee")) { hits = hits + 10; }
    if (has(rs, "zz")) { hits = hits + 100; }
    if (rs.names.len() != 7) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return hits + rs.head[bucket("a", 5)];
}`},
	// The same shape with the root SURVIVING the call: the caller-side bracket
	// is what must send the store down its copy path here.
	{"with-prior-reads-root-survives", `
struct RefSet { names: string[], head: i32[], next: i32[] }
function bucket(name: string, n: i32): i32 { return name.len() % n; }
function add(rs: RefSet, name: string): RefSet {
    var bk: i32 = bucket(name, rs.head.len());
    var names: string[] = rs.names.append(name);
    var next: i32[] = rs.next.append(rs.head[bk]);
    var head: i32[] = rs.head.with(bk, names.len() - 1);
    return RefSet { names: names, head: head, next: next };
}
function main(): i32 {
    var a: RefSet = RefSet { names: [], head: [0 - 1, 0 - 1, 0 - 1, 0 - 1], next: [] };
    a = add(a, "p");
    a = add(a, "qq");
    var b: RefSet = add(a, "rrr");
    if (__rc_underflow_count() != 0) { return 99; }
    return a.names.len() * 10 + b.names.len() + a.head[3] + b.head[3];
}`},
	// A body-scope host INSIDE A LOOP runs again with the same root, and the
	// grow has moved the field out of it — so the site keeps the clone form.
	// The root arrives as a fresh call result at an `own` position: no caller
	// bracket can reach it, so its buffer is at rc 1 and the first push would
	// take the identity arm. Without the refusal the second iteration pushes
	// onto the moved-out field (segfault on the stubbed rule).
	{"body-host-in-loop-clones", `
struct S { ops: i32[], n: i32 }
function mk(): S {
    var s: S = S { ops: [], n: 0 };
    var i: i32 = 0;
    while (i < 3) { s = S { ...s, ops: s.ops.append(i) }; i = i + 1; }
    return s;
}
function tally(own s: S, k: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < k) {
        var ys: i32[] = s.ops.append(i);
        t = t + ys.len() + ys[ys.len() - 1];
        i = i + 1;
    }
    return t;
}
function main(): i32 {
    return tally(mk(), 4);
}`},
}

// TestSelfHostFieldAppendInPlaceX86_64 — the production x86-64 IR path against
// the interpreter oracle.
func TestSelfHostFieldAppendInPlaceX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostFieldAppendCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "fai_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostFieldAppendInPlaceArm64 — the same cases through the arm64 emit.
// The decision is shared irlower analysis, so this leg guards the two register
// backends agreeing about the grow helper's uniqueness gate.
func TestSelfHostFieldAppendInPlaceArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostFieldAppendCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, "fai_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostFieldAppendInPlaceWasmIR — the wasm-IR leg, which reaches
// $__fern_arr_push_i64 for the 8-byte-slot field.
func TestSelfHostFieldAppendInPlaceWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm-IR field-append e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range selfHostFieldAppendCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			wat := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			if len(wat) == 0 {
				t.Fatal("self-host wasm compiler emitted 0 bytes")
			}
			watFile := filepath.Join(dir, "fai_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// The analysis itself, read off the emitted asm: an admitted append grows the
// field's own buffer (__fern_arr_push, no slice), a refused one still clones
// (__fern_arr_slice) before growing. Answers alone cannot separate these — the
// clone form computes the same result, just quadratically — so the shape is
// what pins that the decision went the intended way.
//
// A `.with` keeps a slice either way — the in-place store's shared-buffer arm
// is one — so its mark is the gate that arm sits under: the in-place lowering
// asks __fern_rc_is_unique (of the root box, then of the buffer), and the value
// form, on a borrowed receiver, asks nothing (an `own` root's reuse arm asks it
// too, which is why the refused cases here keep a borrowed one). Nor does the
// bump high-water mark separate them: each clone is the size class of the
// buffer it replaces, so the freelist recycles it.
func TestSelfHostFieldAppendInPlaceShapeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	cases := []struct {
		name      string
		src       string
		label     string
		wantClone bool
		with      bool
	}{
		{
			name: "spread-override-grows-in-place",
			src: `
struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(op: i32): St { return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl + 1 }; }
function main(): i32 { var s: St = St { ops: [], ctrl: 0 }; s = s.emit(1); return s.ops.len(); }`,
			label:     "__fn_St__emit",
			wantClone: false,
		},
		{
			name: "same-field-read-clones",
			src: `
struct St { ops: i32[], n: i32 }
function (s: St) emit(op: i32): St { return St { ...s, ops: s.ops.append(op), n: s.ops.len() }; }
function main(): i32 { var s: St = St { ops: [], n: 0 }; s = s.emit(1); return s.ops.len(); }`,
			label:     "__fn_St__emit",
			wantClone: true,
		},
		{
			name: "local-container-clones",
			src: `
struct St { ops: i32[], n: i32 }
function grow(v: i32): St { var a: St = St { ops: [], n: 0 }; return St { ...a, ops: a.ops.append(v) }; }
function main(): i32 { return grow(3).ops.len(); }`,
			label:     "__fn_grow",
			wantClone: true,
		},
		{
			name: "spread-override-with-stores-in-place",
			src: `
struct St { tab: i32[], n: i32 }
function (s: St) put(i: i32, v: i32): St { return St { ...s, tab: s.tab.with(i, v), n: s.n + 1 }; }
function main(): i32 { var s: St = St { tab: [0, 0], n: 0 }; s = s.put(1, 4); return s.tab[1]; }`,
			label:     "__fn_St__put",
			wantClone: false,
			with:      true,
		},
		// The label table's shape (#8419): an `own` root rebound in a branch and
		// again after it, each a body-scope host of its own field.
		{
			name: "own-root-branch-rebinds-store-in-place",
			src: `
struct St { head: i32[], tail: i32[] }
function add(own a: St, b: i32, at: i32): St {
    if (a.tail[b] < 0) {
        a = St { ...a, head: a.head.with(b, at) };
    }
    a = St { ...a, tail: a.tail.with(b, at) };
    return a;
}
function main(): i32 { var s: St = St { head: [0 - 1, 0 - 1], tail: [0 - 1, 0 - 1] }; s = add(s, 1, 4); return s.head[1] + s.tail[1]; }`,
			label:     "__fn_add",
			wantClone: false,
			with:      true,
		},
		{
			name: "same-field-read-with-clones",
			src: `
struct St { tab: i32[], n: i32 }
function (s: St) put(v: i32): St { return St { ...s, tab: s.tab.with(0, v), n: s.tab[0] }; }
function main(): i32 { var s: St = St { tab: [0, 0], n: 0 }; s = s.put(4); return s.n; }`,
			label:     "__fn_St__put",
			wantClone: true,
			with:      true,
		},
		// #8485: a length and an index read of the SAME field, both sequenced
		// before the body-scope store. Each yields a scalar computed before the
		// store, so neither observes it — the site is admitted.
		{
			name: "prior-scalar-field-reads-store-in-place",
			src: `
struct St { head: i32[], n: i32 }
function put(s: St, k: i32, v: i32): St {
    var b: i32 = k % s.head.len();
    var prev: i32 = s.head[b];
    var head: i32[] = s.head.with(b, v);
    return St { head: head, n: s.n + prev };
}
function main(): i32 { var s: St = St { head: [0, 0, 0], n: 0 }; s = put(s, 1, 4); return s.head[1] + s.n; }`,
			label:     "__fn_put",
			wantClone: false,
			with:      true,
		},
		// The same read AFTER the store would see the new element, and the field
		// the store moved out of the root.
		{
			name: "later-field-read-with-clones",
			src: `
struct St { head: i32[], n: i32 }
function put(s: St, k: i32, v: i32): St {
    var head: i32[] = s.head.with(k, v);
    var m: i32 = s.head.len() + s.head[0];
    return St { head: head, n: m };
}
function main(): i32 { var s: St = St { head: [0, 0, 0], n: 0 }; s = put(s, 1, 4); return s.head[1] + s.n; }`,
			label:     "__fn_put",
			wantClone: true,
			with:      true,
		},
		// A prior read that BINDS the buffer is not excused by being early: the
		// name outlives the store and would see it.
		{
			name: "prior-field-bind-with-clones",
			src: `
struct St { head: i32[], n: i32 }
function put(s: St, k: i32, v: i32): St {
    var alias: i32[] = s.head;
    var head: i32[] = s.head.with(k, v);
    return St { head: head, n: alias[0] };
}
function main(): i32 { var s: St = St { head: [7, 0, 0], n: 0 }; s = put(s, 0, 4); return s.head[0] + s.n; }`,
			label:     "__fn_put",
			wantClone: true,
			with:      true,
		},
		// A pointer-element field keeps the value form whatever the analysis
		// says: the overwritten element is the container's to release.
		{
			name: "string-field-with-clones",
			src: `
struct St { names: string[], n: i32 }
function (s: St) put(v: string): St { return St { ...s, names: s.names.with(0, v), n: s.n + 1 }; }
function main(): i32 { var s: St = St { names: ["a", "b"], n: 0 }; s = s.put("c"); return s.names[0].len() + s.n; }`,
			label:     "__fn_St__put",
			wantClone: true,
			with:      true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := string(runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir"))
			body := asmFuncBody(t, asm, tc.label)
			if tc.with {
				gotClone := !strings.Contains(body, "call __fn___fern_rc_is_unique")
				if gotClone != tc.wantClone {
					t.Errorf("%s: value-form .with = %v, want %v; body:\n%s", tc.name, gotClone, tc.wantClone, body)
				}
				if !strings.Contains(body, "__fern_arr_slice") {
					t.Errorf("%s: no arr_slice in %s — neither form; body:\n%s", tc.name, tc.label, body)
				}
				return
			}
			gotClone := strings.Contains(body, "__fern_arr_slice")
			if gotClone != tc.wantClone {
				t.Errorf("%s: clone-before-grow = %v, want %v; body:\n%s", tc.name, gotClone, tc.wantClone, body)
			}
			if !strings.Contains(body, "__fern_arr_push") {
				t.Errorf("%s: no arr_push in %s; body:\n%s", tc.name, tc.label, body)
			}
		})
	}
}

// The grow bracket must RELEASE WHAT IT RETAINED. Its release side used to be a
// second evaluation of the same PLACE — load the slot, walk the field hops, dec
// — and a place is not stable across the call it brackets: a box the caller
// still names can be freed by the dying-donor release of a pass-through call and
// handed straight back to the next callee's own result-box allocation, so the
// re-read yields the buffer that callee just grew. The dec then lands on a live
// rc-1 buffer, whose freed block takes a freelist link over its cap word, and
// the next append reads cap 0 with len N and copies N elements into a
// four-element buffer (#8224). No answer separates the two forms until the
// freelist happens to line up, so this reads the pairing off the asm.
func TestSelfHostGrowFieldBracketReleasesRetainedX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	const src = `
struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(op: i32): St { return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl }; }
function outer(a: St): i32 {
    var b: St = a.emit(9);
    return a.ops.len() * 10 + b.ops.len();
}
function main(): i32 {
    var a: St = St { ops: [], ctrl: 0 };
    var i: i32 = 0;
    while (i < 5) { a = a.emit(i); i = i + 1; }
    return outer(a);
}`

	body := asmFuncBody(t, string(runCaptureStrictIR(t, gcc, runner, driverBin, []byte(src), "-ir")), "__fn_outer")
	lines := strings.Split(body, "\n")
	slotRe := regexp.MustCompile(`^\s*movq %rax, (-\d+\(%rbp\))$`)
	pushRe := regexp.MustCompile(`^\s*pushq (-\d+\(%rbp\))$`)

	held := ""
	for i, ln := range lines {
		if !strings.Contains(ln, "call __fn___fern_rc_inc") {
			continue
		}
		for j := i - 1; j >= 0 && j > i-6; j-- {
			if m := slotRe.FindStringSubmatch(lines[j]); m != nil {
				held = m[1]
				break
			}
		}
		break
	}
	if held == "" {
		t.Fatalf("no bracket retain capturing a slot in __fn_outer; body:\n%s", body)
	}
	for i, ln := range lines {
		if !strings.Contains(ln, "call __fn___fern_arr_dec") {
			continue
		}
		m := pushRe.FindStringSubmatch(lines[i-1])
		if m == nil {
			t.Fatalf("bracket release is not a plain load of a captured slot (got %q); body:\n%s", strings.TrimSpace(lines[i-1]), body)
		}
		if m[1] != held {
			t.Fatalf("bracket released %s but retained %s; body:\n%s", m[1], held, body)
		}
		return
	}
	t.Fatalf("no bracket release in __fn_outer; body:\n%s", body)
}

// The in-place grow's result must be UNCOUNTED (#8254). The first cut retained
// it on the identity arm — where arr_push handed the source container's own
// buffer straight back — reasoning that the literal the value feeds becomes a
// second owner. Nothing ever decremented that retain: `__field_reclaim_<T>`'s
// array arm cow-SKIPS a field pointer-equal in old and new, which is exactly
// the shape an in-place grow produces. So the buffer sat at rc >= 2 for the
// rest of its life, the NEXT append through the field took `__fern_arr_push`'s
// un-share copy, and the buffer that copy abandoned was reclaimed by nothing —
// one leaked buffer per grow, which is quadratic over a threaded accumulator.
// The whole-compiler emit paid 11.9 GB peak RSS for it against 8.0 GB without.
//
// Answers cannot see this: both forms compute the same array. `__heap_bump_bytes()`
// can — it is the bump allocator's high-water mark, i.e. everything the freelist
// could not recycle. The REFUSED shape beside it is the calibration: it clones
// per append and so must stay high, which is what makes a passing admitted case
// evidence about reclaim rather than about the instrument.
func TestSelfHostFieldAppendInPlaceReclaimsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	// Each program threads 2000 appends and exits with the bytes it could not
	// recycle, in 64 KiB units, clamped to 200 so the reading cannot wrap
	// through the 8-bit exit status.
	const tail = `
    var u: i64 = __heap_bump_bytes() / 65536i64;
    if (u > 200i64) { u = 200i64; }
    return u as i32;
}`
	admitted := `
struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(v: i32): St { return St { ...s, ops: s.ops.append(v), ctrl: s.ctrl + 1 }; }
function main(): i32 {
    var s: St = St { ops: [], ctrl: 0 };
    var i: i32 = 0;
    while (i < 2000) { s = s.emit(i); i = i + 1; }` + tail
	// `n: s.ops.len()` reads the appended field again, so the site is refused
	// and the clone form stands — the control.
	refused := `
struct St { ops: i32[], n: i32 }
function (s: St) emit(v: i32): St { return St { ...s, ops: s.ops.append(v), n: s.ops.len() }; }
function main(): i32 {
    var s: St = St { ops: [], n: 0 };
    var i: i32 = 0;
    while (i < 2000) { s = s.emit(i); i = i + 1; }` + tail

	run := func(t *testing.T, src string) int {
		t.Helper()
		asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(src), "-ir")
		if len(asm) == 0 {
			t.Fatal("self-host compiler emitted 0 bytes")
		}
		progBin := buildBin(t, gcc, dir, "faireclaim", string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(progBin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
		}
		_ = cmd.Run()
		return cmd.ProcessState.ExitCode()
	}

	// Measured: 0 units here, 165 with the identity-arm retain in place.
	if got := run(t, admitted); got > 4 {
		t.Errorf("admitted in-place shape bumped %d x 64 KiB, want <= 4 — the grown buffer is not being reclaimed", got)
	}
	// Measured: 200 (clamped) either way. A low reading here would mean the
	// instrument, not the reclaim, is what the case above is reporting.
	if got := run(t, refused); got < 32 {
		t.Errorf("refused clone shape bumped only %d x 64 KiB, want >= 32 — the calibration case is not allocating, so the admitted case proves nothing", got)
	}

}
