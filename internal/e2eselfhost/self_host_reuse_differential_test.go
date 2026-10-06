package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The #4350 §6.5 reuse-on/off differential (the self-host sibling of native's
// ast.RcReuseEnabled + MatchesNoReuse oracles): FERN_SELFHOST_NO_REUSE=1 in
// the compiler's environment suppresses the donor-based reuse pairing, and the
// two compiles of the same program must be OBSERVATIONALLY IDENTICAL — the
// interpreter's exit code on both, and (via the detector cases) neither
// over-releases. Reuse-off is the plain fresh-alloc semantics; reuse-on may
// only trade alloc count / peak heap, never the value. Every case is a shape
// some reuse family was written for.
var reuseDifferentialCases = []struct {
	name string
	src  string
	want int
}{
	// A RECIPIENT whose every field is a compile-time scalar literal is not a
	// reuse shape at all: `reuse_recipient_ok` excludes it so the static
	// aggregate placement (#6149) can have it instead, which allocates nothing
	// rather than recycling a box. That is why two fixtures below multiply a
	// field by a 1-valued variable — written as plain literals they measure zero
	// reuse against zero and stop testing reuse. Only the cross-statement and
	// enum-donor families are affected; a self-overwrite recipient carries a base
	// (`{ ...d, x: 10 }`) and can never be constant.
	//
	// Family 1 — functional-update self-overwrite.
	{"self-overwrite", `struct Point { x: i32, y: i32 } function main(): i32 { let d = Point { x: 3, y: 4 }; let c = Point { ...d, x: 10 }; return c.x + c.y; }`, 14},
	// Family 2 — cross-statement struct reuse in a loop.
	{"cross-struct-loop", `struct P { x: i32, y: i32 } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let a: P = P { x: i, y: i + 1 }; let s: i32 = a.x + a.y; let b: P = P { x: i * 2, y: 3 }; sum = sum + s + b.x + b.y; i = i + 1; } return sum; }`, 40},
	// Family 2b — cross-statement struct reuse with a CALL-RESULT donor
	// (#4356 divergence 3): `d` is bound from a STRICT fresh-returning
	// function (return_fresh_struct_ret_fns — every return a no-base literal,
	// sole owner of box + fields), so donor_bind_type admits it exactly like
	// a literal-bound donor. Restricting donors to same-body literals
	// fresh-allocates this shape every time.
	{"cross-struct-callret-donor", `struct P { x: i32, y: i32 } function mk(a: i32): P { return P { x: a, y: a + 1 }; } function main(): i32 { let one: i32 = 1; let d: P = mk(3); let u: i32 = d.x + d.y; let c: P = P { x: 10 * one, y: 20 }; return c.x + c.y + u; }`, 37},
	{"cross-struct-callret-donor-detector", `struct P { x: i32, y: i32 } function mk(a: i32): P { return P { x: a, y: a + 1 }; } function main(): i32 { let one: i32 = 1; let d: P = mk(3); let u: i32 = d.x + d.y; let c: P = P { x: 10 * one, y: 20 }; let s: i32 = c.x + c.y + u; if (s != 37) { return 99; } return __rc_underflow_count(); }`, 0},
	// Family 2h — OWN-PARAM struct donor (#4356 divergence 3): a construction in a
	// function with an `own` struct param reuses that param's box (moved in, sole-
	// owned, dead after its last read). Restricted to all-scalar donor+recipient
	// (own_param_reuse_sites); the __fern_rc_is_unique guard in the emitter backstops.
	// Same type and cross-type (A donor → B recipient, same box class).
	{"own-param-donor-same", `struct P { x: i32, y: i32 } function bump(own d: P): i32 { let u: i32 = d.x + d.y; let c = P { x: 10, y: 20 }; return c.x + c.y + u; } function main(): i32 { return bump(P { x: 3, y: 4 }); }`, 37},
	{"own-param-donor-cross", `struct A { n: i32, m: i32 } struct B { p: i32, q: i32 } function f(own d: A): i32 { let u: i32 = d.n + d.m; let c = B { p: 10, q: 20 }; return c.p + c.q + u; } function main(): i32 { return f(A { n: 3, m: 4 }); }`, 37},
	{"own-param-donor-detector", `struct P { x: i32, y: i32 } function bump(own d: P): i32 { let u: i32 = d.x + d.y; let c = P { x: 10, y: 20 }; let s: i32 = c.x + c.y + u; if (s != 37) { return 99; } return __rc_underflow_count(); } function main(): i32 { return bump(P { x: 3, y: 4 }); }`, 0},
	// Family 2i — OWN-PARAM donor with RC-POINTER fields (#4356 slice 11): the
	// donor param's old array / nested-struct field is released on the reuse arm
	// (rc-GUARDED __fern_rc_dec / __struct_drop — safe for a sole-owned `own`
	// param with no donor-freshness gate), the recipient's fresh literals owned
	// going forward. Array-field and nested-struct-field donors.
	{"own-param-donor-array", `struct H { id: i32, items: i32[] } function bump(own d: H): i32 { let u: i32 = d.id + d.items[0]; let c = H { id: 5, items: [7, 8, 9] }; return c.id + c.items[0] + c.items[2] + u; } function main(): i32 { return bump(H { id: 1, items: [10, 20] }); }`, 32},
	{"own-param-donor-array-detector", `struct H { id: i32, items: i32[] } function bump(own d: H): i32 { let u: i32 = d.id + d.items[0]; let c = H { id: 5, items: [7, 8, 9] }; let s: i32 = c.id + c.items[0] + c.items[2] + u; if (s != 32) { return 99; } return __rc_underflow_count(); } function main(): i32 { return bump(H { id: 1, items: [10, 20] }); }`, 0},
	// Family 2h widened (struct_fields_reusable_param): Map / leak-safe tuple /
	// leak-safe Option fields are admitted on the own-param families — all three
	// are leak-only boxes (released nowhere), so the reuse arm's release walk
	// skips them and no donor-freshness proof is needed (enum / string stay
	// excluded: their release proof reads a bind literal a param doesn't have).
	// Map field values: both a bare ident and a map-returning CALL fire.
	// The call-valued shape once crashed on this path (reuse on or off) and
	// was excluded; that bug has been fixed upstream, so the -call case
	// below pins it as a firing reuse shape.
	{"own-param-donor-map-field", `struct C { id: i32, m: Map[i32, i32] } function f(own d: C): i32 { let u: i32 = d.id + d.m.len(); let mm: Map[i32, i32] = map_new(4); mm = mm.insert(1, 5); let c = C { id: 10, m: mm }; return c.id + c.m.len() + u; } function main(): i32 { let m0: Map[i32, i32] = map_new(4); m0 = m0.insert(1, 1); return f(C { id: 3, m: m0 }); }`, 15},
	{"own-param-donor-map-field-call", `struct C { id: i32, m: Map[i32, i32] } function make_map(): Map[i32, i32] { let mm: Map[i32, i32] = map_new(4); mm = mm.insert(1, 5); return mm; } function f(own d: C): i32 { let u: i32 = d.id + d.m.len(); let c = C { id: 10, m: make_map() }; return c.id + c.m.len() + u; } function main(): i32 { let m0: Map[i32, i32] = map_new(4); m0 = m0.insert(1, 1); return f(C { id: 3, m: m0 }); }`, 15},
	{"own-param-donor-tuple-field", `struct T2 { id: i32, t: (i32, i32) } function f(own d: T2): i32 { let u: i32 = d.id + d.t.0; let c = T2 { id: 10, t: (7, 8) }; return c.id + c.t.1 + u; } function main(): i32 { return f(T2 { id: 3, t: (1, 2) }); }`, 22},
	{"own-param-donor-tuple-field-detector", `struct T2 { id: i32, t: (i32, i32) } function f(own d: T2): i32 { let u: i32 = d.id + d.t.0; let c = T2 { id: 10, t: (7, 8) }; let s: i32 = c.id + c.t.1 + u; if (s != 22) { return 99; } return __rc_underflow_count(); } function main(): i32 { return f(T2 { id: 3, t: (1, 2) }); }`, 0},
	{"own-param-donor-opt-field", `struct O1 { id: i32, o: Option[i32] } function f(own d: O1): i32 { let u: i32 = d.id; match (d.o) { Some(v) => { u = u + v; }, None => {} } let c = O1 { id: 10, o: Some(9) }; let r: i32 = c.id + u; match (c.o) { Some(v) => { r = r + v; }, None => {} } return r; } function main(): i32 { return f(O1 { id: 3, o: Some(2) }); }`, 24},
	// Own-param SELF-OVERWRITE with a CARRIED tuple field: `c = T2 { ...d, id: 10 }`
	// moves d's tuple pointer with the reused box (leak-only, no per-field balance).
	{"own-param-funcupdate-tuple-carried", `struct T2 { id: i32, t: (i32, i32) } function f(own d: T2): i32 { let c = T2 { ...d, id: 10 }; return c.id + c.t.0 + c.t.1; } function main(): i32 { return f(T2 { id: 3, t: (1, 2) }); }`, 13},
	{"own-param-funcupdate-tuple-carried-detector", `struct T2 { id: i32, t: (i32, i32) } function f(own d: T2): i32 { let c = T2 { ...d, id: 10 }; let s: i32 = c.id + c.t.0 + c.t.1; if (s != 13) { return 99; } return __rc_underflow_count(); } function main(): i32 { return f(T2 { id: 3, t: (1, 2) }); }`, 0},
	// Family 2h, STRING and ENUM fields (#5342): an own-param donor needs no bind
	// literal to prove its old values alias-free. Its box is sole-owned (moved in),
	// and every enum-field share is counted at construction (the ExprStructLit enum
	// arm and the base copy both retain), as is every string-field share of a type
	// that ROUTES field reclaim — so the reuse arm's rc-gated release of the old
	// value only decs a shared box. The cross donor (a full construction), the
	// var-bound self-overwrite (`let c = T { ...own_d, f }`) and the return-position
	// update (`return T { ...own_p, f }`) all fire.
	{"own-param-donor-string-field", `struct P { s: string, n: i32 } function f(own d: P): i32 { let u: i32 = d.n + d.s.len(); let c = P { s: "fresh-literal-payload", n: u + 20 }; return c.n + c.s.len(); } function main(): i32 { return f(P { s: "abcdefghij-longer", n: 3 }); }`, 61},
	{"own-param-donor-string-field-detector", `struct P { s: string, n: i32 } function f(own d: P): i32 { let u: i32 = d.n + d.s.len(); let c = P { s: "fresh-literal-payload", n: u + 20 }; let s: i32 = c.n + c.s.len(); if (s != 61) { return 99; } return __rc_underflow_count(); } function main(): i32 { return f(P { s: "abcdefghij-longer", n: 3 }); }`, 0},
	{"own-param-donor-enum-field", `enum E { A(i32), B(i32) } struct Q { e: E, n: i32 } function f(own d: Q): i32 { let u: i32 = d.n; match (d.e) { A(v) => { u = u + v; }, B(v) => { u = u + v * 2; } } let c = Q { e: B(5), n: u + 20 }; let r: i32 = c.n; match (c.e) { A(v) => { r = r + v; }, B(v) => { r = r + v * 3; } } return r; } function main(): i32 { return f(Q { e: A(4), n: 3 }); }`, 42},
	{"own-param-donor-enum-field-detector", `enum E { A(i32), B(i32) } struct Q { e: E, n: i32 } function f(own d: Q): i32 { let u: i32 = d.n; match (d.e) { A(v) => { u = u + v; }, B(v) => { u = u + v * 2; } } let c = Q { e: B(5), n: u + 20 }; let r: i32 = c.n; match (c.e) { A(v) => { r = r + v; }, B(v) => { r = r + v * 3; } } if (r != 42) { return 99; } return __rc_underflow_count(); } function main(): i32 { return f(Q { e: A(4), n: 3 }); }`, 0},
	{"own-param-self-overwrite-string-field", `struct P { s: string, n: i32 } function f(own d: P): i32 { let c = P { ...d, s: "override-literal-payload" }; return c.n + c.s.len(); } function main(): i32 { return f(P { s: "abcdefghij-longer", n: 3 }); }`, 27},
	{"own-param-self-overwrite-string-field-detector", `struct P { s: string, n: i32 } function f(own d: P): i32 { let c = P { ...d, s: "override-literal-payload" }; let s: i32 = c.n + c.s.len(); if (s != 27) { return 99; } return __rc_underflow_count(); } function main(): i32 { return f(P { s: "abcdefghij-longer", n: 3 }); }`, 0},
	{"own-param-self-overwrite-enum-field", `enum E { A(i32), B(i32) } struct Q { e: E, n: i32 } function f(own d: Q): i32 { let c = Q { ...d, e: B(5) }; let r: i32 = c.n; match (c.e) { A(v) => { r = r + v; }, B(v) => { r = r + v * 3; } } return r; } function main(): i32 { return f(Q { e: A(4), n: 3 }); }`, 18},
	{"own-param-return-update-enum-field", `enum E { A(i32), B(i32) } struct Q { e: E, n: i32 } function bumpq(own p: Q): Q { return Q { ...p, n: p.n + 1 }; } function main(): i32 { let q: Q = bumpq(Q { e: A(4), n: 3 }); let r: i32 = q.n; match (q.e) { A(v) => { r = r + v; }, B(v) => { r = r + v * 3; } } if (r != 8) { return 99; } return __rc_underflow_count(); }`, 0},
	{"own-param-donor-nested-detector", `struct Inner { a: i32, b: i32 } struct Outer { id: i32, inner: Inner } function bump(own d: Outer): i32 { let u: i32 = d.id + d.inner.a; let c = Outer { id: 5, inner: Inner { a: 7, b: 8 } }; let s: i32 = c.id + c.inner.a + c.inner.b + u; if (s != 23) { return 99; } return __rc_underflow_count(); } function main(): i32 { return bump(Outer { id: 1, inner: Inner { a: 2, b: 3 } }); }`, 0},
	// Family 1g — OWN-PARAM base in the SELF-OVERWRITE family (#4356 slice 12):
	// `let c = T { ...own_d, f: v }` functional-update of an owned param reuses
	// its box in place. Scalar override, array override (fresh literal), and a
	// CARRIED array field (moves with the reused box).
	{"own-param-selfoverwrite-scalar", `struct P { x: i32, y: i32 } function bump(own d: P): i32 { let c = P { ...d, x: 10 }; return c.x + c.y; } function main(): i32 { return bump(P { x: 3, y: 4 }); }`, 14},
	{"own-param-selfoverwrite-array", `struct H { id: i32, items: i32[] } function bump(own d: H): i32 { let c = H { ...d, items: [7, 8, 9] }; return c.id + c.items[0] + c.items[2]; } function main(): i32 { return bump(H { id: 1, items: [10, 20] }); }`, 17},
	{"own-param-selfoverwrite-carried-detector", `struct H { id: i32, items: i32[] } function bump(own d: H): i32 { let c = H { ...d, id: 5 }; let s: i32 = c.id + c.items[0] + c.items[1]; if (s != 35) { return 99; } return __rc_underflow_count(); } function main(): i32 { return bump(H { id: 1, items: [10, 20] }); }`, 0},
	// Family 1b — functional-update self-overwrite with a CALL-RESULT base
	// (#4356 divergence 3): same strict-fresh donor admission on the
	// `let c = P { ...d, x: v }` path.
	{"self-overwrite-callret-base", `struct P { x: i32, y: i32 } function mk(a: i32): P { return P { x: a, y: a + 1 }; } function main(): i32 { let d: P = mk(3); let c: P = P { ...d, x: 10 }; return c.x + c.y; }`, 14},
	{"self-overwrite-callret-base-detector", `struct P { x: i32, y: i32 } function mk(a: i32): P { return P { x: a, y: a + 1 }; } function main(): i32 { let d: P = mk(3); let c: P = P { ...d, x: 10 }; let s: i32 = c.x + c.y; if (s != 14) { return 99; } return __rc_underflow_count(); }`, 0},
	// Family 2c — cross-statement struct reuse with an ENUM field (#4356
	// divergence 1): both the donor's and the recipient's enum values are
	// fresh variant ctors (donor_enum_fields_fresh + the recipient walk), so
	// the reuse arm's flat rc-gated dec of the donor's old enum box is
	// alias-free and the recycled box solely owns the new payload box.
	{"cross-struct-enum-field", `enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { let d = M { tag: 1, st: On(5) }; let u: i32 = 0; match (d.st) { On(v) => { u = v + d.tag; }, Off => { u = d.tag; } } let c = M { tag: 2, st: Off }; let r: i32 = 0; match (c.st) { On(v) => { r = v; }, Off => { r = c.tag + u; } } return r; }`, 8},
	{"cross-struct-enum-field-detector", `enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { let d = M { tag: 1, st: On(5) }; let u: i32 = 0; match (d.st) { On(v) => { u = v + d.tag; }, Off => { u = d.tag; } } let c = M { tag: 2, st: Off }; let r: i32 = 0; match (c.st) { On(v) => { r = v; }, Off => { r = c.tag + u; } } if (r != 8) { return 99; } return __rc_underflow_count(); }`, 0},
	// Family 2d — CROSS-TYPE class pairing (#4356 divergence 2): donor A and
	// recipient B share only the box class (same field count; boxes are
	// slot-uniform), with per-position field KINDS swapped (scalar/array vs
	// array/scalar) — which a position-wise identity rule would reject.
	// The reuse arm releases A's old array at A's OWN slot (donor-layout
	// walk), then B's fields overwrite. Values + detector prove the release
	// hit the right slot (a recipient-layout walk would dec a scalar as a
	// pointer — heap corruption the detector/values would catch).
	{"cross-type-class-pairing", `struct A { n: i32, xs: i32[] } struct B { ys: i32[], m: i32 } function main(): i32 { let d = A { n: 3, xs: [10, 20] }; let u: i32 = d.n + d.xs[0]; let c = B { ys: [7, 8, 9], m: 2 }; return c.ys[2] + c.m + u; }`, 24},
	{"cross-type-class-pairing-detector", `struct A { n: i32, xs: i32[] } struct B { ys: i32[], m: i32 } function main(): i32 { let d = A { n: 3, xs: [10, 20] }; let u: i32 = d.n + d.xs[0]; let c = B { ys: [7, 8, 9], m: 2 }; let s: i32 = c.ys[2] + c.m + u; if (s != 24) { return 99; } return __rc_underflow_count(); }`, 0},
	// Family 1c — self-overwrite with an ENUM field (#4356 divergence 1,
	// self-overwrite family): an OVERRIDDEN enum field's old box is flat-dec
	// released on the reuse arm (base's enum values fresh-ctor gated); a
	// CARRIED enum field moves with the box (fresh arm copies + rc-incs it,
	// sentinel-guarded).
	{"self-overwrite-enum-override", `enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { let d = M { tag: 1, st: On(5) }; let c = M { ...d, st: On(9) }; let r: i32 = 0; match (c.st) { On(v) => { r = v + c.tag; }, Off => { r = 0; } } return r; }`, 10},
	{"self-overwrite-enum-override-detector", `enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { let d = M { tag: 1, st: On(5) }; let c = M { ...d, st: On(9) }; let r: i32 = 0; match (c.st) { On(v) => { r = v + c.tag; }, Off => { r = 0; } } if (r != 10) { return 99; } return __rc_underflow_count(); }`, 0},
	{"self-overwrite-enum-carried-detector", `enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { let d = M { tag: 1, st: On(5) }; let c = M { ...d, tag: 2 }; let r: i32 = 0; match (c.st) { On(v) => { r = v + c.tag; }, Off => { r = 0; } } if (r != 7) { return 99; } return __rc_underflow_count(); }`, 0},
	// Family 1d/2e — STRING fields (#4356 divergence 1): the old buffer is
	// released with the rc-aware __fern_str_free on the reuse arm; both
	// sides' string values are fresh-gated (literal / fresh concat). Covers
	// the self-overwrite override, the carried copy, and the cross family.
	{"self-overwrite-string-override", `struct N { id: i32, name: string } function main(): i32 { let d = N { id: 1, name: "ab" + "c" }; let c = N { ...d, name: "wxyz" + "q" }; return c.name.len() as i32 + c.id; }`, 6},
	{"self-overwrite-string-override-detector", `struct N { id: i32, name: string } function main(): i32 { let d = N { id: 1, name: "ab" + "c" }; let c = N { ...d, name: "wxyz" + "q" }; let s: i32 = c.name.len() as i32 + c.id; if (s != 6) { return 99; } return __rc_underflow_count(); }`, 0},
	{"self-overwrite-string-carried-detector", `struct N { id: i32, name: string } function main(): i32 { let d = N { id: 1, name: "ab" + "c" }; let c = N { ...d, id: 2 }; let s: i32 = c.name.len() as i32 + c.id; if (s != 5) { return 99; } return __rc_underflow_count(); }`, 0},
	{"cross-struct-string-field-detector", `struct N { id: i32, name: string } function main(): i32 { let d = N { id: 1, name: "ab" + "c" }; let u: i32 = d.name.len() as i32 + d.id; let c = N { id: 2, name: "wxyz" + "q" }; let s: i32 = c.name.len() as i32 + c.id + u; if (s != 11) { return 99; } return __rc_underflow_count(); }`, 0},
	// Family 1e/2f — MAP fields (#4356 divergence 1): maps are leak-only on
	// the IR path (a map box is never freed anywhere), so the reuse arms
	// carry NO release, NO carried-copy inc, and NO freshness gate for a map
	// field — overwriting one leaks it exactly as the normal drop path would,
	// and a copied map pointer can never dangle. Covers the self-overwrite
	// carried copy, the override, and the cross family.
	{"self-overwrite-map-carried-detector", `struct P { id: i32, m: Map[i32, i32] } function main(): i32 { let d = P { id: 1, m: Map { 1: 10 } }; let c = P { ...d, id: 2 }; let s: i32 = c.m.get_or(1, 0) + c.id; if (s != 12) { return 99; } return __rc_underflow_count(); }`, 0},
	{"self-overwrite-map-override", `struct P { id: i32, m: Map[i32, i32] } function main(): i32 { let d = P { id: 1, m: Map { 1: 10 } }; let c = P { ...d, m: Map { 1: 39 } }; return c.m.get_or(1, 0) + c.id; }`, 40},
	{"self-overwrite-map-override-detector", `struct P { id: i32, m: Map[i32, i32] } function main(): i32 { let d = P { id: 1, m: Map { 1: 10 } }; let c = P { ...d, m: Map { 1: 39 } }; let s: i32 = c.m.get_or(1, 0) + c.id; if (s != 40) { return 99; } return __rc_underflow_count(); }`, 0},
	{"cross-struct-map-field-detector", `struct P { id: i32, m: Map[i32, i32] } function main(): i32 { let d = P { id: 1, m: Map { 1: 10 } }; let u: i32 = d.m.get_or(1, 0) + d.id; let c = P { id: 2, m: Map { 1: 7 } }; let s: i32 = c.m.get_or(1, 0) + c.id + u; if (s != 20) { return 99; } return __rc_underflow_count(); }`, 0},
	// Family 1f/2g — TUPLE and OPTION fields (#4356 divergence 1): both are
	// leak-only boxes (a tuple box is exit-swept only as a fresh non-escaping
	// scalar-literal local; an Option box never), so like maps the reuse arms
	// carry no release / inc / gate — the value contract is intact reads
	// through the reused box and clean detectors.
	{"self-overwrite-tuple-carried-detector", `struct P { id: i32, pr: (i32, i32) } function main(): i32 { let d = P { id: 1, pr: (10, 20) }; let c = P { ...d, id: 2 }; let s: i32 = c.pr.0 + c.pr.1 + c.id; if (s != 32) { return 99; } return __rc_underflow_count(); }`, 0},
	{"self-overwrite-tuple-override-detector", `struct P { id: i32, pr: (i32, i32) } function main(): i32 { let d = P { id: 1, pr: (10, 20) }; let c = P { ...d, pr: (7, 8) }; let s: i32 = c.pr.0 + c.pr.1 + c.id; if (s != 16) { return 99; } return __rc_underflow_count(); }`, 0},
	{"cross-struct-tuple-field-detector", `struct P { id: i32, pr: (i32, i32) } function main(): i32 { let d = P { id: 1, pr: (10, 20) }; let u: i32 = d.pr.0 + d.id; let c = P { id: 2, pr: (7, 8) }; let s: i32 = c.pr.0 + c.pr.1 + c.id + u; if (s != 28) { return 99; } return __rc_underflow_count(); }`, 0},
	{"self-overwrite-option-carried-detector", `struct Q { id: i32, o: Option[i32] } function main(): i32 { let d = Q { id: 1, o: Some(10) }; let c = Q { ...d, id: 2 }; let r: i32 = 0; match (c.o) { Some(v) => { r = v + c.id; }, None => { r = 0; } } if (r != 12) { return 99; } return __rc_underflow_count(); }`, 0},
	{"self-overwrite-option-override-detector", `struct Q { id: i32, o: Option[i32] } function main(): i32 { let d = Q { id: 1, o: Some(10) }; let c = Q { ...d, o: Some(30) }; let r: i32 = 0; match (c.o) { Some(v) => { r = v + c.id; }, None => { r = 0; } } if (r != 31) { return 99; } return __rc_underflow_count(); }`, 0},
	// Family 3 — cross-statement tuple reuse.
	{"cross-tuple-loop", `function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let a: (i32, i32) = (i, i + 1); let s: i32 = a.0 + a.1; let b: (i32, i32) = (i, 3); sum = sum + s + b.0 + b.1; i = i + 1; } return sum; }`, 34},
	// Family 4 — consumed scalar-enum donor -> struct recipient.
	{"enum-donor", `enum E { A(i32, i32), B(i32, i32) } struct W { p: i32, q: i32 } function main(): i32 { let one: i32 = 1; let x = A(10, 20); let t = 0; match (x) { A(a, b) => { t = a + b; }, B(c, d) => { t = c - d; }, } let y = W { p: 3 * one, q: 4 }; return t + y.p + y.q; }`, 37},
	{"enum-donor-detector", `enum E { A(i32, i32), B(i32, i32) } struct W { p: i32, q: i32 } function main(): i32 { let one: i32 = 1; let x = A(10, 20); let t = 0; match (x) { A(a, b) => { t = a + b; }, B(c, d) => { t = c - d; }, } let y = W { p: 3 * one, q: 4 }; let s = t + y.p + y.q; if (s != 37) { return 99; } return __rc_underflow_count(); }`, 0},
	// Enum-donor recipient WIDENED to the cross field-kind set (the reuse-audit
	// follow-through): a recipient with an ENUM field (fresh variant-ctor value;
	// released at exit via the k_enum drop arm) or a leak-only TUPLE field now
	// reuses a consumed scalar-enum donor's box. The donor's old slots are all
	// scalars, so the emitter's no-release field writes stay sound; the fresh
	// values are sole-owned (no alias-inc). Exit codes cross-checked against
	// native -interp (18 / 24).
	{"enum-donor-enum-field-recipient", `enum St { On(i32), Off } enum D2 { P(i32, i32), Q } struct M { tag: i32, st: St } function main(): i32 { let x: D2 = P(3, 4); let u: i32 = 0; match (x) { P(a, b) => { u = a + b; }, Q => { u = 0; } } let y = M { tag: 2, st: On(9) }; let r: i32 = 0; match (y.st) { On(v) => { r = v + y.tag + u; }, Off => { r = 0; } } return r; }`, 18},
	{"enum-donor-enum-field-recipient-detector", `enum St { On(i32), Off } enum D2 { P(i32, i32), Q } struct M { tag: i32, st: St } function main(): i32 { let x: D2 = P(3, 4); let u: i32 = 0; match (x) { P(a, b) => { u = a + b; }, Q => { u = 0; } } let y = M { tag: 2, st: On(9) }; let r: i32 = 0; match (y.st) { On(v) => { r = v + y.tag + u; }, Off => { r = 0; } } if (r != 18) { return 99; } return __rc_underflow_count(); }`, 0},
	{"enum-donor-tuple-field-recipient", `enum D2 { P(i32, i32), Q } struct T { tag: i32, t: (i32, i32) } function main(): i32 { let x: D2 = P(3, 4); let u: i32 = 0; match (x) { P(a, b) => { u = a + b; }, Q => { u = 0; } } let y = T { tag: 2, t: (7, 8) }; return y.tag + y.t.0 + y.t.1 + u; }`, 24},
	{"enum-donor-tuple-field-recipient-detector", `enum D2 { P(i32, i32), Q } struct T { tag: i32, t: (i32, i32) } function main(): i32 { let x: D2 = P(3, 4); let u: i32 = 0; match (x) { P(a, b) => { u = a + b; }, Q => { u = 0; } } let y = T { tag: 2, t: (7, 8) }; let s: i32 = y.tag + y.t.0 + y.t.1 + u; if (s != 24) { return 99; } return __rc_underflow_count(); }`, 0},
	// Family 5 — enum->enum cross-local reuse.
	{"enum-cross", `enum E { A(i32[]), B(i32[]) } function f(): i32 { let a: E = A([1, 2]); let t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_) => { t = 6; } } let c: E = B([3, 4]); let v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w) => { v = w[0] + w[1]; } } return t + v; } function main(): i32 { return f(); }`, 12},
	{"enum-cross-detector", `enum E { A(i32[]), B(i32[]) } function f(): i32 { let a: E = A([1, 2]); let t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_) => { t = 6; } } let c: E = B([3, 4]); let v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w) => { v = w[0] + w[1]; } } if (t + v != 12) { return 99; } return __rc_underflow_count(); } function main(): i32 { return f(); }`, 0},
	// Family 6 — in-arm consuming-match reuse, scalar +
	// the two array cow-guard shapes (same-slot MOVE and fresh-literal REPLACE).
	{"inarm-scalar", `enum E { V(i32, i32), W(i32, i32) } function go(): i32 { let x = V(3, 4); let y = match (x) { V(a, b) => W(a + 1, b + 1), W(c, d) => V(c, d) }; let r = match (y) { V(a, b) => a + b, W(c, d) => c + d }; return r; } function main(): i32 { return go(); }`, 9},
	{"inarm-array-move", `enum E { V(i32, i32[]), W(i32, i32[]) } function go(): i32 { let x = V(3, [10, 20, 30]); let y = match (x) { V(a, xs) => W(a + 1, xs), W(b, ys) => V(b, ys) }; let r = 0; match (y) { V(a, xs) => { r = a + xs[0] + xs[1] + xs[2]; }, W(c, ds) => { r = c + ds[0] + ds[1] + ds[2]; } } return r; } function main(): i32 { return go(); }`, 64},
	{"inarm-array-replace-detector", `enum E { V(i32, i32[]), W(i32, i32[]) } function go(): i32 { let x = V(3, [10, 20, 30]); let y = match (x) { V(a, xs) => W(a, [7, 8]), W(b, ys) => V(b, ys) }; let r = 0; match (y) { V(a, xs) => { r = a + xs[0] + xs[1]; }, W(c, ds) => { r = c + ds[0] + ds[1]; } } if (r != 18) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
	// string[] fields (#4356 Delta B, rc-element arrays): admitted to the
	// cross / self-overwrite families with element-fresh array-literal values
	// gated on BOTH sides (strarr_lit_all_elems_fresh in donor_enum_fields_fresh
	// / cross_recipient_fields_fresh / the override walk); the reuse arm
	// deep-frees the superseded field via __fern_str_arr_free and the
	// self-overwrite fresh arm rc-incs carried copies. Exit codes cross-checked
	// against native -interp (6 / 4 / 9); detectors prove no over-release.
	{"strarr-field-cross", `struct P { tags: string[], n: i32 } function main(): i32 { let a: P = P { tags: ["x", "y"], n: 1 }; let s1: i32 = a.tags.len() + a.n; let b: P = P { tags: ["z"], n: 2 }; if (__rc_underflow_count() != 0) { return 99; } return s1 + b.tags.len() + b.n; }`, 6},
	{"strarr-field-self-overwrite", `struct P { tags: string[], n: i32 } function main(): i32 { let d: P = P { tags: ["x", "y"], n: 1 }; let c: P = P { ...d, tags: ["z", "w", "v"] }; if (__rc_underflow_count() != 0) { return 99; } return c.tags.len() + c.n; }`, 4},
	{"strarr-field-carried-copy", `struct P { tags: string[], n: i32 } function main(): i32 { let d: P = P { tags: ["x", "y"], n: 1 }; let c: P = P { ...d, n: 5 }; if (__rc_underflow_count() != 0) { return 99; } return c.tags.len() + c.n + c.tags[0].len() + c.tags[1].len(); }`, 9},
	{"strarr-field-churn-detector", `struct P { tags: string[], n: i32 } function churn(n: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < n) { let d: P = P { tags: ["x", "y"], n: i }; let c: P = P { ...d, tags: ["z"] }; if (c.tags.len() + c.n != 1 + i) { bad = 1; } i = i + 1; } return bad; } function main(): i32 { let v: i32 = churn(2000000); if (v != 0) { return 90; } return __rc_underflow_count(); }`, 0},
	// fn (closure) fields (#4356 Delta B, native's FuncType kind): admitted to
	// the cross / self-overwrite / enum-donor families. The coarse "fn"
	// spelling reads as enum-like, so the freshness walks test fn BEFORE their
	// enum arm (fn_field_value_is_fresh: a lambda literal or its lifted
	// __mkclo$ spelling) and the enum-like release arm's shallow rc-guarded
	// dec IS the k_clo env-box release. A donor whose own closure field is
	// CALLED stays conservatively excluded by the general escape walk (a
	// method-shaped receiver use) — same as every other field kind. Exit
	// codes cross-checked against native -interp (17 / 21 / 15 / 11).
	{"fn-field-cross", `struct H { f: (i32) => i32, id: i32 } function main(): i32 { let a: H = H { f: (x: i32): i32 => { return x + 3; }, id: 1 }; let s1: i32 = a.id + 4; let b: H = H { f: (x: i32): i32 => { return x * 2; }, id: 2 }; return s1 + b.f(5) + b.id; }`, 17},
	{"fn-field-self-overwrite", `struct H { f: (i32) => i32, id: i32 } function main(): i32 { let d: H = H { f: (x: i32): i32 => { return x + 3; }, id: 1 }; let c: H = H { ...d, f: (x: i32): i32 => { return x * 4; } }; if (__rc_underflow_count() != 0) { return 99; } return c.f(5) + c.id; }`, 21},
	{"fn-field-carried-copy", `struct H { f: (i32) => i32, id: i32 } function main(): i32 { let d: H = H { f: (x: i32): i32 => { return x + 3; }, id: 1 }; let c: H = H { ...d, id: 7 }; if (__rc_underflow_count() != 0) { return 99; } return c.f(5) + c.id; }`, 15},
	{"fn-field-churn-detector", `struct H { f: (i32) => i32, id: i32 } function churn(n: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < n) { let d: H = H { f: (x: i32): i32 => { return x + 1; }, id: i }; let c: H = H { ...d, f: (x: i32): i32 => { return x + 2; } }; if (c.f(10) + c.id != 12 + i) { bad = 1; } i = i + 1; } return bad; } function main(): i32 { let v: i32 = churn(2000000); if (v != 0) { return 90; } return __rc_underflow_count(); }`, 0},
	{"enum-donor-fn-field-recipient", `enum D2 { P(i32, i32), Q } struct M { tag: i32, g: (i32) => i32 } function main(): i32 { let x: D2 = P(3, 4); let u: i32 = 0; match (x) { P(a, b) => { u = a + b; }, Q => { u = 0; } } let y = M { tag: 2, g: (q: i32): i32 => { return q + 1; } }; return y.g(1) + y.tag + u; }`, 11},
	// struct[] / enum[] box-element-array fields (#4356 Delta B, the last
	// rc-element-array kind): admitted with element-fresh array-literal
	// values on both sides (boxarr_lit_all_elems_fresh) and released
	// per-element via __fern_arrarr_free; struct[] restricted to
	// scalar-field element types (nothing under the box to leak). Exit
	// codes cross-checked against native -interp (15 / 0 / 13 / 17 / 16).
	{"boxarr-struct-cross", `struct In { k: i32, n: i32 } struct W { items: In[], id: i32 } function main(): i32 { let a: W = W { items: [In { k: 1, n: 2 }, In { k: 3, n: 4 }], id: 1 }; let s1: i32 = a.items.len() + a.items[1].k + a.id; let b: W = W { items: [In { k: 5, n: 6 }], id: 2 }; if (__rc_underflow_count() != 0) { return 99; } return s1 + b.items.len() + b.items[0].n + b.id; }`, 15},
	{"boxarr-struct-churn-detector", `struct In { k: i32, n: i32 } struct W { items: In[], id: i32 } function churn(n: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < n) { let a: W = W { items: [In { k: i, n: 2 }, In { k: 3, n: 4 }], id: i }; let t: i32 = a.items.len() + a.items[0].k + a.id; let b: W = W { items: [In { k: 5, n: i }], id: i + 1 }; if (t != 2 + i + i) { bad = 1; } if (b.items.len() + b.items[0].n + b.id != 2 + i + i) { bad = 1; } i = i + 1; } return bad; } function main(): i32 { let v: i32 = churn(1000000); if (v != 0) { return 90; } return __rc_underflow_count(); }`, 0},
	{"boxarr-enum-cross", `enum St { On(i32), Off } struct W { sts: St[], id: i32 } function main(): i32 { let a: W = W { sts: [On(3), Off], id: 1 }; let s1: i32 = a.sts.len() + a.id; let b: W = W { sts: [On(7)], id: 2 }; let s2: i32 = b.sts.len() + b.id; match (b.sts[0]) { On(v) => { s2 = s2 + v; }, Off => {} } if (__rc_underflow_count() != 0) { return 99; } return s1 + s2; }`, 13},
	{"boxarr-self-overwrite", `struct In { k: i32, n: i32 } struct W { items: In[], id: i32 } function main(): i32 { let d: W = W { items: [In { k: 1, n: 2 }], id: 6 }; let c: W = W { ...d, items: [In { k: 7, n: 8 }, In { k: 9, n: 10 }] }; if (__rc_underflow_count() != 0) { return 99; } return c.items.len() + c.items[1].k + c.id; }`, 17},
	{"boxarr-carried-copy", `struct In { k: i32, n: i32 } struct W { items: In[], id: i32 } function main(): i32 { let d: W = W { items: [In { k: 1, n: 2 }, In { k: 3, n: 4 }], id: 6 }; let c: W = W { ...d, id: 9 }; if (__rc_underflow_count() != 0) { return 99; } return c.items.len() + c.items[0].k + c.items[1].n + c.id; }`, 16},
	// MIXED-field interaction shapes (the seams between the fn / string[] /
	// string / enum / nested-struct admissions): one struct carrying several
	// release-armed kinds at once, under cross reuse and a churn-scale
	// self-overwrite. Exit codes cross-checked against native -interp
	// (19 / 0 / 118); the detectors prove no arm double-fires.
	{"mixed-fn-strarr-str-cross", `struct W { f: (i32) => i32, tags: string[], name: string, id: i32 } function main(): i32 { let a: W = W { f: (x: i32): i32 => { return x + 1; }, tags: ["p", "q"], name: "aa", id: 1 }; let s1: i32 = a.id + a.tags.len() + a.name.len(); let b: W = W { f: (x: i32): i32 => { return x * 2; }, tags: ["r"], name: "bbb", id: 2 }; if (__rc_underflow_count() != 0) { return 99; } return s1 + b.f(4) + b.tags.len() + b.name.len() + b.id; }`, 19},
	{"mixed-selfoverwrite-churn-detector", `struct W { f: (i32) => i32, tags: string[], name: string, id: i32 } function churn(n: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < n) { let d: W = W { f: (x: i32): i32 => { return x + 1; }, tags: ["p", "q"], name: "aa", id: i }; let c: W = W { ...d, f: (x: i32): i32 => { return x + 2; }, tags: ["z"] }; if (c.f(10) + c.tags.len() + c.name.len() + c.id != 15 + i) { bad = 1; } i = i + 1; } return bad; } function main(): i32 { let v: i32 = churn(1000000); if (v != 0) { return 90; } return __rc_underflow_count(); }`, 0},
	{"mixed-enum-fn-cross", `enum St { On(i32), Off } struct W { f: (i32) => i32, st: St, id: i32 } function main(): i32 { let a: W = W { f: (x: i32): i32 => { return x + 1; }, st: On(7), id: 1 }; let s1: i32 = a.id; match (a.st) { On(v) => { s1 = s1 + v; }, Off => {} } let b: W = W { f: (x: i32): i32 => { return x * 2; }, st: Off, id: 2 }; let s2: i32 = b.f(4) + b.id; match (b.st) { On(v) => { s2 = s2 + v; }, Off => { s2 = s2 + 100; } } if (__rc_underflow_count() != 0) { return 99; } return s1 + s2; }`, 118},
	// ENUMRE — the in-place enum reassign upgrade,
	// gated with the layer; reuse-off falls back to a free+alloc.
	{"enumre-inplace-churn", `enum Bag { Keep(i32[]), Swap(i32[]) } function churn(n: i32): i32 { let b: Bag = Keep([0, 0, 0, 0]); let i = 0; while (i < n) { b = Keep([i, i, i, i]); b = Swap([i, i, i, i]); i = i + 1; } let r = 0; match (b) { Keep(_) => { r = 1; }, Swap(_) => { r = 2; }, } if (r != 2) { return 99; } return __rc_underflow_count(); } function main(): i32 { return churn(5); }`, 0},
}

// TestSelfHostReuseDifferentialX86_64 compiles each case TWICE through the
// self-hosted x86-64 driver — once normally (reuse on) and once with
// FERN_SELFHOST_NO_REUSE=1 (reuse off) — and asserts both binaries exit with
// the interpreter's answer.
func TestSelfHostReuseDifferentialX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	// emitEnv runs the driver on prog with extra environment entries.
	emitEnv := func(t *testing.T, prog string, extraEnv ...string) string {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(prog))
		// childEnv, not os.Environ(): the "reuse on" arm sets nothing, so an
		// ambient FERN_SELFHOST_NO_REUSE=1 would make it reuse-OFF too (#6833).
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

// TestSelfHostStrarrReuseExclusionX86_64 runs the string[] AND fn reuse
// shapes whose value is ALIASED (a bare local ident as a donor field / a
// self-overwrite override): the alias stays usable after the second
// construction (values cross-checked against native -interp: 10 / 4 / 25 / 18
// / 8 / 12), with the rc-underflow detector clean.
func TestSelfHostStrarrReuseExclusionX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../compiler/drivers/asm_run.fern")
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
		// #5342 own-param string / enum admissions: a bare local as the string
		// override is an uncounted alias (cross_recipient_fields_fresh refuses),
		// and a string-fielded type that does not ROUTE field reclaim (`get`
		// returns `x.s`, which the routing scan reads as an unsafe read of every
		// `s` field) took no retain at the caller's construction, so no family
		// may free its old value.
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
