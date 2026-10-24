package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var reuseDifferentialCases = []struct {
	name string
	src  string
	want int
}{
	{"self-overwrite", `struct Point { x: i32, y: i32 } function main(): i32 { let d = Point { x: 3, y: 4 }; let c = Point { ...d, x: 10 }; return c.x + c.y; }`, 14},
	{"cross-struct-loop", `struct P { x: i32, y: i32 } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let a: P = P { x: i, y: i + 1 }; let s: i32 = a.x + a.y; let b: P = P { x: i * 2, y: 3 }; sum = sum + s + b.x + b.y; i = i + 1; } return sum; }`, 40},
	{"cross-struct-callret-donor", `struct P { x: i32, y: i32 } function mk(a: i32): P { return P { x: a, y: a + 1 }; } function main(): i32 { let one: i32 = 1; let d: P = mk(3); let u: i32 = d.x + d.y; let c: P = P { x: 10 * one, y: 20 }; return c.x + c.y + u; }`, 37},
	{"cross-struct-callret-donor-detector", `struct P { x: i32, y: i32 } function mk(a: i32): P { return P { x: a, y: a + 1 }; } function main(): i32 { let one: i32 = 1; let d: P = mk(3); let u: i32 = d.x + d.y; let c: P = P { x: 10 * one, y: 20 }; let s: i32 = c.x + c.y + u; if (s != 37) { return 99; } return __rc_underflow_count(); }`, 0},
	{"own-param-donor-same", `struct P { x: i32, y: i32 } function bump(own d: P): i32 { let u: i32 = d.x + d.y; let c = P { x: 10, y: 20 }; return c.x + c.y + u; } function main(): i32 { return bump(P { x: 3, y: 4 }); }`, 37},
	{"own-param-donor-cross", `struct A { n: i32, m: i32 } struct B { p: i32, q: i32 } function f(own d: A): i32 { let u: i32 = d.n + d.m; let c = B { p: 10, q: 20 }; return c.p + c.q + u; } function main(): i32 { return f(A { n: 3, m: 4 }); }`, 37},
	{"own-param-donor-detector", `struct P { x: i32, y: i32 } function bump(own d: P): i32 { let u: i32 = d.x + d.y; let c = P { x: 10, y: 20 }; let s: i32 = c.x + c.y + u; if (s != 37) { return 99; } return __rc_underflow_count(); } function main(): i32 { return bump(P { x: 3, y: 4 }); }`, 0},
	{"own-param-donor-array", `struct H { id: i32, items: i32[] } function bump(own d: H): i32 { let u: i32 = d.id + d.items[0]; let c = H { id: 5, items: [7, 8, 9] }; return c.id + c.items[0] + c.items[2] + u; } function main(): i32 { return bump(H { id: 1, items: [10, 20] }); }`, 32},
	{"own-param-donor-array-detector", `struct H { id: i32, items: i32[] } function bump(own d: H): i32 { let u: i32 = d.id + d.items[0]; let c = H { id: 5, items: [7, 8, 9] }; let s: i32 = c.id + c.items[0] + c.items[2] + u; if (s != 32) { return 99; } return __rc_underflow_count(); } function main(): i32 { return bump(H { id: 1, items: [10, 20] }); }`, 0},
	{"own-param-donor-tuple-field", `struct T2 { id: i32, t: (i32, i32) } function f(own d: T2): i32 { let u: i32 = d.id + d.t.0; let c = T2 { id: 10, t: (7, 8) }; return c.id + c.t.1 + u; } function main(): i32 { return f(T2 { id: 3, t: (1, 2) }); }`, 22},
	{"own-param-donor-tuple-field-detector", `struct T2 { id: i32, t: (i32, i32) } function f(own d: T2): i32 { let u: i32 = d.id + d.t.0; let c = T2 { id: 10, t: (7, 8) }; let s: i32 = c.id + c.t.1 + u; if (s != 22) { return 99; } return __rc_underflow_count(); } function main(): i32 { return f(T2 { id: 3, t: (1, 2) }); }`, 0},
	{"own-param-donor-opt-field", `struct O1 { id: i32, o: Option[i32] } function f(own d: O1): i32 { let u: i32 = d.id; match (d.o) { Some(v) => { u = u + v; }, None => {} } let c = O1 { id: 10, o: Some(9) }; let r: i32 = c.id + u; match (c.o) { Some(v) => { r = r + v; }, None => {} } return r; } function main(): i32 { return f(O1 { id: 3, o: Some(2) }); }`, 24},
	{"own-param-funcupdate-tuple-carried", `struct T2 { id: i32, t: (i32, i32) } function f(own d: T2): i32 { let c = T2 { ...d, id: 10 }; return c.id + c.t.0 + c.t.1; } function main(): i32 { return f(T2 { id: 3, t: (1, 2) }); }`, 13},
	{"own-param-funcupdate-tuple-carried-detector", `struct T2 { id: i32, t: (i32, i32) } function f(own d: T2): i32 { let c = T2 { ...d, id: 10 }; let s: i32 = c.id + c.t.0 + c.t.1; if (s != 13) { return 99; } return __rc_underflow_count(); } function main(): i32 { return f(T2 { id: 3, t: (1, 2) }); }`, 0},
	{"own-param-donor-string-field", `struct P { s: string, n: i32 } function f(own d: P): i32 { let u: i32 = d.n + d.s.len(); let c = P { s: "fresh-literal-payload", n: u + 20 }; return c.n + c.s.len(); } function main(): i32 { return f(P { s: "abcdefghij-longer", n: 3 }); }`, 61},
	{"own-param-donor-string-field-detector", `struct P { s: string, n: i32 } function f(own d: P): i32 { let u: i32 = d.n + d.s.len(); let c = P { s: "fresh-literal-payload", n: u + 20 }; let s: i32 = c.n + c.s.len(); if (s != 61) { return 99; } return __rc_underflow_count(); } function main(): i32 { return f(P { s: "abcdefghij-longer", n: 3 }); }`, 0},
	{"own-param-donor-enum-field", `enum E { A(i32), B(i32) } struct Q { e: E, n: i32 } function f(own d: Q): i32 { let u: i32 = d.n; match (d.e) { A(v) => { u = u + v; }, B(v) => { u = u + v * 2; } } let c = Q { e: B(5), n: u + 20 }; let r: i32 = c.n; match (c.e) { A(v) => { r = r + v; }, B(v) => { r = r + v * 3; } } return r; } function main(): i32 { return f(Q { e: A(4), n: 3 }); }`, 42},
	{"own-param-donor-enum-field-detector", `enum E { A(i32), B(i32) } struct Q { e: E, n: i32 } function f(own d: Q): i32 { let u: i32 = d.n; match (d.e) { A(v) => { u = u + v; }, B(v) => { u = u + v * 2; } } let c = Q { e: B(5), n: u + 20 }; let r: i32 = c.n; match (c.e) { A(v) => { r = r + v; }, B(v) => { r = r + v * 3; } } if (r != 42) { return 99; } return __rc_underflow_count(); } function main(): i32 { return f(Q { e: A(4), n: 3 }); }`, 0},
	{"own-param-self-overwrite-string-field", `struct P { s: string, n: i32 } function f(own d: P): i32 { let c = P { ...d, s: "override-literal-payload" }; return c.n + c.s.len(); } function main(): i32 { return f(P { s: "abcdefghij-longer", n: 3 }); }`, 27},
	{"own-param-self-overwrite-string-field-detector", `struct P { s: string, n: i32 } function f(own d: P): i32 { let c = P { ...d, s: "override-literal-payload" }; let s: i32 = c.n + c.s.len(); if (s != 27) { return 99; } return __rc_underflow_count(); } function main(): i32 { return f(P { s: "abcdefghij-longer", n: 3 }); }`, 0},
	{"own-param-self-overwrite-enum-field", `enum E { A(i32), B(i32) } struct Q { e: E, n: i32 } function f(own d: Q): i32 { let c = Q { ...d, e: B(5) }; let r: i32 = c.n; match (c.e) { A(v) => { r = r + v; }, B(v) => { r = r + v * 3; } } return r; } function main(): i32 { return f(Q { e: A(4), n: 3 }); }`, 18},
	{"own-param-return-update-enum-field", `enum E { A(i32), B(i32) } struct Q { e: E, n: i32 } function bumpq(own p: Q): Q { return Q { ...p, n: p.n + 1 }; } function main(): i32 { let q: Q = bumpq(Q { e: A(4), n: 3 }); let r: i32 = q.n; match (q.e) { A(v) => { r = r + v; }, B(v) => { r = r + v * 3; } } if (r != 8) { return 99; } return __rc_underflow_count(); }`, 0},
	{"own-param-donor-nested-detector", `struct Inner { a: i32, b: i32 } struct Outer { id: i32, inner: Inner } function bump(own d: Outer): i32 { let u: i32 = d.id + d.inner.a; let c = Outer { id: 5, inner: Inner { a: 7, b: 8 } }; let s: i32 = c.id + c.inner.a + c.inner.b + u; if (s != 23) { return 99; } return __rc_underflow_count(); } function main(): i32 { return bump(Outer { id: 1, inner: Inner { a: 2, b: 3 } }); }`, 0},
	{"own-param-selfoverwrite-scalar", `struct P { x: i32, y: i32 } function bump(own d: P): i32 { let c = P { ...d, x: 10 }; return c.x + c.y; } function main(): i32 { return bump(P { x: 3, y: 4 }); }`, 14},
	{"own-param-selfoverwrite-array", `struct H { id: i32, items: i32[] } function bump(own d: H): i32 { let c = H { ...d, items: [7, 8, 9] }; return c.id + c.items[0] + c.items[2]; } function main(): i32 { return bump(H { id: 1, items: [10, 20] }); }`, 17},
	{"own-param-selfoverwrite-carried-detector", `struct H { id: i32, items: i32[] } function bump(own d: H): i32 { let c = H { ...d, id: 5 }; let s: i32 = c.id + c.items[0] + c.items[1]; if (s != 35) { return 99; } return __rc_underflow_count(); } function main(): i32 { return bump(H { id: 1, items: [10, 20] }); }`, 0},
	{"self-overwrite-callret-base", `struct P { x: i32, y: i32 } function mk(a: i32): P { return P { x: a, y: a + 1 }; } function main(): i32 { let d: P = mk(3); let c: P = P { ...d, x: 10 }; return c.x + c.y; }`, 14},
	{"self-overwrite-callret-base-detector", `struct P { x: i32, y: i32 } function mk(a: i32): P { return P { x: a, y: a + 1 }; } function main(): i32 { let d: P = mk(3); let c: P = P { ...d, x: 10 }; let s: i32 = c.x + c.y; if (s != 14) { return 99; } return __rc_underflow_count(); }`, 0},
	{"cross-struct-enum-field", `enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { let d = M { tag: 1, st: On(5) }; let u: i32 = 0; match (d.st) { On(v) => { u = v + d.tag; }, Off => { u = d.tag; } } let c = M { tag: 2, st: Off }; let r: i32 = 0; match (c.st) { On(v) => { r = v; }, Off => { r = c.tag + u; } } return r; }`, 8},
	{"cross-struct-enum-field-detector", `enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { let d = M { tag: 1, st: On(5) }; let u: i32 = 0; match (d.st) { On(v) => { u = v + d.tag; }, Off => { u = d.tag; } } let c = M { tag: 2, st: Off }; let r: i32 = 0; match (c.st) { On(v) => { r = v; }, Off => { r = c.tag + u; } } if (r != 8) { return 99; } return __rc_underflow_count(); }`, 0},
	{"cross-type-class-pairing", `struct A { n: i32, xs: i32[] } struct B { ys: i32[], m: i32 } function main(): i32 { let d = A { n: 3, xs: [10, 20] }; let u: i32 = d.n + d.xs[0]; let c = B { ys: [7, 8, 9], m: 2 }; return c.ys[2] + c.m + u; }`, 24},
	{"cross-type-class-pairing-detector", `struct A { n: i32, xs: i32[] } struct B { ys: i32[], m: i32 } function main(): i32 { let d = A { n: 3, xs: [10, 20] }; let u: i32 = d.n + d.xs[0]; let c = B { ys: [7, 8, 9], m: 2 }; let s: i32 = c.ys[2] + c.m + u; if (s != 24) { return 99; } return __rc_underflow_count(); }`, 0},
	{"self-overwrite-enum-override", `enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { let d = M { tag: 1, st: On(5) }; let c = M { ...d, st: On(9) }; let r: i32 = 0; match (c.st) { On(v) => { r = v + c.tag; }, Off => { r = 0; } } return r; }`, 10},
	{"self-overwrite-enum-override-detector", `enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { let d = M { tag: 1, st: On(5) }; let c = M { ...d, st: On(9) }; let r: i32 = 0; match (c.st) { On(v) => { r = v + c.tag; }, Off => { r = 0; } } if (r != 10) { return 99; } return __rc_underflow_count(); }`, 0},
	{"self-overwrite-enum-carried-detector", `enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { let d = M { tag: 1, st: On(5) }; let c = M { ...d, tag: 2 }; let r: i32 = 0; match (c.st) { On(v) => { r = v + c.tag; }, Off => { r = 0; } } if (r != 7) { return 99; } return __rc_underflow_count(); }`, 0},
	{"self-overwrite-string-override", `struct N { id: i32, name: string } function main(): i32 { let d = N { id: 1, name: "ab" + "c" }; let c = N { ...d, name: "wxyz" + "q" }; return c.name.len() as i32 + c.id; }`, 6},
	{"self-overwrite-string-override-detector", `struct N { id: i32, name: string } function main(): i32 { let d = N { id: 1, name: "ab" + "c" }; let c = N { ...d, name: "wxyz" + "q" }; let s: i32 = c.name.len() as i32 + c.id; if (s != 6) { return 99; } return __rc_underflow_count(); }`, 0},
	{"self-overwrite-string-carried-detector", `struct N { id: i32, name: string } function main(): i32 { let d = N { id: 1, name: "ab" + "c" }; let c = N { ...d, id: 2 }; let s: i32 = c.name.len() as i32 + c.id; if (s != 5) { return 99; } return __rc_underflow_count(); }`, 0},
	{"cross-struct-string-field-detector", `struct N { id: i32, name: string } function main(): i32 { let d = N { id: 1, name: "ab" + "c" }; let u: i32 = d.name.len() as i32 + d.id; let c = N { id: 2, name: "wxyz" + "q" }; let s: i32 = c.name.len() as i32 + c.id + u; if (s != 11) { return 99; } return __rc_underflow_count(); }`, 0},
	{"self-overwrite-tuple-carried-detector", `struct P { id: i32, pr: (i32, i32) } function main(): i32 { let d = P { id: 1, pr: (10, 20) }; let c = P { ...d, id: 2 }; let s: i32 = c.pr.0 + c.pr.1 + c.id; if (s != 32) { return 99; } return __rc_underflow_count(); }`, 0},
	{"self-overwrite-tuple-override-detector", `struct P { id: i32, pr: (i32, i32) } function main(): i32 { let d = P { id: 1, pr: (10, 20) }; let c = P { ...d, pr: (7, 8) }; let s: i32 = c.pr.0 + c.pr.1 + c.id; if (s != 16) { return 99; } return __rc_underflow_count(); }`, 0},
	{"cross-struct-tuple-field-detector", `struct P { id: i32, pr: (i32, i32) } function main(): i32 { let d = P { id: 1, pr: (10, 20) }; let u: i32 = d.pr.0 + d.id; let c = P { id: 2, pr: (7, 8) }; let s: i32 = c.pr.0 + c.pr.1 + c.id + u; if (s != 28) { return 99; } return __rc_underflow_count(); }`, 0},
	{"self-overwrite-option-carried-detector", `struct Q { id: i32, o: Option[i32] } function main(): i32 { let d = Q { id: 1, o: Some(10) }; let c = Q { ...d, id: 2 }; let r: i32 = 0; match (c.o) { Some(v) => { r = v + c.id; }, None => { r = 0; } } if (r != 12) { return 99; } return __rc_underflow_count(); }`, 0},
	{"self-overwrite-option-override-detector", `struct Q { id: i32, o: Option[i32] } function main(): i32 { let d = Q { id: 1, o: Some(10) }; let c = Q { ...d, o: Some(30) }; let r: i32 = 0; match (c.o) { Some(v) => { r = v + c.id; }, None => { r = 0; } } if (r != 31) { return 99; } return __rc_underflow_count(); }`, 0},
	{"cross-tuple-loop", `function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let a: (i32, i32) = (i, i + 1); let s: i32 = a.0 + a.1; let b: (i32, i32) = (i, 3); sum = sum + s + b.0 + b.1; i = i + 1; } return sum; }`, 34},
	{"enum-donor", `enum E { A(i32, i32), B(i32, i32) } struct W { p: i32, q: i32 } function main(): i32 { let one: i32 = 1; let x = A(10, 20); let t = 0; match (x) { A(a, b) => { t = a + b; }, B(c, d) => { t = c - d; }, } let y = W { p: 3 * one, q: 4 }; return t + y.p + y.q; }`, 37},
	{"enum-donor-detector", `enum E { A(i32, i32), B(i32, i32) } struct W { p: i32, q: i32 } function main(): i32 { let one: i32 = 1; let x = A(10, 20); let t = 0; match (x) { A(a, b) => { t = a + b; }, B(c, d) => { t = c - d; }, } let y = W { p: 3 * one, q: 4 }; let s = t + y.p + y.q; if (s != 37) { return 99; } return __rc_underflow_count(); }`, 0},
	{"enum-donor-enum-field-recipient", `enum St { On(i32), Off } enum D2 { P(i32, i32), Q } struct M { tag: i32, st: St } function main(): i32 { let x: D2 = P(3, 4); let u: i32 = 0; match (x) { P(a, b) => { u = a + b; }, Q => { u = 0; } } let y = M { tag: 2, st: On(9) }; let r: i32 = 0; match (y.st) { On(v) => { r = v + y.tag + u; }, Off => { r = 0; } } return r; }`, 18},
	{"enum-donor-enum-field-recipient-detector", `enum St { On(i32), Off } enum D2 { P(i32, i32), Q } struct M { tag: i32, st: St } function main(): i32 { let x: D2 = P(3, 4); let u: i32 = 0; match (x) { P(a, b) => { u = a + b; }, Q => { u = 0; } } let y = M { tag: 2, st: On(9) }; let r: i32 = 0; match (y.st) { On(v) => { r = v + y.tag + u; }, Off => { r = 0; } } if (r != 18) { return 99; } return __rc_underflow_count(); }`, 0},
	{"enum-donor-tuple-field-recipient", `enum D2 { P(i32, i32), Q } struct T { tag: i32, t: (i32, i32) } function main(): i32 { let x: D2 = P(3, 4); let u: i32 = 0; match (x) { P(a, b) => { u = a + b; }, Q => { u = 0; } } let y = T { tag: 2, t: (7, 8) }; return y.tag + y.t.0 + y.t.1 + u; }`, 24},
	{"enum-donor-tuple-field-recipient-detector", `enum D2 { P(i32, i32), Q } struct T { tag: i32, t: (i32, i32) } function main(): i32 { let x: D2 = P(3, 4); let u: i32 = 0; match (x) { P(a, b) => { u = a + b; }, Q => { u = 0; } } let y = T { tag: 2, t: (7, 8) }; let s: i32 = y.tag + y.t.0 + y.t.1 + u; if (s != 24) { return 99; } return __rc_underflow_count(); }`, 0},
	{"enum-cross", `enum E { A(i32[]), B(i32[]) } function f(): i32 { let a: E = A([1, 2]); let t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_) => { t = 6; } } let c: E = B([3, 4]); let v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w) => { v = w[0] + w[1]; } } return t + v; } function main(): i32 { return f(); }`, 12},
	{"enum-cross-detector", `enum E { A(i32[]), B(i32[]) } function f(): i32 { let a: E = A([1, 2]); let t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_) => { t = 6; } } let c: E = B([3, 4]); let v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w) => { v = w[0] + w[1]; } } if (t + v != 12) { return 99; } return __rc_underflow_count(); } function main(): i32 { return f(); }`, 0},
	{"inarm-scalar", `enum E { V(i32, i32), W(i32, i32) } function go(): i32 { let x = V(3, 4); let y = match (x) { V(a, b) => W(a + 1, b + 1), W(c, d) => V(c, d) }; let r = match (y) { V(a, b) => a + b, W(c, d) => c + d }; return r; } function main(): i32 { return go(); }`, 9},
	{"inarm-array-move", `enum E { V(i32, i32[]), W(i32, i32[]) } function go(): i32 { let x = V(3, [10, 20, 30]); let y = match (x) { V(a, xs) => W(a + 1, xs), W(b, ys) => V(b, ys) }; let r = 0; match (y) { V(a, xs) => { r = a + xs[0] + xs[1] + xs[2]; }, W(c, ds) => { r = c + ds[0] + ds[1] + ds[2]; } } return r; } function main(): i32 { return go(); }`, 64},
	{"inarm-array-replace-detector", `enum E { V(i32, i32[]), W(i32, i32[]) } function go(): i32 { let x = V(3, [10, 20, 30]); let y = match (x) { V(a, xs) => W(a, [7, 8]), W(b, ys) => V(b, ys) }; let r = 0; match (y) { V(a, xs) => { r = a + xs[0] + xs[1]; }, W(c, ds) => { r = c + ds[0] + ds[1]; } } if (r != 18) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
	{"strarr-field-cross", `struct P { tags: string[], n: i32 } function main(): i32 { let a: P = P { tags: ["x", "y"], n: 1 }; let s1: i32 = a.tags.len() + a.n; let b: P = P { tags: ["z"], n: 2 }; if (__rc_underflow_count() != 0) { return 99; } return s1 + b.tags.len() + b.n; }`, 6},
	{"strarr-field-self-overwrite", `struct P { tags: string[], n: i32 } function main(): i32 { let d: P = P { tags: ["x", "y"], n: 1 }; let c: P = P { ...d, tags: ["z", "w", "v"] }; if (__rc_underflow_count() != 0) { return 99; } return c.tags.len() + c.n; }`, 4},
	{"strarr-field-carried-copy", `struct P { tags: string[], n: i32 } function main(): i32 { let d: P = P { tags: ["x", "y"], n: 1 }; let c: P = P { ...d, n: 5 }; if (__rc_underflow_count() != 0) { return 99; } return c.tags.len() + c.n + c.tags[0].len() + c.tags[1].len(); }`, 9},
	{"strarr-field-churn-detector", `struct P { tags: string[], n: i32 } function churn(n: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < n) { let d: P = P { tags: ["x", "y"], n: i }; let c: P = P { ...d, tags: ["z"] }; if (c.tags.len() + c.n != 1 + i) { bad = 1; } i = i + 1; } return bad; } function main(): i32 { let v: i32 = churn(2000000); if (v != 0) { return 90; } return __rc_underflow_count(); }`, 0},
	{"fn-field-cross", `struct H { f: (i32) => i32, id: i32 } function main(): i32 { let a: H = H { f: (x: i32): i32 => { return x + 3; }, id: 1 }; let s1: i32 = a.id + 4; let b: H = H { f: (x: i32): i32 => { return x * 2; }, id: 2 }; return s1 + b.f(5) + b.id; }`, 17},
	{"fn-field-self-overwrite", `struct H { f: (i32) => i32, id: i32 } function main(): i32 { let d: H = H { f: (x: i32): i32 => { return x + 3; }, id: 1 }; let c: H = H { ...d, f: (x: i32): i32 => { return x * 4; } }; if (__rc_underflow_count() != 0) { return 99; } return c.f(5) + c.id; }`, 21},
	{"fn-field-carried-copy", `struct H { f: (i32) => i32, id: i32 } function main(): i32 { let d: H = H { f: (x: i32): i32 => { return x + 3; }, id: 1 }; let c: H = H { ...d, id: 7 }; if (__rc_underflow_count() != 0) { return 99; } return c.f(5) + c.id; }`, 15},
	{"fn-field-churn-detector", `struct H { f: (i32) => i32, id: i32 } function churn(n: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < n) { let d: H = H { f: (x: i32): i32 => { return x + 1; }, id: i }; let c: H = H { ...d, f: (x: i32): i32 => { return x + 2; } }; if (c.f(10) + c.id != 12 + i) { bad = 1; } i = i + 1; } return bad; } function main(): i32 { let v: i32 = churn(2000000); if (v != 0) { return 90; } return __rc_underflow_count(); }`, 0},
	{"enum-donor-fn-field-recipient", `enum D2 { P(i32, i32), Q } struct M { tag: i32, g: (i32) => i32 } function main(): i32 { let x: D2 = P(3, 4); let u: i32 = 0; match (x) { P(a, b) => { u = a + b; }, Q => { u = 0; } } let y = M { tag: 2, g: (q: i32): i32 => { return q + 1; } }; return y.g(1) + y.tag + u; }`, 11},
	{"boxarr-struct-cross", `struct In { k: i32, n: i32 } struct W { items: In[], id: i32 } function main(): i32 { let a: W = W { items: [In { k: 1, n: 2 }, In { k: 3, n: 4 }], id: 1 }; let s1: i32 = a.items.len() + a.items[1].k + a.id; let b: W = W { items: [In { k: 5, n: 6 }], id: 2 }; if (__rc_underflow_count() != 0) { return 99; } return s1 + b.items.len() + b.items[0].n + b.id; }`, 15},
	{"boxarr-struct-churn-detector", `struct In { k: i32, n: i32 } struct W { items: In[], id: i32 } function churn(n: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < n) { let a: W = W { items: [In { k: i, n: 2 }, In { k: 3, n: 4 }], id: i }; let t: i32 = a.items.len() + a.items[0].k + a.id; let b: W = W { items: [In { k: 5, n: i }], id: i + 1 }; if (t != 2 + i + i) { bad = 1; } if (b.items.len() + b.items[0].n + b.id != 2 + i + i) { bad = 1; } i = i + 1; } return bad; } function main(): i32 { let v: i32 = churn(1000000); if (v != 0) { return 90; } return __rc_underflow_count(); }`, 0},
	{"boxarr-enum-cross", `enum St { On(i32), Off } struct W { sts: St[], id: i32 } function main(): i32 { let a: W = W { sts: [On(3), Off], id: 1 }; let s1: i32 = a.sts.len() + a.id; let b: W = W { sts: [On(7)], id: 2 }; let s2: i32 = b.sts.len() + b.id; match (b.sts[0]) { On(v) => { s2 = s2 + v; }, Off => {} } if (__rc_underflow_count() != 0) { return 99; } return s1 + s2; }`, 13},
	{"boxarr-self-overwrite", `struct In { k: i32, n: i32 } struct W { items: In[], id: i32 } function main(): i32 { let d: W = W { items: [In { k: 1, n: 2 }], id: 6 }; let c: W = W { ...d, items: [In { k: 7, n: 8 }, In { k: 9, n: 10 }] }; if (__rc_underflow_count() != 0) { return 99; } return c.items.len() + c.items[1].k + c.id; }`, 17},
	{"boxarr-carried-copy", `struct In { k: i32, n: i32 } struct W { items: In[], id: i32 } function main(): i32 { let d: W = W { items: [In { k: 1, n: 2 }, In { k: 3, n: 4 }], id: 6 }; let c: W = W { ...d, id: 9 }; if (__rc_underflow_count() != 0) { return 99; } return c.items.len() + c.items[0].k + c.items[1].n + c.id; }`, 16},
	{"mixed-fn-strarr-str-cross", `struct W { f: (i32) => i32, tags: string[], name: string, id: i32 } function main(): i32 { let a: W = W { f: (x: i32): i32 => { return x + 1; }, tags: ["p", "q"], name: "aa", id: 1 }; let s1: i32 = a.id + a.tags.len() + a.name.len(); let b: W = W { f: (x: i32): i32 => { return x * 2; }, tags: ["r"], name: "bbb", id: 2 }; if (__rc_underflow_count() != 0) { return 99; } return s1 + b.f(4) + b.tags.len() + b.name.len() + b.id; }`, 19},
	{"mixed-selfoverwrite-churn-detector", `struct W { f: (i32) => i32, tags: string[], name: string, id: i32 } function churn(n: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < n) { let d: W = W { f: (x: i32): i32 => { return x + 1; }, tags: ["p", "q"], name: "aa", id: i }; let c: W = W { ...d, f: (x: i32): i32 => { return x + 2; }, tags: ["z"] }; if (c.f(10) + c.tags.len() + c.name.len() + c.id != 15 + i) { bad = 1; } i = i + 1; } return bad; } function main(): i32 { let v: i32 = churn(1000000); if (v != 0) { return 90; } return __rc_underflow_count(); }`, 0},
	{"mixed-enum-fn-cross", `enum St { On(i32), Off } struct W { f: (i32) => i32, st: St, id: i32 } function main(): i32 { let a: W = W { f: (x: i32): i32 => { return x + 1; }, st: On(7), id: 1 }; let s1: i32 = a.id; match (a.st) { On(v) => { s1 = s1 + v; }, Off => {} } let b: W = W { f: (x: i32): i32 => { return x * 2; }, st: Off, id: 2 }; let s2: i32 = b.f(4) + b.id; match (b.st) { On(v) => { s2 = s2 + v; }, Off => { s2 = s2 + 100; } } if (__rc_underflow_count() != 0) { return 99; } return s1 + s2; }`, 118},
	{"enumre-inplace-churn", `enum Bag { Keep(i32[]), Swap(i32[]) } function churn(n: i32): i32 { let b: Bag = Keep([0, 0, 0, 0]); let i = 0; while (i < n) { b = Keep([i, i, i, i]); b = Swap([i, i, i, i]); i = i + 1; } let r = 0; match (b) { Keep(_) => { r = 1; }, Swap(_) => { r = 2; }, } if (r != 2) { return 99; } return __rc_underflow_count(); } function main(): i32 { return churn(5); }`, 0},
}

func TestSelfHostReuseDifferentialX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	emitEnv := func(t *testing.T, prog string, extraEnv ...string) string {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(prog))
		cmd.Env = childEnv(extraEnv...)
		out, err := cmd.Output()
		if err != nil || len(out) == 0 {
			t.Fatalf("driver failed (env %v): %v", extraEnv, err)
		}
		return string(out)
	}
	runBin := func(t *testing.T, bin string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
		}
		_ = cmd.Run()
		return cmd.ProcessState.ExitCode()
	}

	for _, tc := range reuseDifferentialCases {
		t.Run(tc.name, func(t *testing.T) {
			gotOn := runBin(t, buildBin(t, gcc, dir, tc.name+"-on", emitEnv(t, tc.src)))
			gotOff := runBin(t, buildBin(t, gcc, dir, tc.name+"-off", emitEnv(t, tc.src, "FERN_SELFHOST_NO_REUSE=1")))
			if gotOn != tc.want {
				t.Errorf("%s: reuse-on exited %d, want %d", tc.name, gotOn, tc.want)
			}
			if gotOff != tc.want {
				t.Errorf("%s: reuse-off exited %d, want %d", tc.name, gotOff, tc.want)
			}
		})
	}
}

func TestSelfHostStrarrReuseExclusionX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"aliased-donor-field", `struct P { tags: string[], n: i32 } function main(): i32 { let xs: string[] = ["k", "m"]; let a: P = P { tags: xs, n: 1 }; let s1: i32 = a.tags.len() + a.n; let b: P = P { tags: ["z"], n: 2 }; let live: i32 = xs.len() + xs[0].len() + xs[1].len(); if (__rc_underflow_count() != 0) { return 99; } return s1 + b.tags.len() + b.n + live; }`, 10},
		{"aliased-override", `struct P { tags: string[], n: i32 } function main(): i32 { let xs: string[] = ["k"]; let d: P = P { tags: ["x"], n: 1 }; let c: P = P { ...d, tags: xs, n: 2 }; let live: i32 = xs[0].len(); if (__rc_underflow_count() != 0) { return 99; } return c.tags.len() + c.n + live; }`, 4},
		{"aliased-fn-donor-field", `struct H { f: (i32) => i32, id: i32 } function main(): i32 { let g = (x: i32): i32 => { return x * 2; }; let a: H = H { f: g, id: 1 }; let s1: i32 = a.f(5) + a.id; let b: H = H { f: (x: i32): i32 => { return x + 1; }, id: 2 }; let live: i32 = g(3); if (__rc_underflow_count() != 0) { return 99; } return s1 + b.f(5) + b.id + live; }`, 25},
		{"aliased-fn-override", `struct H { f: (i32) => i32, id: i32 } function main(): i32 { let g = (x: i32): i32 => { return x * 2; }; let d: H = H { f: (x: i32): i32 => { return x + 1; }, id: 1 }; let c: H = H { ...d, f: g, id: 2 }; let live: i32 = g(3); if (__rc_underflow_count() != 0) { return 99; } return c.f(5) + c.id + live; }`, 18},
		{"aliased-boxarr-donor-field", `struct In { k: i32, n: i32 } struct W { items: In[], id: i32 } function main(): i32 { let xs: In[] = [In { k: 1, n: 2 }]; let a: W = W { items: xs, id: 1 }; let s1: i32 = a.items.len() + a.id; let b: W = W { items: [In { k: 5, n: 6 }], id: 2 }; let live: i32 = xs[0].k + xs[0].n; if (__rc_underflow_count() != 0) { return 99; } return s1 + b.items.len() + b.id + live; }`, 8},
		{"aliased-string-own-override", `struct P { s: string, n: i32 } function f(own d: P): i32 { let t: string = "aliased-local-payload"; let c = P { ...d, s: t }; return c.n + c.s.len() + t.len(); } function main(): i32 { if (f(P { s: "abcdefghij-longer", n: 3 }) != 45) { return 98; } return __rc_underflow_count(); }`, 0},
		{"unrouted-string-own-donor", `struct P { s: string, n: i32 } function get(x: P): string { return x.s; } function f(own d: P): i32 { let u: i32 = d.n; let c = P { ...d, s: "override-literal-payload" }; return c.n + c.s.len() + u; } function main(): i32 { let h: P = P { s: "abcdefghij-longer", n: 3 }; let r: i32 = f(P { s: h.s, n: 4 }); let g: string = get(h); if (r + g.len() != 49) { return 98; } return __rc_underflow_count(); }`, 0},
		{"rcfield-element-type-excluded", `struct In2 { xs: i32[], k: i32 } struct W { items: In2[], id: i32 } function main(): i32 { let a: W = W { items: [In2 { xs: [1, 2], k: 3 }], id: 1 }; let s1: i32 = a.items.len() + a.items[0].k + a.id; let b: W = W { items: [In2 { xs: [4], k: 5 }], id: 2 }; if (__rc_underflow_count() != 0) { return 99; } return s1 + b.items.len() + b.items[0].xs[0] + b.id; }`, 12},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d (99 = over-release)", tc.name, code, tc.want)
			}
		})
	}
}
