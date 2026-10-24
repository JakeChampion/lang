package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var selfHostFieldAppendCases = []struct {
	name string
	src  string
}{
	{"spread-threading", `
struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(op: i32): St {
    let nctrl: i32 = s.ctrl;
    if (op == 1) { nctrl = s.ctrl + 1; }
    return St { ...s, ops: s.ops.append(op), ctrl: nctrl };
}
function main(): i32 {
    let s: St = St { ops: [], ctrl: 0 };
    let i: i32 = 0;
    while (i < 30) { s = s.emit(i); i = i + 1; }
    let sum: i32 = 0;
    for v in s.ops { sum = sum + v; }
    return s.ops.len() + s.ctrl + (sum % 7);
}`},
	{"method-recv-survives", `
struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(op: i32): St { return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl }; }
function main(): i32 {
    let a: St = St { ops: [], ctrl: 0 };
    let i: i32 = 0;
    while (i < 5) { a = a.emit(i); i = i + 1; }
    let b: St = a.emit(9);
    return a.ops.len() * 10 + b.ops.len() + b.ops[5];
}`},
	{"free-param-survives", `
struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(op: i32): St { return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl }; }
function bump(s: St, v: i32): St { return St { ...s, ops: s.ops.append(v), ctrl: s.ctrl }; }
function main(): i32 {
    let a: St = St { ops: [], ctrl: 0 };
    let i: i32 = 0;
    while (i < 5) { a = a.emit(i); i = i + 1; }
    let c: St = bump(a, 7);
    return a.ops.len() * 10 + c.ops.len() + c.ops[5];
}`},
	{"transitive-pass-through", `
struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(op: i32): St { return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl }; }
function bump(s: St, v: i32): St { return St { ...s, ops: s.ops.append(v), ctrl: s.ctrl }; }
function bump2(s: St, v: i32): St { s = bump(s, v); return s; }
function main(): i32 {
    let a: St = St { ops: [], ctrl: 0 };
    let i: i32 = 0;
    while (i < 5) { a = a.emit(i); i = i + 1; }
    let d: St = bump2(a, 6);
    return a.ops.len() * 10 + d.ops.len() + d.ops[5];
}`},
	{"sole-occurrence-pass-through", `
struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(op: i32): St { return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl }; }
function bump(s: St, v: i32): St { return St { ...s, ops: s.ops.append(v), ctrl: s.ctrl }; }
function bump3(p: St): St { let t: St = bump(p, 4); return t; }
function main(): i32 {
    let a: St = St { ops: [], ctrl: 0 };
    let i: i32 = 0;
    while (i < 5) { a = a.emit(i); i = i + 1; }
    let e: St = bump3(a);
    return a.ops.len() * 10 + e.ops.len() + e.ops[5];
}`},
	{"passthrough-then-bracketed-grow", `
struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(op: i32): St { return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl + 1 }; }
function passthru(n: i32, s: St): St {
    let t: St = s;
    let i: i32 = 0;
    while (i < n) { t = t.emit(i); i = i + 1; }
    return t;
}
function outer(a: St): i32 {
    let b: St = passthru(0, a);
    let c: St = b.emit(9);
    return b.ops.len() * 10 + c.ops.len() + c.ops[6];
}
function main(): i32 {
    let a: St = St { ops: [], ctrl: 0 };
    let i: i32 = 0;
    while (i < 6) { a = a.emit(i); i = i + 1; }
    return outer(a);
}`},
	{"field-read-alias-refuses-exemption", `
struct Reg { rows: i32[], next: i32[] }
struct Sigs { reg: Reg, tag: i32 }
function append_row(r: Reg, v: i32): Reg {
    let rows: i32[] = r.rows.append(v);
    let next: i32[] = r.next.append(0 - 1);
    next = next.with(0, v);
    return Reg { rows: rows, next: next };
}
function grow_from_field(sg: Sigs): i32 {
    let aug: Reg = sg.reg;
    let i: i32 = 0;
    while (i < 5) { aug = append_row(aug, i); i = i + 1; }
    if (sg.reg.rows.len() != 3) { return 71; }
    if (sg.reg.next.len() != 3) { return 72; }
    if (aug.rows.len() != 8) { return 73; }
    if (aug.next.len() != 8) { return 74; }
    return 7;
}
function main(): i32 {
    let rows: i32[] = [];
    let next: i32[] = [];
    let k: i32 = 0;
    while (k < 3) { rows = rows.append(k); next = next.append(0 - 1); k = k + 1; }
    let sg: Sigs = Sigs { reg: Reg { rows: rows, next: next }, tag: 0 };
    return grow_from_field(sg);
}`},
	{"nested-field-argument", `
struct Inner { xs: i32[] }
struct Outer { inner: Inner, tag: i32 }
function push(i: Inner, v: i32): Inner { return Inner { xs: i.xs.append(v) }; }
function main(): i32 {
    let o: Outer = Outer { inner: Inner { xs: [] }, tag: 0 };
    let k: i32 = 0;
    while (k < 5) { o = Outer { ...o, inner: push(o.inner, k) }; k = k + 1; }
    let again: Inner = push(o.inner, 8);
    return o.inner.xs.len() * 10 + again.xs.len() + again.xs[5];
}`},
	{"same-field-read-forces-clone", `
struct St { ops: i32[], n: i32 }
function grow(s: St, v: i32): St { return St { ops: s.ops.append(v), n: s.ops.len() }; }
function main(): i32 {
    let a: St = St { ops: [], n: 0 };
    let i: i32 = 0;
    while (i < 5) { a = St { ops: a.ops.append(i), n: a.n }; i = i + 1; }
    let b: St = grow(a, 9);
    return a.ops.len() * 10 + b.ops.len() + b.n;
}`},
	{"bare-read-forces-clone", `
struct St { ops: i32[], n: i32 }
function total(s: St): i32 { let t: i32 = 0; for v in s.ops { t = t + v; } return t; }
function grow(s: St, v: i32): St { return St { ops: s.ops.append(v), n: total(s) }; }
function main(): i32 {
    let a: St = St { ops: [], n: 0 };
    let i: i32 = 0;
    while (i < 5) { a = St { ops: a.ops.append(i), n: a.n }; i = i + 1; }
    let b: St = grow(a, 9);
    return a.ops.len() * 10 + b.ops.len() + b.n;
}`},
	{"string-field", `
struct Bag { names: string[], n: i32 }
function (b: Bag) add(s: string): Bag { return Bag { ...b, names: b.names.append(s), n: b.n + 1 }; }
function main(): i32 {
    let b: Bag = Bag { names: [], n: 0 };
    let i: i32 = 0;
    while (i < 6) { b = b.add("ab"); i = i + 1; }
    let c: Bag = b.add("cdef");
    let t: i32 = 0;
    for s in b.names { t = t + s.len(); }
    for s in c.names { t = t + s.len(); }
    return t + b.names.len() + c.names.len();
}`},
	{"local-root-second-name-refused", `
struct St { ops: i32[], n: i32 }
function main(): i32 {
    let a: St = St { ops: [], n: 0 };
    let i: i32 = 0;
    while (i < 5) { a = St { ops: a.ops.append(i), n: a.n }; i = i + 1; }
    let keep: St = a;
    let b: St = St { ops: a.ops.append(9), n: a.n };
    return keep.ops.len() * 10 + b.ops.len() + a.ops.len();
}`},
	{"local-root-threading", `
struct St { ops: i32[], ctrl: i32 }
function main(): i32 {
    let a: St = St { ops: [], ctrl: 0 };
    let i: i32 = 0;
    while (i < 30) { a = St { ...a, ops: a.ops.append(i), ctrl: a.ctrl + 1 }; i = i + 1; }
    let sum: i32 = 0;
    for v in a.ops { sum = sum + v; }
    if (__rc_underflow_count() != 0) { return 99; }
    return a.ops.len() + a.ctrl + (sum % 7);
}`},
	{"local-root-with-threading", `
struct Tab { tab: i32[], n: i32 }
function main(): i32 {
    let t: Tab = Tab { tab: [0, 0, 0, 0], n: 0 };
    let i: i32 = 0;
    while (i < 12) { t = Tab { ...t, tab: t.tab.with(i % 4, i), n: t.n + 1 }; i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t.tab[0] + t.tab[1] * 2 + t.tab[2] + t.tab[3] + t.n;
}`},
	{"local-root-boxed-elsewhere-copies", `
struct St { ops: i32[], n: i32 }
function main(): i32 {
    let a: St = St { ops: [], n: 0 };
    let i: i32 = 0;
    while (i < 4) { a = St { ...a, ops: a.ops.append(i), n: a.n + 1 }; i = i + 1; }
    let hold: St[] = [];
    hold = hold.append(a);
    a = St { ...a, ops: a.ops.append(9), n: a.n + 1 };
    if (__rc_underflow_count() != 0) { return 99; }
    return hold[0].ops.len() * 10 + a.ops.len() + hold[0].ops[3];
}`},
	{"local-root-alias-from-param-refused", `
struct St { ops: i32[], n: i32 }
function take(p: St): i32 {
    let a: St = p;
    let i: i32 = 0;
    while (i < 3) { a = St { ...a, ops: a.ops.append(i), n: a.n + 1 }; i = i + 1; }
    return p.ops.len() * 10 + a.ops.len() + p.ops[2];
}
function main(): i32 {
    let s: St = St { ops: [], n: 0 };
    let k: i32 = 0;
    while (k < 4) { s = St { ...s, ops: s.ops.append(k), n: s.n + 1 }; k = k + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return take(s);
}`},
	{"local-root-counted-call-shared", `
struct St { ops: i32[], n: i32 }
@noinline function mk(p: St): St { return p; }
function take(p: St): i32 {
    let a: St = mk(p);
    let i: i32 = 0;
    while (i < 3) { a = St { ...a, ops: a.ops.append(i), n: a.n + 1 }; i = i + 1; }
    return p.ops.len() * 10 + a.ops.len() + p.ops[2];
}
function main(): i32 {
    let s: St = St { ops: [], n: 0 };
    let k: i32 = 0;
    while (k < 4) { s = St { ...s, ops: s.ops.append(k), n: s.n + 1 }; k = k + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return take(s);
}`},
	{"local-root-counted-call-threading", `
struct St { ops: i32[], n: i32 }
function tag(s: St): St { return St { ...s, n: s.n + 1 }; }
function (s: St) emit(op: i32): St { return St { ...s, ops: s.ops.append(op), n: s.n }; }
function main(): i32 {
    let a: St = tag(St { ops: [], n: 0 });
    let i: i32 = 0;
    while (i < 30) {
        a = St { ...a, ops: a.ops.append(i) };
        if (i % 7 == 0) { a = tag(a); }
        a = a.emit(i * 2);
        i = i + 1;
    }
    a = tag(a);
    let sum: i32 = 0;
    for v in a.ops { sum = sum + v; }
    if (__rc_underflow_count() != 0) { return 99; }
    return (a.ops.len() + a.n + sum) % 100;
}`},
	{"local-root-declared-in-loop", `
struct St { ops: i32[], n: i32 }
function main(): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < 6) {
        let a: St = St { ops: [j], n: 0 };
        let i: i32 = 0;
        while (i < j + 3) { a = St { ...a, ops: a.ops.append(i), n: a.n + 1 }; i = i + 1; }
        t = t + a.ops.len() * 10 + a.ops[0] + a.n;
        j = j + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 100;
}`},
	{"return-position-death-frees-the-grown-buffer", `
struct St { ops: i32[], n: i32 }
function f(s: St, v: i32): St { return St { ops: s.ops.append(v), n: 1 }; }
function mk(n: i32): St {
    let a: St = St { ops: [], n: 0 };
    let i: i32 = 0;
    while (i < n) { a = St { ops: a.ops.append(i + 1), n: 0 }; i = i + 1; }
    return f(a, 999);
}
function main(): i32 {
    let r: St = mk(9);
    let junk: i32[] = [];
    let k: i32 = 0;
    while (k < 20) { junk = junk.append(0 - 5); k = k + 1; }
    let t: i32 = 0;
    for v in r.ops { t = t + v; }
    return (t + junk.len()) % 251;
}`},
	{"return-position-death", `
struct St { ops: i32[], ctrl: i32 }
function bump(s: St, v: i32): St { return St { ...s, ops: s.ops.append(v), ctrl: s.ctrl + 1 }; }
function make(n: i32): St {
    let a: St = St { ops: [], ctrl: 0 };
    let i: i32 = 0;
    while (i < n) { a = bump(a, i); i = i + 1; }
    return bump(a, 99);
}
function main(): i32 {
    let r: St = make(6);
    let t: i32 = 0;
    for v in r.ops { t = t + v; }
    return r.ops.len() * 10 + (t % 97) + r.ctrl;
}`},
	{"own-param-container", `
struct St { ops: i32[], ctrl: i32 }
function bump(own s: St, v: i32): St { return St { ...s, ops: s.ops.append(v), ctrl: s.ctrl + 1 }; }
function main(): i32 {
    let a: St = St { ops: [], ctrl: 0 };
    let i: i32 = 0;
    while (i < 10) { a = bump(a, i); i = i + 1; }
    let t: i32 = 0;
    for v in a.ops { t = t + v; }
    return a.ops.len() + a.ctrl + (t % 29);
}`},
	{"struct-array-field", `
struct Row { k: i32, v: i32 }
struct Tab { rows: Row[], n: i32 }
function (t: Tab) put(k: i32, v: i32): Tab { return Tab { ...t, rows: t.rows.append(Row { k: k, v: v }), n: t.n + 1 }; }
function main(): i32 {
    let t: Tab = Tab { rows: [], n: 0 };
    let i: i32 = 0;
    while (i < 12) { t = t.put(i, i * 2); i = i + 1; }
    let s: i32 = 0;
    for r in t.rows { s = s + r.k + r.v; }
    return t.rows.len() + t.n + (s % 53);
}`},
	{"i64-field", `
struct W { xs: i64[], n: i32 }
function (w: W) put(v: i64): W { return W { ...w, xs: w.xs.append(v), n: w.n + 1 }; }
function main(): i32 {
    let w: W = W { xs: [], n: 0 };
    let i: i32 = 0;
    while (i < 6) { w = w.put(3i64); i = i + 1; }
    let t: i64 = 0i64;
    for v in w.xs { t = t + v; }
    return (t as i32) + w.xs.len() + w.n;
}`},
	{"return-position-deep-swept-receiver", `
struct P { x: i32 }
struct S { ops: P[], n: i32 }
function (self: S) emit(v: i32): S { return S { ops: self.ops.append(P { x: v }), n: self.n + 1 }; }
function build(): S {
    let ms: S = S { ops: [], n: 0 };
    ms = ms.emit(1);
    ms = ms.emit(2);
    ms = ms.emit(3);
    return ms.emit(4);
}
function churn(k: i32): i32 {
    let a: P[] = [];
    let i: i32 = 0;
    while (i < k) { a = a.append(P { x: 7 }); i = i + 1; }
    return a.len();
}
function main(): i32 {
    let r: S = build();
    let c: i32 = churn(64);
    if (r.ops.len() != 4 || c != 64 || r.n != 4) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return r.ops[0].x * 27 + r.ops[1].x * 9 + r.ops[2].x * 3 + r.ops[3].x;
}`},
	{"own-param-exit-release", `
struct P { x: i32 }
struct S { ops: P[], n: i32 }
function push(own b: S, v: i32): S {
    let ys: P[] = b.ops.append(P { x: v });
    return S { ops: ys, n: b.n + 1 };
}
function churn(k: i32): i32 {
    let a: P[] = [];
    let i: i32 = 0;
    while (i < k) { a = a.append(P { x: 7 }); i = i + 1; }
    return a.len();
}
function main(): i32 {
    let a: S = S { ops: [], n: 0 };
    let i: i32 = 1;
    while (i < 5) { a = push(a, i); i = i + 1; }
    let c: i32 = churn(64);
    if (a.ops.len() != 4 || c != 64 || a.n != 4) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return a.ops[0].x * 27 + a.ops[1].x * 9 + a.ops[2].x * 3 + a.ops[3].x;
}`},
	{"own-param-passed-once-to-grower", `
struct P { x: i32 }
struct S { ops: P[], n: i32 }
function h(s: S, v: i32): S { return S { ops: s.ops.append(P { x: v }), n: s.n + 1 }; }
function g(own s: S, v: i32): S { return h(s, v); }
function churn(k: i32): i32 {
    let a: P[] = [];
    let i: i32 = 0;
    while (i < k) { a = a.append(P { x: 7 }); i = i + 1; }
    return a.len();
}
function main(): i32 {
    let a: S = S { ops: [], n: 0 };
    let i: i32 = 1;
    while (i < 5) { a = g(a, i); i = i + 1; }
    let c: i32 = churn(64);
    if (a.ops.len() != 4 || c != 64 || a.n != 4) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return a.ops[0].x * 27 + a.ops[1].x * 9 + a.ops[2].x * 3 + a.ops[3].x;
}`},
	{"own-param-receiver-of-grower", `
struct P { x: i32 }
struct S { ops: P[], n: i32 }
function (self: S) emit(v: i32): S { return S { ops: self.ops.append(P { x: v }), n: self.n + 1 }; }
function g(own s: S, v: i32): S { return s.emit(v); }
function churn(k: i32): i32 {
    let a: P[] = [];
    let i: i32 = 0;
    while (i < k) { a = a.append(P { x: 7 }); i = i + 1; }
    return a.len();
}
function main(): i32 {
    let a: S = S { ops: [], n: 0 };
    let i: i32 = 1;
    while (i < 5) { a = g(a, i); i = i + 1; }
    let c: i32 = churn(64);
    if (a.ops.len() != 4 || c != 64 || a.n != 4) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return a.ops[0].x * 27 + a.ops[1].x * 9 + a.ops[2].x * 3 + a.ops[3].x;
}`},
	{"value-local-swept-before-caller-reclaim", `
struct P { x: i32 }
struct S { ops: P[], n: i32 }
function f(s: S, v: i32): S {
    let ys: P[] = s.ops.append(P { x: v });
    let n: i32 = ys.len();
    return S { ops: [], n: n };
}
function grow(s: S, v: i32): S { return S { ops: s.ops.append(P { x: v }), n: s.n + 1 }; }
function churn(k: i32): i32 {
    let a: P[] = [];
    let i: i32 = 0;
    while (i < k) { a = a.append(P { x: 7 }); i = i + 1; }
    return a.len();
}
function main(): i32 {
    let ms: S = S { ops: [], n: 0 };
    ms = grow(ms, 1);
    ms = grow(ms, 2);
    ms = grow(ms, 3);
    ms = f(ms, 4);
    let c: i32 = churn(64);
    if (c != 64 || ms.ops.len() != 0) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return ms.n;
}`},
	{"aliased-root-box-copies", `
struct Sc { names: i32[], n: i32 }
function (s: Sc) bind(v: i32): Sc {
    let ns: i32[] = s.names.append(v);
    return Sc { names: ns, n: s.n + 1 };
}
function step(v: i32, s: Sc): Sc { return s.bind(v); }
function walk(s: Sc, depth: i32): i32 {
    let cur: Sc = s;
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        if (depth > 0) { acc = acc + walk(cur, depth - 1); }
        cur = step(i, cur);
        i = i + 1;
    }
    return acc + cur.names.len() * 3 + cur.n;
}
function main(): i32 {
    let s0: Sc = Sc { names: [], n: 0 };
    s0 = step(1, s0);
    s0 = step(2, s0);
    s0 = step(3, s0);
    let r: i32 = walk(s0, 2);
    if (s0.names.len() != 3) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return (r + s0.names[2]) % 200;
}`},
	{"with-spread-threading", `
struct St { tab: i32[], n: i32 }
function (s: St) put(i: i32, v: i32): St { return St { ...s, tab: s.tab.with(i, v), n: s.n + 1 }; }
function main(): i32 {
    let s: St = St { tab: [0, 0, 0, 0, 0, 0, 0, 0], n: 0 };
    let i: i32 = 0;
    while (i < 40) { s = s.put(i % 8, i); i = i + 1; }
    let sum: i32 = 0;
    for v in s.tab { sum = sum + v; }
    return (s.n + sum) % 113;
}`},
	{"with-method-recv-survives", `
struct St { tab: i32[], n: i32 }
function (s: St) put(i: i32, v: i32): St { return St { ...s, tab: s.tab.with(i, v), n: s.n + 1 }; }
function main(): i32 {
    let a: St = St { tab: [1, 2, 3, 4], n: 0 };
    a = a.put(0, 5);
    let b: St = a.put(2, 9);
    return a.tab[2] * 10 + b.tab[2] + a.tab[0];
}`},
	{"with-own-param-label-table", `
struct Tab { names: string[], head: i32[], tail: i32[], next: i32[] }
function bucket(name: string): i32 { return name.len() % 4; }
function add(own a: Tab, name: string): Tab {
    let at: i32 = a.names.len();
    let b: i32 = bucket(name);
    let prev: i32 = a.tail[b];
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
    let i: i32 = t.head[bucket(name)];
    while (i >= 0) {
        if (t.names[i] == name) { return i; }
        i = t.next[i];
    }
    return 0 - 1;
}
function main(): i32 {
    let t: Tab = Tab { names: [], head: [0 - 1, 0 - 1, 0 - 1, 0 - 1], tail: [0 - 1, 0 - 1, 0 - 1, 0 - 1], next: [] };
    t = add(t, "a");
    t = add(t, "bb");
    t = add(t, "ccc");
    t = add(t, "dddd");
    t = add(t, "eeeee");
    t = add(t, "ff");
    t = add(t, "a");
    return lookup(t, "a") * 100 + lookup(t, "eeeee") * 10 + lookup(t, "ff") + lookup(t, "zz") + 1;
}`},
	{"with-same-field-read-forces-clone", `
struct St { tab: i32[], n: i32 }
function put(s: St, v: i32): St { return St { tab: s.tab.with(0, v), n: s.tab[0] }; }
function main(): i32 {
    let a: St = St { tab: [3, 4], n: 0 };
    let b: St = put(a, 9);
    return (a.tab[0] * 100 + b.tab[0] * 10 + b.n) % 101;
}`},
	{"with-i64-field", `
struct W { xs: i64[], n: i32 }
function (w: W) put(i: i32, v: i64): W { return W { ...w, xs: w.xs.with(i, v), n: w.n + 1 }; }
function main(): i32 {
    let w: W = W { xs: [0i64, 0i64, 0i64], n: 0 };
    let i: i32 = 0;
    while (i < 9) { w = w.put(i % 3, (i as i64) * 5i64); i = i + 1; }
    let t: i64 = 0i64;
    for v in w.xs { t = t + v; }
    return (t as i32) + w.n;
}`},
	{"with-aliased-root-box-copies", `
struct Sc { tab: i32[], n: i32 }
function (s: Sc) put(v: i32): Sc { return Sc { ...s, tab: s.tab.with(0, v), n: s.n + 1 }; }
function step(v: i32, s: Sc): Sc { return s.put(v); }
function walk(s: Sc, depth: i32): i32 {
    let cur: Sc = s;
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 3) {
        if (depth > 0) { acc = acc + walk(cur, depth - 1); }
        cur = step(i + 10 * depth, cur);
        i = i + 1;
    }
    return acc + cur.tab[0] + cur.n;
}
function main(): i32 {
    let s0: Sc = Sc { tab: [7, 7], n: 0 };
    s0 = step(1, s0);
    let r: i32 = walk(s0, 2);
    if (s0.tab[0] != 1) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return r % 113;
}`},
	{"with-ptr-elem-threading", `
struct L { v: i32 }
struct W { w: L[], n: i32 }
function (p: W) put(i: i32, l: L): W { return W { ...p, w: p.w.with(i, l), n: p.n + 1 }; }
function main(): i32 {
    let p: W = W { w: [L { v: 0 }, L { v: 0 }, L { v: 0 }, L { v: 0 }], n: 0 };
    let i: i32 = 0;
    while (i < 20) { p = p.put(i % 4, L { v: i }); i = i + 1; }
    let sum: i32 = 0;
    for e in p.w { sum = sum + e.v; }
    if (__rc_underflow_count() != 0) { return 99; }
    return (sum + p.n) % 113;
}`},
	{"with-ptr-elem-method-recv-survives", `
struct L { v: i32 }
struct W { w: L[], n: i32 }
function (p: W) put(i: i32, l: L): W { return W { ...p, w: p.w.with(i, l), n: p.n + 1 }; }
function main(): i32 {
    let a: W = W { w: [L { v: 1 }, L { v: 2 }, L { v: 3 }, L { v: 4 }], n: 0 };
    a = a.put(0, L { v: 5 });
    let b: W = a.put(2, L { v: 9 });
    if (__rc_underflow_count() != 0) { return 99; }
    return a.w[2].v * 10 + b.w[2].v + a.w[0].v;
}`},
	{"with-ptr-elem-free-param-survives", `
struct L { v: i32 }
struct W { w: L[], n: i32 }
function (p: W) put(i: i32, l: L): W { return W { ...p, w: p.w.with(i, l), n: p.n + 1 }; }
function bump(p: W, i: i32, v: i32): W { return W { ...p, w: p.w.with(i, L { v: v }), n: p.n + 1 }; }
function main(): i32 {
    let a: W = W { w: [L { v: 1 }, L { v: 2 }, L { v: 3 }, L { v: 4 }], n: 0 };
    a = a.put(0, L { v: 5 });
    let c: W = bump(a, 2, 7);
    if (__rc_underflow_count() != 0) { return 99; }
    return a.w[2].v * 10 + c.w[2].v + a.w[0].v;
}`},
	{"with-ptr-elem-aliased-root-box-copies", `
struct L { v: i32 }
struct W { w: L[], n: i32 }
function (p: W) put(v: i32): W { return W { ...p, w: p.w.with(0, L { v: v }), n: p.n + 1 }; }
function step(v: i32, p: W): W { return p.put(v); }
function walk(p: W, depth: i32): i32 {
    let cur: W = p;
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 3) {
        if (depth > 0) { acc = acc + walk(cur, depth - 1); }
        cur = step(i + 10 * depth, cur);
        i = i + 1;
    }
    return acc + cur.w[0].v + cur.n;
}
function main(): i32 {
    let p0: W = W { w: [L { v: 7 }, L { v: 7 }], n: 0 };
    p0 = step(1, p0);
    let r: i32 = walk(p0, 2);
    if (p0.w[0].v != 1) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return r % 113;
}`},
	{"with-enum-elem-recv-survives", `
enum E { A(i32), B }
struct W { es: E[], n: i32 }
function val(e: E): i32 { match (e) { E.A(x) => { return x; }, E.B => { return 0; } } return 0; }
function (p: W) put(i: i32, e: E): W { return W { ...p, es: p.es.with(i, e), n: p.n + 1 }; }
function main(): i32 {
    let a: W = W { es: [E.A(1), E.A(2), E.A(3), E.B], n: 0 };
    a = a.put(0, E.A(5));
    let b: W = a.put(2, E.A(9));
    if (__rc_underflow_count() != 0) { return 99; }
    return val(a.es[2]) * 10 + val(b.es[2]) + val(a.es[0]);
}`},
	{"with-nested-arr-elem-recv-survives", `
struct W { rows: i32[][], n: i32 }
function (p: W) put(i: i32, r: i32[]): W { return W { ...p, rows: p.rows.with(i, r), n: p.n + 1 }; }
function main(): i32 {
    let a: W = W { rows: [[1], [2], [3]], n: 0 };
    a = a.put(0, [5]);
    let b: W = a.put(2, [9]);
    if (__rc_underflow_count() != 0) { return 99; }
    return a.rows[2][0] * 10 + b.rows[2][0] + a.rows[0][0];
}`},
	{"with-prior-scalar-field-reads", `
struct RefSet { names: string[], head: i32[], next: i32[] }
function bucket(name: string, n: i32): i32 { return name.len() % n; }
function add(rs: RefSet, name: string): RefSet {
    let bk: i32 = bucket(name, rs.head.len());
    let names: string[] = rs.names.append(name);
    let next: i32[] = rs.next.append(rs.head[bk]);
    let head: i32[] = rs.head.with(bk, names.len() - 1);
    return RefSet { names: names, head: head, next: next };
}
function has(rs: RefSet, name: string): boolean {
    let i: i32 = rs.head[bucket(name, rs.head.len())];
    while (i >= 0) {
        if (rs.names[i] == name) { return true; }
        i = rs.next[i];
    }
    return false;
}
function main(): i32 {
    let rs: RefSet = RefSet { names: [], head: [0 - 1, 0 - 1, 0 - 1, 0 - 1, 0 - 1], next: [] };
    rs = add(rs, "a");
    rs = add(rs, "bb");
    rs = add(rs, "ccc");
    rs = add(rs, "dddd");
    rs = add(rs, "eeeee");
    rs = add(rs, "ffffff");
    rs = add(rs, "a");
    let hits: i32 = 0;
    if (has(rs, "a")) { hits = hits + 1; }
    if (has(rs, "eeeee")) { hits = hits + 10; }
    if (has(rs, "zz")) { hits = hits + 100; }
    if (rs.names.len() != 7) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return hits + rs.head[bucket("a", 5)];
}`},
	{"with-prior-reads-root-survives", `
struct RefSet { names: string[], head: i32[], next: i32[] }
function bucket(name: string, n: i32): i32 { return name.len() % n; }
function add(rs: RefSet, name: string): RefSet {
    let bk: i32 = bucket(name, rs.head.len());
    let names: string[] = rs.names.append(name);
    let next: i32[] = rs.next.append(rs.head[bk]);
    let head: i32[] = rs.head.with(bk, names.len() - 1);
    return RefSet { names: names, head: head, next: next };
}
function main(): i32 {
    let a: RefSet = RefSet { names: [], head: [0 - 1, 0 - 1, 0 - 1, 0 - 1], next: [] };
    a = add(a, "p");
    a = add(a, "qq");
    let b: RefSet = add(a, "rrr");
    if (__rc_underflow_count() != 0) { return 99; }
    return a.names.len() * 10 + b.names.len() + a.head[3] + b.head[3];
}`},
	{"body-host-in-loop-clones", `
struct S { ops: i32[], n: i32 }
function mk(): S {
    let s: S = S { ops: [], n: 0 };
    let i: i32 = 0;
    while (i < 3) { s = S { ...s, ops: s.ops.append(i) }; i = i + 1; }
    return s;
}
function tally(own s: S, k: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < k) {
        let ys: i32[] = s.ops.append(i);
        t = t + ys.len() + ys[ys.len() - 1];
        i = i + 1;
    }
    return t;
}
function main(): i32 {
    return tally(mk(), 4);
}`},
}

func TestSelfHostFieldAppendInPlaceX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range selfHostFieldAppendCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src))
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

func TestSelfHostFieldAppendInPlaceArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range selfHostFieldAppendCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
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

func TestSelfHostFieldAppendInPlaceWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm-IR field-append e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "wasm_ir.fern", "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range selfHostFieldAppendCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			wat := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src))
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

func TestSelfHostGrowFieldBracketReleasesRetainedX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	const src = `
struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(op: i32): St { return St { ops: s.ops.append(op), ctrl: s.ctrl }; }
@noinline function outer(a: St): i32 {
    let b: St = a.emit(9);
    return a.ops.len() * 10 + b.ops.len();
}
function main(): i32 {
    let a: St = St { ops: [], ctrl: 0 };
    let i: i32 = 0;
    while (i < 5) { a = a.emit(i); i = i + 1; }
    return outer(a);
}`

	body := asmFuncBody(t, string(runCaptureStrictIR(t, gcc, runner, driverBin, []byte(src))), "__fn_outer")
	lines := strings.Split(body, "\n")
	pushRe := regexp.MustCompile(`^\s*pushq (%r[a-z0-9]+|-\d+\(%rbp\))$`)

	held := ""
	for i, ln := range lines {
		if !strings.Contains(ln, "call __fn___fern_rc_inc") {
			continue
		}
		if i > 0 {
			if m := pushRe.FindStringSubmatch(lines[i-1]); m != nil {
				held = m[1]
			}
		}
		break
	}
	if held == "" {
		t.Fatalf("no bracket retain of a pushed operand in __fn_outer; body:\n%s", body)
	}
	regArgRe := regexp.MustCompile(`^\s*movq (%r[a-z0-9]+|-\d+\(%rbp\)), %rax$`)
	for i, ln := range lines {
		if !strings.Contains(ln, "call __fn___fern_arr_dec") {
			continue
		}
		re := pushRe
		if strings.Contains(ln, "call __fn___fern_arr_dec.r") || strings.Contains(ln, "call __fn___fern_arr_dec.u") {
			re = regArgRe
		}
		m := re.FindStringSubmatch(lines[i-1])
		if m == nil {
			t.Fatalf("bracket release does not pass the retained operand directly (got %q); body:\n%s", strings.TrimSpace(lines[i-1]), body)
		}
		if m[1] != held {
			t.Fatalf("bracket released %s but retained %s; body:\n%s", m[1], held, body)
		}
		return
	}
	t.Fatalf("no bracket release in __fn_outer; body:\n%s", body)
}

func TestSelfHostFieldAppendInPlaceReclaimsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	const tail = `
    let u: i64 = __heap_bump_bytes() / 65536i64;
    if (u > 200i64) { u = 200i64; }
    return u as i32;
}`
	admitted := `
struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(v: i32): St { return St { ...s, ops: s.ops.append(v), ctrl: s.ctrl + 1 }; }
function main(): i32 {
    let s: St = St { ops: [], ctrl: 0 };
    let i: i32 = 0;
    while (i < 2000) { s = s.emit(i); i = i + 1; }` + tail
	retained := `
struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(v: i32): St { return St { ...s, ops: s.ops.append(v), ctrl: s.ctrl + 1 }; }
function main(): i32 {
    let s: St = St { ops: [], ctrl: 0 };
    let keep: i32[][] = [];
    let i: i32 = 0;
    while (i < 2000) { s = s.emit(i); keep = keep.append(s.ops); i = i + 1; }
    if (keep.len() != 2000) { return 255; }` + tail

	run := func(t *testing.T, src string) int {
		t.Helper()
		asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(src))
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

	withPtr := `
struct L { v: i32 }
struct W { w: L[], n: i32 }
function (p: W) put(i: i32, l: L): W { return W { ...p, w: p.w.with(i, l), n: p.n + 1 }; }
function main(): i32 {
    let w: L[] = [];
    let j: i32 = 0;
    while (j < 256) { w = w.append(L { v: 0 }); j = j + 1; }
    let p: W = W { w: w, n: 0 };
    let i: i32 = 0;
    while (i < 4000) { p = p.put(i % 256, L { v: i }); i = i + 1; }` + tail

	if got := run(t, admitted); got > 4 {
		t.Errorf("admitted in-place shape bumped %d x 64 KiB, want <= 4 — the grown buffer is not being reclaimed", got)
	}
	if got := run(t, retained); got < 32 {
		t.Errorf("retained control bumped only %d x 64 KiB, want >= 32 — the calibration case is not allocating, so the admitted case proves nothing", got)
	}
	if got := run(t, withPtr); got > 8 {
		t.Errorf("pointer-element in-place .with bumped %d x 64 KiB, want <= 8 — the field's buffer is not being reused across stores", got)
	}
}
