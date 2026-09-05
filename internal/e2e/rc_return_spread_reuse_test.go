package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Return-position struct-update reuse (computeReturnSpreadReuse): `return T {
// ...p, f: v }` writes p's own box in place when the frame owns it and it is
// uniquely referenced at run time, instead of filling a fresh box, retaining
// every carried field into it, and deep-dropping p on the way out.
//
// The shape is the state threading every self-host emitter is built out of
// (`s = s.emit(op)`), and an rc miscount here is a leak or a double free in
// every one of them — so each case below asserts the VALUES first (a wrong box
// or a freed field shows up there) and then returns __rc_underflow_count(),
// which is non-zero on any over-release.
//
// The two gates are: the frame owns p (freeEligible — an owned-by-default
// parameter, whose caller retained an argument it keeps and moved one it does
// not), and the runtime is_unique test at the site (a surviving caller-side
// alias makes rc>1, so the reuse declines to a fresh box and the alias keeps
// the old value intact). The `survives` cases below are that second gate.

// threading: the measured shape, 200 updates deep, with an array field grown
// per call and a string + bool carried the whole way.
const retSpreadThreadingSrc = `struct St { ops: i32[], tag: string, ok: boolean, ctrl: i32 }
function (s: St) emit(op: i32): St {
    var nctrl: i32 = s.ctrl;
    if (op == 3) { nctrl = s.ctrl + 1; }
    return St { ...s, ops: s.ops.append(op), ctrl: nctrl };
}
function main(): i32 {
    var s: St = St { ops: [], tag: "abc", ok: true, ctrl: 0 };
    var i: i32 = 0;
    while (i < 200) { s = s.emit(i); i = i + 1; }
    var sum: i32 = 0;
    for v in s.ops { sum = sum + v; }
    if (s.ops.len() != 200) { return 101; }
    if (sum != 19900) { return 102; }
    if (s.ctrl != 1) { return 103; }
    if (s.tag.len() != 3) { return 104; }
    if (!s.ok) { return 105; }
    return __rc_underflow_count();
}`

// method_recv_survives: the caller keeps `a` across the call, so the caller
// retains its argument and the callee sees rc>1 — the reuse must decline and
// leave a's box, its array and its string exactly as they were.
const retSpreadRecvSurvivesSrc = `struct St { ops: i32[], tag: string, ctrl: i32 }
function (s: St) emit(op: i32): St { return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl + 1 }; }
function main(): i32 {
    var a: St = St { ops: [], tag: "xy", ctrl: 0 };
    var i: i32 = 0;
    while (i < 5) { a = a.emit(i); i = i + 1; }
    var b: St = a.emit(9);
    if (a.ops.len() != 5) { return 101; }
    if (b.ops.len() != 6) { return 102; }
    if (b.ops[5] != 9) { return 103; }
    if (a.ctrl != 5) { return 104; }
    if (b.ctrl != 6) { return 105; }
    if (a.tag.len() != 2 || b.tag.len() != 2) { return 106; }
    return __rc_underflow_count();
}`

// free_param_survives: the same hole through a free function's parameter.
const retSpreadFreeParamSurvivesSrc = `struct St { ops: i32[], ctrl: i32 }
function bump(s: St, v: i32): St { return St { ...s, ops: s.ops.append(v), ctrl: s.ctrl + 1 }; }
function main(): i32 {
    var a: St = St { ops: [1, 2], ctrl: 0 };
    var c: St = bump(a, 7);
    var d: St = bump(a, 8);
    if (a.ops.len() != 2) { return 101; }
    if (c.ops.len() != 3 || c.ops[2] != 7) { return 102; }
    if (d.ops.len() != 3 || d.ops[2] != 8) { return 103; }
    if (a.ctrl != 0 || c.ctrl != 1 || d.ctrl != 1) { return 104; }
    return __rc_underflow_count();
}`

// alias_in_callee: the callee itself aliases the receiver before the update,
// so rc>1 there — the decline arm dec's the reference the frame owns and the
// alias reads the ORIGINAL values out of the box that was not repurposed.
const retSpreadAliasInCalleeSrc = `struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(op: i32): St {
    var keep: St = s;
    return St { ...s, ops: s.ops.append(op), ctrl: keep.ctrl + 1 };
}
function main(): i32 {
    var s: St = St { ops: [], ctrl: 0 };
    var i: i32 = 0;
    while (i < 50) { s = s.emit(i); i = i + 1; }
    if (s.ops.len() != 50) { return 101; }
    if (s.ctrl != 50) { return 102; }
    if (s.ops[49] != 49) { return 103; }
    return __rc_underflow_count();
}`

// replaced_ptr_field: the displaced array is released on the reuse branch. A
// FRESH array each turn means the old one reaches rc 0 and is freed (a flat dec
// would strand it); the loop's values only come out right if the store landed
// in the reused box.
const retSpreadReplacedPtrSrc = `struct St { items: i32[], n: i32 }
function (s: St) reset(v: i32): St { return St { ...s, items: [v, v + 1, v + 2], n: s.n + 1 }; }
function main(): i32 {
    var s: St = St { items: [0], n: 0 };
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        s = s.reset(i);
        acc = acc + s.items[0] + s.items[2];
        i = i + 1;
    }
    // (i) + (i+2) = 2i+2; sum i=0..199 = 2*19900 + 400 = 40200
    if (acc != 40200) { return 101; }
    if (s.n != 200) { return 102; }
    if (s.items.len() != 3) { return 103; }
    return __rc_underflow_count();
}`

// nested_struct_field: a struct-typed field (the LowerFrame shape) is carried
// through every update, so the reuse branch must leave the reference it holds
// alone — releasing it would free the frame out from under the result.
const retSpreadNestedStructSrc = `struct Frame { name: string, depth: i32 }
struct St { frame: Frame, ops: i32[], ctrl: i32 }
function (s: St) emit(op: i32): St { return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl + op }; }
function main(): i32 {
    var s: St = St { frame: Frame { name: "f", depth: 3 }, ops: [], ctrl: 0 };
    var i: i32 = 0;
    while (i < 100) { s = s.emit(i); i = i + 1; }
    if (s.frame.depth != 3) { return 101; }
    if (s.frame.name.len() != 1) { return 102; }
    if (s.ops.len() != 100) { return 103; }
    if (s.ctrl != 4950) { return 104; }
    return __rc_underflow_count();
}`

// own_param: an explicitly consumed parameter is the frame's outright, and the
// caller moved its reference — the reuse fires and nothing double-releases.
const retSpreadOwnParamSrc = `struct St { ops: i32[], ctrl: i32 }
function eat(own s: St, v: i32): St { return St { ...s, ops: s.ops.append(v), ctrl: s.ctrl + 1 }; }
function main(): i32 {
    var s: St = St { ops: [], ctrl: 0 };
    var i: i32 = 0;
    while (i < 50) { s = eat(s, i); i = i + 1; }
    if (s.ops.len() != 50) { return 101; }
    if (s.ops[49] != 49) { return 102; }
    if (s.ctrl != 50) { return 103; }
    return __rc_underflow_count();
}`

// address_taken: reached through a function value, so the parameter is
// borrowed — no call-site retain exists to pay for a repurposed box. The
// caller's `keep` must still read the original after the call.
const retSpreadAddressTakenSrc = `struct St { ops: i32[], ctrl: i32 }
function bump(s: St): St { return St { ...s, ops: s.ops.append(9), ctrl: s.ctrl + 1 }; }
function apply(f: (St) => St, s: St): St { return f(s); }
function main(): i32 {
    var keep: St = St { ops: [1, 2], ctrl: 0 };
    var out: St = apply(bump, keep);
    if (keep.ops.len() != 2) { return 101; }
    if (out.ops.len() != 3 || out.ops[2] != 9) { return 102; }
    if (keep.ctrl != 0 || out.ctrl != 1) { return 103; }
    return __rc_underflow_count();
}`

// two_returns: two update sites in one function, on different branches. Each
// empties the parameter's slot on the path that runs; the other never does.
const retSpreadTwoReturnsSrc = `struct St { ops: i32[], tag: string, ctrl: i32 }
function (s: St) step(v: i32): St {
    if (v % 2 == 0) { return St { ...s, ops: s.ops.append(v), ctrl: s.ctrl + 1 }; }
    return St { ...s, ctrl: s.ctrl - 1 };
}
function main(): i32 {
    var s: St = St { ops: [], tag: "zz", ctrl: 0 };
    var i: i32 = 0;
    while (i < 60) { s = s.step(i); i = i + 1; }
    if (s.ops.len() != 30) { return 101; }
    if (s.ctrl != 0) { return 102; }
    if (s.tag.len() != 2) { return 103; }
    return __rc_underflow_count();
}`

var retSpreadReuseCases = []struct{ name, src string }{
	{"threading", retSpreadThreadingSrc},
	{"method_recv_survives", retSpreadRecvSurvivesSrc},
	{"free_param_survives", retSpreadFreeParamSurvivesSrc},
	{"alias_in_callee", retSpreadAliasInCalleeSrc},
	{"replaced_ptr_field", retSpreadReplacedPtrSrc},
	{"nested_struct_field", retSpreadNestedStructSrc},
	{"own_param", retSpreadOwnParamSrc},
	{"address_taken", retSpreadAddressTakenSrc},
	{"two_returns", retSpreadTwoReturnsSrc},
}

func TestX86_64ReturnSpreadReuse(t *testing.T) {
	for _, c := range retSpreadReuseCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64FreeOn(t, c.src); code != 0 {
				t.Errorf("%s: got %d, want 0", c.name, code)
			}
		})
	}
}

func TestArm64ReturnSpreadReuse(t *testing.T) {
	for _, c := range retSpreadReuseCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64FreeOn(t, c.src); code != 0 {
				t.Errorf("%s: got %d, want 0", c.name, code)
			}
		})
	}
}

func TestWASMReturnSpreadReuse(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	for _, c := range retSpreadReuseCases {
		t.Run(c.name, func(t *testing.T) {
			if got := runWasm(t, c.src); got != 0 {
				t.Errorf("%s: got %d, want 0", c.name, got)
			}
		})
	}
}
