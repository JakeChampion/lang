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
