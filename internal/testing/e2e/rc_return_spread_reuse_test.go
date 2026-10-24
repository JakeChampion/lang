package e2e

import (
	"testing"
)

const retSpreadThreadingSrc = `struct St { ops: i32[], tag: string, ok: boolean, ctrl: i32 }
function (s: St) emit(op: i32): St {
    let nctrl: i32 = s.ctrl;
    if (op == 3) { nctrl = s.ctrl + 1; }
    return St { ...s, ops: s.ops.append(op), ctrl: nctrl };
}
function main(): i32 {
    let s: St = St { ops: [], tag: "abc", ok: true, ctrl: 0 };
    let i: i32 = 0;
    while (i < 200) { s = s.emit(i); i = i + 1; }
    let sum: i32 = 0;
    for v in s.ops { sum = sum + v; }
    if (s.ops.len() != 200) { return 101; }
    if (sum != 19900) { return 102; }
    if (s.ctrl != 1) { return 103; }
    if (s.tag.len() != 3) { return 104; }
    if (!s.ok) { return 105; }
    return __rc_underflow_count();
}`

const retSpreadRecvSurvivesSrc = `struct St { ops: i32[], tag: string, ctrl: i32 }
function (s: St) emit(op: i32): St { return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl + 1 }; }
function main(): i32 {
    let a: St = St { ops: [], tag: "xy", ctrl: 0 };
    let i: i32 = 0;
    while (i < 5) { a = a.emit(i); i = i + 1; }
    let b: St = a.emit(9);
    if (a.ops.len() != 5) { return 101; }
    if (b.ops.len() != 6) { return 102; }
    if (b.ops[5] != 9) { return 103; }
    if (a.ctrl != 5) { return 104; }
    if (b.ctrl != 6) { return 105; }
    if (a.tag.len() != 2 || b.tag.len() != 2) { return 106; }
    return __rc_underflow_count();
}`

const retSpreadFreeParamSurvivesSrc = `struct St { ops: i32[], ctrl: i32 }
function bump(s: St, v: i32): St { return St { ...s, ops: s.ops.append(v), ctrl: s.ctrl + 1 }; }
function main(): i32 {
    let a: St = St { ops: [1, 2], ctrl: 0 };
    let c: St = bump(a, 7);
    let d: St = bump(a, 8);
    if (a.ops.len() != 2) { return 101; }
    if (c.ops.len() != 3 || c.ops[2] != 7) { return 102; }
    if (d.ops.len() != 3 || d.ops[2] != 8) { return 103; }
    if (a.ctrl != 0 || c.ctrl != 1 || d.ctrl != 1) { return 104; }
    return __rc_underflow_count();
}`

const retSpreadAliasInCalleeSrc = `struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(op: i32): St {
    let keep: St = s;
    return St { ...s, ops: s.ops.append(op), ctrl: keep.ctrl + 1 };
}
function main(): i32 {
    let s: St = St { ops: [], ctrl: 0 };
    let i: i32 = 0;
    while (i < 50) { s = s.emit(i); i = i + 1; }
    if (s.ops.len() != 50) { return 101; }
    if (s.ctrl != 50) { return 102; }
    if (s.ops[49] != 49) { return 103; }
    return __rc_underflow_count();
}`

const retSpreadReplacedPtrSrc = `struct St { items: i32[], n: i32 }
function (s: St) reset(v: i32): St { return St { ...s, items: [v, v + 1, v + 2], n: s.n + 1 }; }
function main(): i32 {
    let s: St = St { items: [0], n: 0 };
    let acc: i32 = 0;
    let i: i32 = 0;
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

const retSpreadNestedStructSrc = `struct Frame { name: string, depth: i32 }
struct St { frame: Frame, ops: i32[], ctrl: i32 }
function (s: St) emit(op: i32): St { return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl + op }; }
function main(): i32 {
    let s: St = St { frame: Frame { name: "f", depth: 3 }, ops: [], ctrl: 0 };
    let i: i32 = 0;
    while (i < 100) { s = s.emit(i); i = i + 1; }
    if (s.frame.depth != 3) { return 101; }
    if (s.frame.name.len() != 1) { return 102; }
    if (s.ops.len() != 100) { return 103; }
    if (s.ctrl != 4950) { return 104; }
    return __rc_underflow_count();
}`

const retSpreadOwnParamSrc = `struct St { ops: i32[], ctrl: i32 }
function eat(own s: St, v: i32): St { return St { ...s, ops: s.ops.append(v), ctrl: s.ctrl + 1 }; }
function main(): i32 {
    let s: St = St { ops: [], ctrl: 0 };
    let i: i32 = 0;
    while (i < 50) { s = eat(s, i); i = i + 1; }
    if (s.ops.len() != 50) { return 101; }
    if (s.ops[49] != 49) { return 102; }
    if (s.ctrl != 50) { return 103; }
    return __rc_underflow_count();
}`

const retSpreadAddressTakenSrc = `struct St { ops: i32[], ctrl: i32 }
function bump(s: St): St { return St { ...s, ops: s.ops.append(9), ctrl: s.ctrl + 1 }; }
function apply(f: (St) => St, s: St): St { return f(s); }
function main(): i32 {
    let keep: St = St { ops: [1, 2], ctrl: 0 };
    let out: St = apply(bump, keep);
    if (keep.ops.len() != 2) { return 101; }
    if (out.ops.len() != 3 || out.ops[2] != 9) { return 102; }
    if (keep.ctrl != 0 || out.ctrl != 1) { return 103; }
    return __rc_underflow_count();
}`

const retSpreadTwoReturnsSrc = `struct St { ops: i32[], tag: string, ctrl: i32 }
function (s: St) step(v: i32): St {
    if (v % 2 == 0) { return St { ...s, ops: s.ops.append(v), ctrl: s.ctrl + 1 }; }
    return St { ...s, ctrl: s.ctrl - 1 };
}
function main(): i32 {
    let s: St = St { ops: [], tag: "zz", ctrl: 0 };
    let i: i32 = 0;
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
	for _, c := range retSpreadReuseCases {
		t.Run(c.name, func(t *testing.T) {
			if got := runWasm(t, c.src); got != 0 {
				t.Errorf("%s: got %d, want 0", c.name, got)
			}
		})
	}
}
