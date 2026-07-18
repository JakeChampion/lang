package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The #4350 §6.5 reuse-on/off differential gate (the self-host sibling of
// native's ast.RcReuseEnabled + MatchesNoReuse oracles): compiling with
// FERN_SELFHOST_NO_REUSE=1 in the DRIVER's environment suppresses every
// donor-based reuse pairing (self-overwrite / cross-struct / cross-tuple /
// enum-donor / enum-cross / in-arm) plus the ENUMRE in-place reassign
// upgrade, and the two compiles of the same program must be OBSERVATIONALLY
// IDENTICAL — same exit code, and (via the detector cases) both leak-free of
// over-releases. Reuse-off is the plain fresh-alloc semantics; reuse-on may
// only trade alloc count / peak heap, never the value.
//
// Every case here is a FIRING shape for its family (guard-checked below: the
// reuse-on asm carries the __fern_alloc_reuse call or the ENUMRE in-place
// shape, the reuse-off asm does not), so the differential actually exercises
// the switch rather than comparing two identical lowerings.
var reuseDifferentialCases = []struct {
	name string
	src  string
	want int
	// what the reuse-ON asm must contain that the OFF asm must not — the
	// firing witness. The alloc_reuse CALL for the five guarded donor
	// families (the call, not the bare helper name: the runtime helper BODY
	// is always emitted, so its label matches on both sides). "" skips the
	// witness check (ENUMRE's in-place form has no distinct call symbol;
	// its firing is pinned by asm inequality instead).
	witness string
}{
	// Family 1 — functional-update self-overwrite (emit_self_overwrite_reuse).
	{"self-overwrite", `struct Point { x: i32, y: i32 } function main(): i32 { var d = Point { x: 3, y: 4 }; var c = Point { ...d, x: 10 }; return c.x + c.y; }`, 14, "call __fn___fern_alloc_reuse"},
	// Family 2 — cross-statement struct reuse in a loop (emit_cross_struct_reuse).
	{"cross-struct-loop", `struct P { x: i32, y: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var a: P = P { x: i, y: i + 1 }; var s: i32 = a.x + a.y; var b: P = P { x: i * 2, y: 3 }; sum = sum + s + b.x + b.y; i = i + 1; } return sum; }`, 40, "call __fn___fern_alloc_reuse"},
	// Family 2b — cross-statement struct reuse with a CALL-RESULT donor
	// (#4356 divergence 3): `d` is bound from a STRICT fresh-returning
	// function (return_fresh_struct_ret_fns — every return a no-base literal,
	// sole owner of box + fields), so donor_bind_type admits it exactly like
	// a literal-bound donor. Previously only same-body literals could donate,
	// so this shape fresh-allocated every time.
	{"cross-struct-callret-donor", `struct P { x: i32, y: i32 } function mk(a: i32): P { return P { x: a, y: a + 1 }; } function main(): i32 { var d: P = mk(3); var u: i32 = d.x + d.y; var c: P = P { x: 10, y: 20 }; return c.x + c.y + u; }`, 37, "call __fn___fern_alloc_reuse"},
	{"cross-struct-callret-donor-detector", `struct P { x: i32, y: i32 } function mk(a: i32): P { return P { x: a, y: a + 1 }; } function main(): i32 { var d: P = mk(3); var u: i32 = d.x + d.y; var c: P = P { x: 10, y: 20 }; var s: i32 = c.x + c.y + u; if (s != 37) { return 99; } return __rc_underflow(); }`, 0, "call __fn___fern_alloc_reuse"},
	// Family 2h — OWN-PARAM struct donor (#4356 divergence 3): a construction in a
	// function with an `own` struct param reuses that param's box (moved in, sole-
	// owned, dead after its last read). Restricted to all-scalar donor+recipient
	// (own_param_reuse_sites); the __fern_rc_is_unique guard in the emitter backstops.
	// Same type and cross-type (A donor → B recipient, same box class).
	{"own-param-donor-same", `struct P { x: i32, y: i32 } function bump(own d: P): i32 { var u: i32 = d.x + d.y; var c = P { x: 10, y: 20 }; return c.x + c.y + u; } function main(): i32 { return bump(P { x: 3, y: 4 }); }`, 37, "call __fn___fern_alloc_reuse"},
	{"own-param-donor-cross", `struct A { n: i32, m: i32 } struct B { p: i32, q: i32 } function f(own d: A): i32 { var u: i32 = d.n + d.m; var c = B { p: 10, q: 20 }; return c.p + c.q + u; } function main(): i32 { return f(A { n: 3, m: 4 }); }`, 37, "call __fn___fern_alloc_reuse"},
	{"own-param-donor-detector", `struct P { x: i32, y: i32 } function bump(own d: P): i32 { var u: i32 = d.x + d.y; var c = P { x: 10, y: 20 }; var s: i32 = c.x + c.y + u; if (s != 37) { return 99; } return __rc_underflow(); } function main(): i32 { return bump(P { x: 3, y: 4 }); }`, 0, "call __fn___fern_alloc_reuse"},
	// Family 2i — OWN-PARAM donor with RC-POINTER fields (#4356 slice 11): the
	// donor param's old array / nested-struct field is released on the reuse arm
	// (rc-GUARDED __fern_rc_dec / __struct_drop — safe for a sole-owned `own`
	// param with no donor-freshness gate), the recipient's fresh literals owned
	// going forward. Array-field and nested-struct-field donors.
	{"own-param-donor-array", `struct H { id: i32, items: i32[] } function bump(own d: H): i32 { var u: i32 = d.id + d.items[0]; var c = H { id: 5, items: [7, 8, 9] }; return c.id + c.items[0] + c.items[2] + u; } function main(): i32 { return bump(H { id: 1, items: [10, 20] }); }`, 32, "call __fn___fern_alloc_reuse"},
	{"own-param-donor-array-detector", `struct H { id: i32, items: i32[] } function bump(own d: H): i32 { var u: i32 = d.id + d.items[0]; var c = H { id: 5, items: [7, 8, 9] }; var s: i32 = c.id + c.items[0] + c.items[2] + u; if (s != 32) { return 99; } return __rc_underflow(); } function main(): i32 { return bump(H { id: 1, items: [10, 20] }); }`, 0, "call __fn___fern_alloc_reuse"},
	// Family 2h widened (struct_fields_reusable_param): Map / leak-safe tuple /
	// leak-safe Option fields are admitted on the own-param families — all three
	// are leak-only boxes (released nowhere), so the reuse arm's release walk
	// skips them and no donor-freshness proof is needed (enum / string stay
	// excluded: their release proof reads a bind literal a param doesn't have).
	// The Map field VALUE is a bare ident — a map-returning CALL as a struct-lit
	// field value is a separate pre-existing crash on this path, reuse on or off.
	{"own-param-donor-map-field", `struct C { id: i32, m: Map[i32, i32] } function f(own d: C): i32 { var u: i32 = d.id + d.m.len(); var mm: Map[i32, i32] = map_new(4); mm = mm.insert(1, 5); var c = C { id: 10, m: mm }; return c.id + c.m.len() + u; } function main(): i32 { var m0: Map[i32, i32] = map_new(4); m0 = m0.insert(1, 1); return f(C { id: 3, m: m0 }); }`, 15, "call __fn___fern_alloc_reuse"},
	{"own-param-donor-tuple-field", `struct T2 { id: i32, t: (i32, i32) } function f(own d: T2): i32 { var u: i32 = d.id + d.t.0; var c = T2 { id: 10, t: (7, 8) }; return c.id + c.t.1 + u; } function main(): i32 { return f(T2 { id: 3, t: (1, 2) }); }`, 22, "call __fn___fern_alloc_reuse"},
	{"own-param-donor-tuple-field-detector", `struct T2 { id: i32, t: (i32, i32) } function f(own d: T2): i32 { var u: i32 = d.id + d.t.0; var c = T2 { id: 10, t: (7, 8) }; var s: i32 = c.id + c.t.1 + u; if (s != 22) { return 99; } return __rc_underflow(); } function main(): i32 { return f(T2 { id: 3, t: (1, 2) }); }`, 0, "call __fn___fern_alloc_reuse"},
	{"own-param-donor-opt-field", `struct O1 { id: i32, o: Option[i32] } function f(own d: O1): i32 { var u: i32 = d.id; match (d.o) { Some(v) => { u = u + v; }, None => {} } var c = O1 { id: 10, o: Some(9) }; var r: i32 = c.id + u; match (c.o) { Some(v) => { r = r + v; }, None => {} } return r; } function main(): i32 { return f(O1 { id: 3, o: Some(2) }); }`, 24, "call __fn___fern_alloc_reuse"},
	// Own-param SELF-OVERWRITE with a CARRIED tuple field: `c = T2 { ...d, id: 10 }`
	// moves d's tuple pointer with the reused box (leak-only, no per-field balance).
	{"own-param-funcupdate-tuple-carried", `struct T2 { id: i32, t: (i32, i32) } function f(own d: T2): i32 { var c = T2 { ...d, id: 10 }; return c.id + c.t.0 + c.t.1; } function main(): i32 { return f(T2 { id: 3, t: (1, 2) }); }`, 13, "call __fn___fern_alloc_reuse"},
	{"own-param-funcupdate-tuple-carried-detector", `struct T2 { id: i32, t: (i32, i32) } function f(own d: T2): i32 { var c = T2 { ...d, id: 10 }; var s: i32 = c.id + c.t.0 + c.t.1; if (s != 13) { return 99; } return __rc_underflow(); } function main(): i32 { return f(T2 { id: 3, t: (1, 2) }); }`, 0, "call __fn___fern_alloc_reuse"},
	{"own-param-donor-nested-detector", `struct Inner { a: i32, b: i32 } struct Outer { id: i32, inner: Inner } function bump(own d: Outer): i32 { var u: i32 = d.id + d.inner.a; var c = Outer { id: 5, inner: Inner { a: 7, b: 8 } }; var s: i32 = c.id + c.inner.a + c.inner.b + u; if (s != 23) { return 99; } return __rc_underflow(); } function main(): i32 { return bump(Outer { id: 1, inner: Inner { a: 2, b: 3 } }); }`, 0, "call __fn___fern_alloc_reuse"},
	// Family 1g — OWN-PARAM base in the SELF-OVERWRITE family (#4356 slice 12):
	// `var c = T { ...own_d, f: v }` functional-update of an owned param reuses
	// its box in place. Scalar override, array override (fresh literal), and a
	// CARRIED array field (moves with the reused box).
	{"own-param-selfoverwrite-scalar", `struct P { x: i32, y: i32 } function bump(own d: P): i32 { var c = P { ...d, x: 10 }; return c.x + c.y; } function main(): i32 { return bump(P { x: 3, y: 4 }); }`, 14, "call __fn___fern_alloc_reuse"},
	{"own-param-selfoverwrite-array", `struct H { id: i32, items: i32[] } function bump(own d: H): i32 { var c = H { ...d, items: [7, 8, 9] }; return c.id + c.items[0] + c.items[2]; } function main(): i32 { return bump(H { id: 1, items: [10, 20] }); }`, 17, "call __fn___fern_alloc_reuse"},
	{"own-param-selfoverwrite-carried-detector", `struct H { id: i32, items: i32[] } function bump(own d: H): i32 { var c = H { ...d, id: 5 }; var s: i32 = c.id + c.items[0] + c.items[1]; if (s != 35) { return 99; } return __rc_underflow(); } function main(): i32 { return bump(H { id: 1, items: [10, 20] }); }`, 0, "call __fn___fern_alloc_reuse"},
	// Family 1b — functional-update self-overwrite with a CALL-RESULT base
	// (#4356 divergence 3): same strict-fresh donor admission on the
	// `var c = P { ...d, x: v }` path.
	{"self-overwrite-callret-base", `struct P { x: i32, y: i32 } function mk(a: i32): P { return P { x: a, y: a + 1 }; } function main(): i32 { var d: P = mk(3); var c: P = P { ...d, x: 10 }; return c.x + c.y; }`, 14, "call __fn___fern_alloc_reuse"},
	{"self-overwrite-callret-base-detector", `struct P { x: i32, y: i32 } function mk(a: i32): P { return P { x: a, y: a + 1 }; } function main(): i32 { var d: P = mk(3); var c: P = P { ...d, x: 10 }; var s: i32 = c.x + c.y; if (s != 14) { return 99; } return __rc_underflow(); }`, 0, "call __fn___fern_alloc_reuse"},
	// Family 2c — cross-statement struct reuse with an ENUM field (#4356
	// divergence 1): both the donor's and the recipient's enum values are
	// fresh variant ctors (donor_enum_fields_fresh + the recipient walk), so
	// the reuse arm's flat rc-gated dec of the donor's old enum box is
	// alias-free and the recycled box solely owns the new payload box.
	{"cross-struct-enum-field", `enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { var d = M { tag: 1, st: On(5) }; var u: i32 = 0; match (d.st) { On(v) => { u = v + d.tag; }, Off => { u = d.tag; } } var c = M { tag: 2, st: Off }; var r: i32 = 0; match (c.st) { On(v) => { r = v; }, Off => { r = c.tag + u; } } return r; }`, 8, "call __fn___fern_alloc_reuse"},
	{"cross-struct-enum-field-detector", `enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { var d = M { tag: 1, st: On(5) }; var u: i32 = 0; match (d.st) { On(v) => { u = v + d.tag; }, Off => { u = d.tag; } } var c = M { tag: 2, st: Off }; var r: i32 = 0; match (c.st) { On(v) => { r = v; }, Off => { r = c.tag + u; } } if (r != 8) { return 99; } return __rc_underflow(); }`, 0, "call __fn___fern_alloc_reuse"},
	// Family 2d — CROSS-TYPE class pairing (#4356 divergence 2): donor A and
	// recipient B share only the box class (same field count; boxes are
	// slot-uniform), with per-position field KINDS swapped (scalar/array vs
	// array/scalar) — previously rejected by the position-wise identity rule.
	// The reuse arm releases A's old array at A's OWN slot (donor-layout
	// walk), then B's fields overwrite. Values + detector prove the release
	// hit the right slot (a recipient-layout walk would dec a scalar as a
	// pointer — heap corruption the detector/values would catch).
	{"cross-type-class-pairing", `struct A { n: i32, xs: i32[] } struct B { ys: i32[], m: i32 } function main(): i32 { var d = A { n: 3, xs: [10, 20] }; var u: i32 = d.n + d.xs[0]; var c = B { ys: [7, 8, 9], m: 2 }; return c.ys[2] + c.m + u; }`, 24, "call __fn___fern_alloc_reuse"},
	{"cross-type-class-pairing-detector", `struct A { n: i32, xs: i32[] } struct B { ys: i32[], m: i32 } function main(): i32 { var d = A { n: 3, xs: [10, 20] }; var u: i32 = d.n + d.xs[0]; var c = B { ys: [7, 8, 9], m: 2 }; var s: i32 = c.ys[2] + c.m + u; if (s != 24) { return 99; } return __rc_underflow(); }`, 0, "call __fn___fern_alloc_reuse"},
	// Family 1c — self-overwrite with an ENUM field (#4356 divergence 1,
	// self-overwrite family): an OVERRIDDEN enum field's old box is flat-dec
	// released on the reuse arm (base's enum values fresh-ctor gated); a
	// CARRIED enum field moves with the box (fresh arm copies + rc-incs it,
	// sentinel-guarded).
	{"self-overwrite-enum-override", `enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { var d = M { tag: 1, st: On(5) }; var c = M { ...d, st: On(9) }; var r: i32 = 0; match (c.st) { On(v) => { r = v + c.tag; }, Off => { r = 0; } } return r; }`, 10, "call __fn___fern_alloc_reuse"},
	{"self-overwrite-enum-override-detector", `enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { var d = M { tag: 1, st: On(5) }; var c = M { ...d, st: On(9) }; var r: i32 = 0; match (c.st) { On(v) => { r = v + c.tag; }, Off => { r = 0; } } if (r != 10) { return 99; } return __rc_underflow(); }`, 0, "call __fn___fern_alloc_reuse"},
	{"self-overwrite-enum-carried-detector", `enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { var d = M { tag: 1, st: On(5) }; var c = M { ...d, tag: 2 }; var r: i32 = 0; match (c.st) { On(v) => { r = v + c.tag; }, Off => { r = 0; } } if (r != 7) { return 99; } return __rc_underflow(); }`, 0, "call __fn___fern_alloc_reuse"},
	// Family 1d/2e — STRING fields (#4356 divergence 1): the old buffer is
	// released with the rc-aware __fern_str_free on the reuse arm; both
	// sides' string values are fresh-gated (literal / fresh concat). Covers
	// the self-overwrite override, the carried copy, and the cross family.
	{"self-overwrite-string-override", `struct N { id: i32, name: string } function main(): i32 { var d = N { id: 1, name: "ab" + "c" }; var c = N { ...d, name: "wxyz" + "q" }; return c.name.len() as i32 + c.id; }`, 6, "call __fn___fern_alloc_reuse"},
	{"self-overwrite-string-override-detector", `struct N { id: i32, name: string } function main(): i32 { var d = N { id: 1, name: "ab" + "c" }; var c = N { ...d, name: "wxyz" + "q" }; var s: i32 = c.name.len() as i32 + c.id; if (s != 6) { return 99; } return __rc_underflow(); }`, 0, "call __fn___fern_alloc_reuse"},
	{"self-overwrite-string-carried-detector", `struct N { id: i32, name: string } function main(): i32 { var d = N { id: 1, name: "ab" + "c" }; var c = N { ...d, id: 2 }; var s: i32 = c.name.len() as i32 + c.id; if (s != 5) { return 99; } return __rc_underflow(); }`, 0, "call __fn___fern_alloc_reuse"},
	{"cross-struct-string-field-detector", `struct N { id: i32, name: string } function main(): i32 { var d = N { id: 1, name: "ab" + "c" }; var u: i32 = d.name.len() as i32 + d.id; var c = N { id: 2, name: "wxyz" + "q" }; var s: i32 = c.name.len() as i32 + c.id + u; if (s != 11) { return 99; } return __rc_underflow(); }`, 0, "call __fn___fern_alloc_reuse"},
	// Family 1e/2f — MAP fields (#4356 divergence 1): maps are leak-only on
	// the IR path (a map box is never freed anywhere), so the reuse arms
	// carry NO release, NO carried-copy inc, and NO freshness gate for a map
	// field — overwriting one leaks it exactly as the normal drop path would,
	// and a copied map pointer can never dangle. Covers the self-overwrite
	// carried copy, the override, and the cross family.
	{"self-overwrite-map-carried-detector", `struct P { id: i32, m: Map[i32, i32] } function main(): i32 { var d = P { id: 1, m: Map { 1: 10 } }; var c = P { ...d, id: 2 }; var s: i32 = c.m.get_or(1, 0) + c.id; if (s != 12) { return 99; } return __rc_underflow(); }`, 0, "call __fn___fern_alloc_reuse"},
	{"self-overwrite-map-override", `struct P { id: i32, m: Map[i32, i32] } function main(): i32 { var d = P { id: 1, m: Map { 1: 10 } }; var c = P { ...d, m: Map { 1: 39 } }; return c.m.get_or(1, 0) + c.id; }`, 40, "call __fn___fern_alloc_reuse"},
	{"self-overwrite-map-override-detector", `struct P { id: i32, m: Map[i32, i32] } function main(): i32 { var d = P { id: 1, m: Map { 1: 10 } }; var c = P { ...d, m: Map { 1: 39 } }; var s: i32 = c.m.get_or(1, 0) + c.id; if (s != 40) { return 99; } return __rc_underflow(); }`, 0, "call __fn___fern_alloc_reuse"},
	{"cross-struct-map-field-detector", `struct P { id: i32, m: Map[i32, i32] } function main(): i32 { var d = P { id: 1, m: Map { 1: 10 } }; var u: i32 = d.m.get_or(1, 0) + d.id; var c = P { id: 2, m: Map { 1: 7 } }; var s: i32 = c.m.get_or(1, 0) + c.id + u; if (s != 20) { return 99; } return __rc_underflow(); }`, 0, "call __fn___fern_alloc_reuse"},
	// Family 1f/2g — TUPLE and OPTION fields (#4356 divergence 1): both are
	// leak-only boxes (a tuple box is exit-swept only as a fresh non-escaping
	// scalar-literal local; an Option box never), so like maps the reuse arms
	// carry no release / inc / gate — the value contract is intact reads
	// through the reused box and clean detectors.
	{"self-overwrite-tuple-carried-detector", `struct P { id: i32, pr: (i32, i32) } function main(): i32 { var d = P { id: 1, pr: (10, 20) }; var c = P { ...d, id: 2 }; var s: i32 = c.pr.0 + c.pr.1 + c.id; if (s != 32) { return 99; } return __rc_underflow(); }`, 0, "call __fn___fern_alloc_reuse"},
	{"self-overwrite-tuple-override-detector", `struct P { id: i32, pr: (i32, i32) } function main(): i32 { var d = P { id: 1, pr: (10, 20) }; var c = P { ...d, pr: (7, 8) }; var s: i32 = c.pr.0 + c.pr.1 + c.id; if (s != 16) { return 99; } return __rc_underflow(); }`, 0, "call __fn___fern_alloc_reuse"},
	{"cross-struct-tuple-field-detector", `struct P { id: i32, pr: (i32, i32) } function main(): i32 { var d = P { id: 1, pr: (10, 20) }; var u: i32 = d.pr.0 + d.id; var c = P { id: 2, pr: (7, 8) }; var s: i32 = c.pr.0 + c.pr.1 + c.id + u; if (s != 28) { return 99; } return __rc_underflow(); }`, 0, "call __fn___fern_alloc_reuse"},
	{"self-overwrite-option-carried-detector", `struct Q { id: i32, o: Option[i32] } function main(): i32 { var d = Q { id: 1, o: Some(10) }; var c = Q { ...d, id: 2 }; var r: i32 = 0; match (c.o) { Some(v) => { r = v + c.id; }, None => { r = 0; } } if (r != 12) { return 99; } return __rc_underflow(); }`, 0, "call __fn___fern_alloc_reuse"},
	{"self-overwrite-option-override-detector", `struct Q { id: i32, o: Option[i32] } function main(): i32 { var d = Q { id: 1, o: Some(10) }; var c = Q { ...d, o: Some(30) }; var r: i32 = 0; match (c.o) { Some(v) => { r = v + c.id; }, None => { r = 0; } } if (r != 31) { return 99; } return __rc_underflow(); }`, 0, "call __fn___fern_alloc_reuse"},
	// Family 3 — cross-statement tuple reuse (emit_cross_tuple_reuse).
	{"cross-tuple-loop", `function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var a: (i32, i32) = (i, i + 1); var s: i32 = a.0 + a.1; var b: (i32, i32) = (i, 3); sum = sum + s + b.0 + b.1; i = i + 1; } return sum; }`, 34, "call __fn___fern_alloc_reuse"},
	// Family 4 — consumed scalar-enum donor -> struct recipient (emit_enum_donor_reuse).
	{"enum-donor", `enum E { A(i32, i32), B(i32, i32) } struct W { p: i32, q: i32 } function main(): i32 { var x = A(10, 20); var t = 0; match (x) { A(a, b) => { t = a + b; }, B(c, d) => { t = c - d; }, } var y = W { p: 3, q: 4 }; return t + y.p + y.q; }`, 37, "call __fn___fern_alloc_reuse"},
	{"enum-donor-detector", `enum E { A(i32, i32), B(i32, i32) } struct W { p: i32, q: i32 } function main(): i32 { var x = A(10, 20); var t = 0; match (x) { A(a, b) => { t = a + b; }, B(c, d) => { t = c - d; }, } var y = W { p: 3, q: 4 }; var s = t + y.p + y.q; if (s != 37) { return 99; } return __rc_underflow(); }`, 0, "call __fn___fern_alloc_reuse"},
	// Enum-donor recipient WIDENED to the cross field-kind set (the reuse-audit
	// follow-through): a recipient with an ENUM field (fresh variant-ctor value;
	// released at exit via the k_enum drop arm) or a leak-only TUPLE field now
	// reuses a consumed scalar-enum donor's box. The donor's old slots are all
	// scalars, so the emitter's no-release field writes stay sound; the fresh
	// values are sole-owned (no alias-inc). Exit codes cross-checked against
	// native -interp (18 / 24).
	{"enum-donor-enum-field-recipient", `enum St { On(i32), Off } enum D2 { P(i32, i32), Q } struct M { tag: i32, st: St } function main(): i32 { var x: D2 = P(3, 4); var u: i32 = 0; match (x) { P(a, b) => { u = a + b; }, Q => { u = 0; } } var y = M { tag: 2, st: On(9) }; var r: i32 = 0; match (y.st) { On(v) => { r = v + y.tag + u; }, Off => { r = 0; } } return r; }`, 18, "call __fn___fern_alloc_reuse"},
	{"enum-donor-enum-field-recipient-detector", `enum St { On(i32), Off } enum D2 { P(i32, i32), Q } struct M { tag: i32, st: St } function main(): i32 { var x: D2 = P(3, 4); var u: i32 = 0; match (x) { P(a, b) => { u = a + b; }, Q => { u = 0; } } var y = M { tag: 2, st: On(9) }; var r: i32 = 0; match (y.st) { On(v) => { r = v + y.tag + u; }, Off => { r = 0; } } if (r != 18) { return 99; } return __rc_underflow(); }`, 0, "call __fn___fern_alloc_reuse"},
	{"enum-donor-tuple-field-recipient", `enum D2 { P(i32, i32), Q } struct T { tag: i32, t: (i32, i32) } function main(): i32 { var x: D2 = P(3, 4); var u: i32 = 0; match (x) { P(a, b) => { u = a + b; }, Q => { u = 0; } } var y = T { tag: 2, t: (7, 8) }; return y.tag + y.t.0 + y.t.1 + u; }`, 24, "call __fn___fern_alloc_reuse"},
	{"enum-donor-tuple-field-recipient-detector", `enum D2 { P(i32, i32), Q } struct T { tag: i32, t: (i32, i32) } function main(): i32 { var x: D2 = P(3, 4); var u: i32 = 0; match (x) { P(a, b) => { u = a + b; }, Q => { u = 0; } } var y = T { tag: 2, t: (7, 8) }; var s: i32 = y.tag + y.t.0 + y.t.1 + u; if (s != 24) { return 99; } return __rc_underflow(); }`, 0, "call __fn___fern_alloc_reuse"},
	// Family 5 — enum->enum cross-local reuse (emit_enum_cross_reuse).
	{"enum-cross", `enum E { A(i32[]), B(i32[]) } function f(): i32 { var a: E = A([1, 2]); var t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_) => { t = 6; } } var c: E = B([3, 4]); var v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w) => { v = w[0] + w[1]; } } return t + v; } function main(): i32 { return f(); }`, 12, "call __fn___fern_alloc_reuse"},
	{"enum-cross-detector", `enum E { A(i32[]), B(i32[]) } function f(): i32 { var a: E = A([1, 2]); var t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_) => { t = 6; } } var c: E = B([3, 4]); var v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w) => { v = w[0] + w[1]; } } if (t + v != 12) { return 99; } return __rc_underflow(); } function main(): i32 { return f(); }`, 0, "call __fn___fern_alloc_reuse"},
	// Family 6 — in-arm consuming-match reuse (emit_inarm_match_reuse), scalar +
	// the two array cow-guard shapes (same-slot MOVE and fresh-literal REPLACE).
	{"inarm-scalar", `enum E { V(i32, i32), W(i32, i32) } function go(): i32 { var x = V(3, 4); var y = match (x) { V(a, b) => W(a + 1, b + 1), W(c, d) => V(c, d) }; var r = match (y) { V(a, b) => a + b, W(c, d) => c + d }; return r; } function main(): i32 { return go(); }`, 9, "call __fn___fern_alloc_reuse"},
	{"inarm-array-move", `enum E { V(i32, i32[]), W(i32, i32[]) } function go(): i32 { var x = V(3, [10, 20, 30]); var y = match (x) { V(a, xs) => W(a + 1, xs), W(b, ys) => V(b, ys) }; var r = 0; match (y) { V(a, xs) => { r = a + xs[0] + xs[1] + xs[2]; }, W(c, ds) => { r = c + ds[0] + ds[1] + ds[2]; } } return r; } function main(): i32 { return go(); }`, 64, "call __fn___fern_alloc_reuse"},
	{"inarm-array-replace-detector", `enum E { V(i32, i32[]), W(i32, i32[]) } function go(): i32 { var x = V(3, [10, 20, 30]); var y = match (x) { V(a, xs) => W(a, [7, 8]), W(b, ys) => V(b, ys) }; var r = 0; match (y) { V(a, xs) => { r = a + xs[0] + xs[1]; }, W(c, ds) => { r = c + ds[0] + ds[1]; } } if (r != 18) { return 99; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0, "call __fn___fern_alloc_reuse"},
	// string[] fields (#4356 Delta B, rc-element arrays): admitted to the
	// cross / self-overwrite families with element-fresh array-literal values
	// gated on BOTH sides (strarr_lit_all_elems_fresh in donor_enum_fields_fresh
	// / cross_recipient_fields_fresh / the override walk); the reuse arm
	// deep-frees the superseded field via __fern_str_arr_free and the
	// self-overwrite fresh arm rc-incs carried copies. Exit codes cross-checked
	// against native -interp (6 / 4 / 9); detectors prove no over-release.
	{"strarr-field-cross", `struct P { tags: string[], n: i32 } function main(): i32 { var a: P = P { tags: ["x", "y"], n: 1 }; var s1: i32 = a.tags.len() + a.n; var b: P = P { tags: ["z"], n: 2 }; if (__rc_underflow() != 0) { return 99; } return s1 + b.tags.len() + b.n; }`, 6, "call __fn___fern_alloc_reuse"},
	{"strarr-field-self-overwrite", `struct P { tags: string[], n: i32 } function main(): i32 { var d: P = P { tags: ["x", "y"], n: 1 }; var c: P = P { ...d, tags: ["z", "w", "v"] }; if (__rc_underflow() != 0) { return 99; } return c.tags.len() + c.n; }`, 4, "call __fn___fern_alloc_reuse"},
	{"strarr-field-carried-copy", `struct P { tags: string[], n: i32 } function main(): i32 { var d: P = P { tags: ["x", "y"], n: 1 }; var c: P = P { ...d, n: 5 }; if (__rc_underflow() != 0) { return 99; } return c.tags.len() + c.n + c.tags[0].len() + c.tags[1].len(); }`, 9, "call __fn___fern_alloc_reuse"},
	{"strarr-field-churn-detector", `struct P { tags: string[], n: i32 } function churn(n: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < n) { var d: P = P { tags: ["x", "y"], n: i }; var c: P = P { ...d, tags: ["z"] }; if (c.tags.len() + c.n != 1 + i) { bad = 1; } i = i + 1; } return bad; } function main(): i32 { var v: i32 = churn(2000000); if (v != 0) { return 90; } return __rc_underflow(); }`, 0, "call __fn___fern_alloc_reuse"},
	// fn (closure) fields (#4356 Delta B, native's FuncType kind): admitted to
	// the cross / self-overwrite / enum-donor families. The coarse "fn"
	// spelling reads as enum-like, so the freshness walks test fn BEFORE their
	// enum arm (fn_field_value_is_fresh: a lambda literal or its lifted
	// __mkclo$ spelling) and the enum-like release arm's shallow rc-guarded
	// dec IS the k_clo env-box release. A donor whose own closure field is
	// CALLED stays conservatively excluded by the general escape walk (a
	// method-shaped receiver use) — same as every other field kind. Exit
	// codes cross-checked against native -interp (17 / 21 / 15 / 11).
	{"fn-field-cross", `struct H { f: (i32) => i32, id: i32 } function main(): i32 { var a: H = H { f: function (x: i32): i32 { return x + 3; }, id: 1 }; var s1: i32 = a.id + 4; var b: H = H { f: function (x: i32): i32 { return x * 2; }, id: 2 }; return s1 + b.f(5) + b.id; }`, 17, "call __fn___fern_alloc_reuse"},
	{"fn-field-self-overwrite", `struct H { f: (i32) => i32, id: i32 } function main(): i32 { var d: H = H { f: function (x: i32): i32 { return x + 3; }, id: 1 }; var c: H = H { ...d, f: function (x: i32): i32 { return x * 4; } }; if (__rc_underflow() != 0) { return 99; } return c.f(5) + c.id; }`, 21, "call __fn___fern_alloc_reuse"},
	{"fn-field-carried-copy", `struct H { f: (i32) => i32, id: i32 } function main(): i32 { var d: H = H { f: function (x: i32): i32 { return x + 3; }, id: 1 }; var c: H = H { ...d, id: 7 }; if (__rc_underflow() != 0) { return 99; } return c.f(5) + c.id; }`, 15, "call __fn___fern_alloc_reuse"},
	{"fn-field-churn-detector", `struct H { f: (i32) => i32, id: i32 } function churn(n: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < n) { var d: H = H { f: function (x: i32): i32 { return x + 1; }, id: i }; var c: H = H { ...d, f: function (x: i32): i32 { return x + 2; } }; if (c.f(10) + c.id != 12 + i) { bad = 1; } i = i + 1; } return bad; } function main(): i32 { var v: i32 = churn(2000000); if (v != 0) { return 90; } return __rc_underflow(); }`, 0, "call __fn___fern_alloc_reuse"},
	{"enum-donor-fn-field-recipient", `enum D2 { P(i32, i32), Q } struct M { tag: i32, g: (i32) => i32 } function main(): i32 { var x: D2 = P(3, 4); var u: i32 = 0; match (x) { P(a, b) => { u = a + b; }, Q => { u = 0; } } var y = M { tag: 2, g: function (q: i32): i32 { return q + 1; } }; return y.g(1) + y.tag + u; }`, 11, "call __fn___fern_alloc_reuse"},
	// struct[] / enum[] box-element-array fields (#4356 Delta B, the last
	// rc-element-array kind): admitted with element-fresh array-literal
	// values on both sides (boxarr_lit_all_elems_fresh) and released
	// per-element via __fern_arrarr_free; struct[] restricted to
	// scalar-field element types (nothing under the box to leak). Exit
	// codes cross-checked against native -interp (15 / 0 / 13 / 17 / 16).
	{"boxarr-struct-cross", `struct In { k: i32, n: i32 } struct W { items: In[], id: i32 } function main(): i32 { var a: W = W { items: [In { k: 1, n: 2 }, In { k: 3, n: 4 }], id: 1 }; var s1: i32 = a.items.len() + a.items[1].k + a.id; var b: W = W { items: [In { k: 5, n: 6 }], id: 2 }; if (__rc_underflow() != 0) { return 99; } return s1 + b.items.len() + b.items[0].n + b.id; }`, 15, "call __fn___fern_alloc_reuse"},
	{"boxarr-struct-churn-detector", `struct In { k: i32, n: i32 } struct W { items: In[], id: i32 } function churn(n: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < n) { var a: W = W { items: [In { k: i, n: 2 }, In { k: 3, n: 4 }], id: i }; var t: i32 = a.items.len() + a.items[0].k + a.id; var b: W = W { items: [In { k: 5, n: i }], id: i + 1 }; if (t != 2 + i + i) { bad = 1; } if (b.items.len() + b.items[0].n + b.id != 2 + i + i) { bad = 1; } i = i + 1; } return bad; } function main(): i32 { var v: i32 = churn(1000000); if (v != 0) { return 90; } return __rc_underflow(); }`, 0, "call __fn___fern_alloc_reuse"},
	{"boxarr-enum-cross", `enum St { On(i32), Off } struct W { sts: St[], id: i32 } function main(): i32 { var a: W = W { sts: [On(3), Off], id: 1 }; var s1: i32 = a.sts.len() + a.id; var b: W = W { sts: [On(7)], id: 2 }; var s2: i32 = b.sts.len() + b.id; match (b.sts[0]) { On(v) => { s2 = s2 + v; }, Off => {} } if (__rc_underflow() != 0) { return 99; } return s1 + s2; }`, 13, "call __fn___fern_alloc_reuse"},
	{"boxarr-self-overwrite", `struct In { k: i32, n: i32 } struct W { items: In[], id: i32 } function main(): i32 { var d: W = W { items: [In { k: 1, n: 2 }], id: 6 }; var c: W = W { ...d, items: [In { k: 7, n: 8 }, In { k: 9, n: 10 }] }; if (__rc_underflow() != 0) { return 99; } return c.items.len() + c.items[1].k + c.id; }`, 17, "call __fn___fern_alloc_reuse"},
	{"boxarr-carried-copy", `struct In { k: i32, n: i32 } struct W { items: In[], id: i32 } function main(): i32 { var d: W = W { items: [In { k: 1, n: 2 }, In { k: 3, n: 4 }], id: 6 }; var c: W = W { ...d, id: 9 }; if (__rc_underflow() != 0) { return 99; } return c.items.len() + c.items[0].k + c.items[1].n + c.id; }`, 16, "call __fn___fern_alloc_reuse"},
	// MIXED-field interaction shapes (the seams between the fn / string[] /
	// string / enum / nested-struct admissions): one struct carrying several
	// release-armed kinds at once, under cross reuse and a churn-scale
	// self-overwrite. Exit codes cross-checked against native -interp
	// (19 / 0 / 118); the detectors prove no arm double-fires.
	{"mixed-fn-strarr-str-cross", `struct W { f: (i32) => i32, tags: string[], name: string, id: i32 } function main(): i32 { var a: W = W { f: function (x: i32): i32 { return x + 1; }, tags: ["p", "q"], name: "aa", id: 1 }; var s1: i32 = a.id + a.tags.len() + a.name.len(); var b: W = W { f: function (x: i32): i32 { return x * 2; }, tags: ["r"], name: "bbb", id: 2 }; if (__rc_underflow() != 0) { return 99; } return s1 + b.f(4) + b.tags.len() + b.name.len() + b.id; }`, 19, "call __fn___fern_alloc_reuse"},
	{"mixed-selfoverwrite-churn-detector", `struct W { f: (i32) => i32, tags: string[], name: string, id: i32 } function churn(n: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < n) { var d: W = W { f: function (x: i32): i32 { return x + 1; }, tags: ["p", "q"], name: "aa", id: i }; var c: W = W { ...d, f: function (x: i32): i32 { return x + 2; }, tags: ["z"] }; if (c.f(10) + c.tags.len() + c.name.len() + c.id != 15 + i) { bad = 1; } i = i + 1; } return bad; } function main(): i32 { var v: i32 = churn(1000000); if (v != 0) { return 90; } return __rc_underflow(); }`, 0, "call __fn___fern_alloc_reuse"},
	{"mixed-enum-fn-cross", `enum St { On(i32), Off } struct W { f: (i32) => i32, st: St, id: i32 } function main(): i32 { var a: W = W { f: function (x: i32): i32 { return x + 1; }, st: On(7), id: 1 }; var s1: i32 = a.id; match (a.st) { On(v) => { s1 = s1 + v; }, Off => {} } var b: W = W { f: function (x: i32): i32 { return x * 2; }, st: Off, id: 2 }; var s2: i32 = b.f(4) + b.id; match (b.st) { On(v) => { s2 = s2 + v; }, Off => { s2 = s2 + 100; } } if (__rc_underflow() != 0) { return 99; } return s1 + s2; }`, 118, "call __fn___fern_alloc_reuse"},
	// ENUMRE — the in-place enum reassign upgrade (emit_enum_inplace_reassign),
	// gated with the layer; reuse-off falls back to emit_enum_reclaim_store's
	// free+alloc. No distinct call symbol, so no witness string — the asm
	// inequality check below pins that the switch changed the lowering.
	{"enumre-inplace-churn", `enum Bag { Keep(i32[]), Swap(i32[]) } function churn(n: i32): i32 { var b: Bag = Keep([0, 0, 0, 0]); var i = 0; while (i < n) { b = Keep([i, i, i, i]); b = Swap([i, i, i, i]); i = i + 1; } var r = 0; match (b) { Keep(_) => { r = 1; }, Swap(_) => { r = 2; }, } if (r != 2) { return 99; } return __rc_underflow(); } function main(): i32 { return churn(5); }`, 0, ""},
}

// TestSelfHostReuseDifferentialX86_64 compiles each firing case TWICE through
// the self-hosted x86-64 driver — once normally (reuse on) and once with
// FERN_SELFHOST_NO_REUSE=1 (reuse off) — and asserts (a) the switch actually
// changed the lowering (asm differs; the ON asm carries the family's firing
// witness, the OFF asm does not), and (b) both binaries exit identically.
func TestSelfHostReuseDifferentialX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

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
		cmd.Env = append(os.Environ(), extraEnv...)
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
			asmOn := emitEnv(t, tc.src)
			asmOff := emitEnv(t, tc.src, "FERN_SELFHOST_NO_REUSE=1")
			if asmOn == asmOff {
				t.Errorf("%s: reuse-on and reuse-off asm are identical — the switch did not change the lowering (not a firing shape?)", tc.name)
			}
			if tc.witness != "" {
				if !strings.Contains(asmOn, tc.witness) {
					t.Errorf("%s: reuse-on asm lacks firing witness %q", tc.name, tc.witness)
				}
				if strings.Contains(asmOff, tc.witness) {
					t.Errorf("%s: reuse-off asm still contains %q — the reuse was not suppressed", tc.name, tc.witness)
				}
			}
			onBin := buildBin(t, gcc, dir, tc.name+"-on", asmOn)
			offBin := buildBin(t, gcc, dir, tc.name+"-off", asmOff)
			gotOn := runBin(t, onBin)
			gotOff := runBin(t, offBin)
			if gotOn != gotOff {
				t.Errorf("%s: OBSERVATIONAL DIVERGENCE — reuse-on exited %d, reuse-off %d", tc.name, gotOn, gotOff)
			}
			if gotOn != tc.want {
				t.Errorf("%s: reuse-on exited %d, want %d", tc.name, gotOn, tc.want)
			}
			if gotOff != tc.want {
				t.Errorf("%s: reuse-off exited %d, want %d", tc.name, gotOff, tc.want)
			}
		})
	}
}

// TestSelfHostStrarrReuseExclusionX86_64 pins the string[] AND fn reuse
// admissions' NEGATIVE space: an ALIASED value (a bare local ident as a donor
// field / a self-overwrite override) fails the freshness gate
// (strarr_lit_all_elems_fresh / fn_field_value_is_fresh), so no reuse fires —
// the emitted asm carries no __fern_alloc_reuse call — and the alias stays
// usable after the second construction (values cross-checked against native
// -interp: 10 / 4 / 25 / 18 / 8 / 12), with the rc-underflow detector clean.
func TestSelfHostStrarrReuseExclusionX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"aliased-donor-field", `struct P { tags: string[], n: i32 } function main(): i32 { var xs: string[] = ["k", "m"]; var a: P = P { tags: xs, n: 1 }; var s1: i32 = a.tags.len() + a.n; var b: P = P { tags: ["z"], n: 2 }; var live: i32 = xs.len() + xs[0].len() + xs[1].len(); if (__rc_underflow() != 0) { return 99; } return s1 + b.tags.len() + b.n + live; }`, 10},
		{"aliased-override", `struct P { tags: string[], n: i32 } function main(): i32 { var xs: string[] = ["k"]; var d: P = P { tags: ["x"], n: 1 }; var c: P = P { ...d, tags: xs, n: 2 }; var live: i32 = xs[0].len(); if (__rc_underflow() != 0) { return 99; } return c.tags.len() + c.n + live; }`, 4},
		{"aliased-fn-donor-field", `struct H { f: (i32) => i32, id: i32 } function main(): i32 { var g = function (x: i32): i32 { return x * 2; }; var a: H = H { f: g, id: 1 }; var s1: i32 = a.f(5) + a.id; var b: H = H { f: function (x: i32): i32 { return x + 1; }, id: 2 }; var live: i32 = g(3); if (__rc_underflow() != 0) { return 99; } return s1 + b.f(5) + b.id + live; }`, 25},
		{"aliased-fn-override", `struct H { f: (i32) => i32, id: i32 } function main(): i32 { var g = function (x: i32): i32 { return x * 2; }; var d: H = H { f: function (x: i32): i32 { return x + 1; }, id: 1 }; var c: H = H { ...d, f: g, id: 2 }; var live: i32 = g(3); if (__rc_underflow() != 0) { return 99; } return c.f(5) + c.id + live; }`, 18},
		{"aliased-boxarr-donor-field", `struct In { k: i32, n: i32 } struct W { items: In[], id: i32 } function main(): i32 { var xs: In[] = [In { k: 1, n: 2 }]; var a: W = W { items: xs, id: 1 }; var s1: i32 = a.items.len() + a.id; var b: W = W { items: [In { k: 5, n: 6 }], id: 2 }; var live: i32 = xs[0].k + xs[0].n; if (__rc_underflow() != 0) { return 99; } return s1 + b.items.len() + b.id + live; }`, 8},
		{"rcfield-element-type-excluded", `struct In2 { xs: i32[], k: i32 } struct W { items: In2[], id: i32 } function main(): i32 { var a: W = W { items: [In2 { xs: [1, 2], k: 3 }], id: 1 }; var s1: i32 = a.items.len() + a.items[0].k + a.id; var b: W = W { items: [In2 { xs: [4], k: 5 }], id: 2 }; if (__rc_underflow() != 0) { return 99; } return s1 + b.items.len() + b.items[0].xs[0] + b.id; }`, 12},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if strings.Contains(string(asm), "call __fn___fern_alloc_reuse") {
				t.Errorf("%s: asm contains an alloc_reuse call — the aliased string[] value was wrongly admitted to reuse", tc.name)
			}
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
