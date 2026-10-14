package e2ecompiler

import "testing"

// An integer value block arm joined with an unsuffixed float sibling settles at
// that float as a bare literal arm does (#12381): `if (c) { ({ 1 }) } else {
// 2.5 }` is an f64, so the block's lambda is too. Each program's comparison
// holds only when the arm really became a float.
var valueBlockFloatArmCases = []struct {
	name string
	src  string
	want int
}{
	{"block-arm", `function main(): i32 { let c: boolean = true; let x = if (c) { ({ 1 }) } else { 2.5 }; if (x / 2.0 == 0.5) { return 3; } return 4; }`, 3},
	{"block-arm-leading-statement", `function main(): i32 { let x = if (true) { ({ let q: i32 = 3; 1 }) } else { 2.5 }; if (x / 2.0 == 0.5) { return 5; } return 6; }`, 5},
	{"nested-block-arm", `function main(): i32 { let x = if (true) { ({ ({ 1 }) }) } else { 2.5 }; if (x / 2.0 == 0.5) { return 7; } return 8; }`, 7},
	{"block-arm-after-float", `function main(): i32 { let x = if (false) { 2.5 } else { ({ 1 }) }; if (x / 2.0 == 0.5) { return 9; } return 10; }`, 9},
	{"match-block-arm", `function main(): i32 { let k: i32 = 1; let x = match (k) { 1 => ({ 1 }), _ => 2.5 }; if (x / 2.0 == 0.5) { return 11; } return 12; }`, 11},
	{"block-arm-f32", `function main(): i32 { let x = if (true) { ({ 1 }) } else { 2.5f32 }; if (x / 2.0f32 == 0.5f32) { return 13; } return 14; }`, 13},
	{"block-arm-return", `function g(c: boolean): f64 { return if (c) { ({ 1 }) } else { 2.5 }; } function main(): i32 { if (g(true) / 2.0 == 0.5) { return 15; } return 16; }`, 15},
	{"block-return", `function g(): f64 { return ({ 1 }); } function main(): i32 { if (g() / 2.0 == 0.5) { return 17; } return 18; }`, 17},
	{"block-arm-argument", `function g(v: f64): f64 { return v / 2.0; } function main(): i32 { if (g(if (true) { ({ 1 }) } else { 2.5 }) == 0.5) { return 19; } return 20; }`, 19},
	// The same settle reaches tuple elements and enum payloads.
	{"tuple-elem", `function main(): i32 { let c: boolean = true; let t = if (c) { (1, 2) } else { (3.5, 4) }; if (t.0 / 2.0 == 0.5 && t.1 == 2) { return 21; } return 22; }`, 21},
	{"tuple-elem-after-float", `function main(): i32 { let c: boolean = false; let t = if (c) { (3.5, 4) } else { (1, 2) }; if (t.0 / 2.0 == 0.5) { return 23; } return 24; }`, 23},
	{"tuple-elem-typed-sibling", `function main(): i32 { let c: boolean = true; let n: i32 = 4; let t = if (c) { (1, n) } else { (3.5, 4) }; if (t.0 / 2.0 == 0.5 && t.1 == 4) { return 25; } return 26; }`, 25},
	{"nested-tuple-elem", `function main(): i32 { let c: boolean = true; let t = if (c) { ((1, 2), 3) } else { ((3.5, 4), 5) }; if (t.0.0 / 2.0 == 0.5) { return 27; } return 28; }`, 27},
	{"match-tuple-elem", `function main(): i32 { let k: i32 = 1; let t = match (k) { 1 => (1, 2), _ => (3.5, 4) }; if (t.0 / 2.0 == 0.5) { return 29; } return 30; }`, 29},
	{"block-tuple-arm", `function main(): i32 { let c: boolean = true; let t = if (c) { ({ (1, 2) }) } else { (3.5, 4) }; if (t.0 / 2.0 == 0.5) { return 31; } return 32; }`, 31},
	{"nested-if-tuple-elem", `function main(): i32 { let c: boolean = true; let t = if (c) { (if (true) { 1 } else { 2 }, 3) } else { (3.5, 4) }; if (t.0 / 2.0 == 0.5) { return 33; } return 34; }`, 33},
	{"tuple-elem-f32", `function main(): i32 { let c: boolean = true; let t = if (c) { (1, 2) } else { (3.5f32, 4) }; if (t.0 / 2.0f32 == 0.5f32) { return 35; } return 36; }`, 35},
	{"some-payload", `function main(): i32 { let c: boolean = true; let o = if (c) { Some(1) } else { Some(2.5) }; match (o) { Some(v) => { if (v / 2.0 == 0.5) { return 37; } }, None => {} } return 38; }`, 37},
	{"some-payload-after-float", `function main(): i32 { let c: boolean = false; let o = if (c) { Some(2.5) } else { Some(1) }; match (o) { Some(v) => { if (v / 2.0 == 0.5) { return 39; } }, None => {} } return 40; }`, 39},
	{"some-tuple-payload", `function main(): i32 { let c: boolean = true; let o = if (c) { Some((1, 2)) } else { Some((3.5, 4)) }; match (o) { Some(v) => { if (v.0 / 2.0 == 0.5) { return 41; } }, None => {} } return 42; }`, 41},
	{"ok-payload", `function main(): i32 { let c: boolean = true; let r = if (c) { Ok(1) } else { Ok(2.5) }; match (r) { Ok(v) => { if (v / 2.0 == 0.5) { return 43; } }, Err(e) => {} } return 44; }`, 43},
	{"err-payload", `function main(): i32 { let c: boolean = true; let r = if (c) { Err(1) } else { Err(2.5) }; match (r) { Ok(v) => {}, Err(e) => { if (e / 2.0 == 0.5) { return 45; } } } return 46; }`, 45},
	{"match-some-payload", `function main(): i32 { let k: i32 = 2; let o = match (k) { 1 => Some(2.5), 2 => Some(1), _ => None }; match (o) { Some(v) => { if (v / 2.0 == 0.5) { return 47; } }, None => {} } return 48; }`, 47},
	{"generic-enum-payload", `enum Bx[T] { Full(T), Empty } function main(): i32 { let c: boolean = true; let b = if (c) { Full(1) } else { Full(2.5) }; match (b) { Full(v) => { if (v / 2.0 == 0.5) { return 49; } }, Empty => {} } return 50; }`, 49},
}

// TestSelfHostValueBlockFloatArmX86_64 runs each case as a self-host x86-64
// binary built under FERN_STRICT_IR.
func TestSelfHostValueBlockFloatArmX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range valueBlockFloatArmCases {
		t.Run(tc.name, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, tc.src, "x86-64-linux", "FERN_STRICT_IR=1"); code != tc.want {
				t.Errorf("x86-64 binary exited %d, want %d\n%s", code, tc.want, stderr)
			}
		})
	}
}
