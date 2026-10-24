package e2ecompiler

import (
	"testing"
)

var enumCrossReuseIRCases = []struct {
	name string
	src  string
	want int
}{
	{"fires-value", `enum E { A(i32[]), B(i32[]) }
function f(): i32 { let a: E = A([1, 2]); let t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_) => { t = 6; } } let c: E = B([3, 4]); let v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w) => { v = w[0] + w[1]; } } return t + v; }
function main(): i32 { return f(); }`, 12},
	{"fires-detector", `enum E { A(i32[]), B(i32[]) }
function f(): i32 { let a: E = A([1, 2]); let t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_) => { t = 6; } } let c: E = B([3, 4]); let v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w) => { v = w[0] + w[1]; } } if (t + v != 12) { return 99; } return __rc_underflow_count(); }
function main(): i32 { return f(); }`, 0},
	{"corruption-probe-detector", `enum E { A(i32[]), B(i32[]) }
function f(): i32 { let a: E = A([1, 2]); let t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_) => { t = 6; } } let c: E = B([3, 4]); let fresh: i32[] = [11, 22, 33]; let fs: i32 = fresh[0] + fresh[1] + fresh[2]; let v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w) => { v = w[0] + w[1]; } } if (t + v != 12) { return 90; } if (fs != 66) { return 91; } return __rc_underflow_count(); }
function main(): i32 { return f(); }`, 0},
	{"donor-live-no-reuse-detector", `enum E { A(i32[]), B(i32[]) }
function f(): i32 { let a: E = A([1, 2]); let c: E = B([3, 4]); let t: i32 = 0; match (a) { A(_) => { t = 3; }, B(_) => { t = 4; } } let v: i32 = 0; match (c) { A(_) => { v = 7; }, B(_) => { v = 8; } } if (t + v != 11) { return 99; } return __rc_underflow_count(); }
function main(): i32 { return f(); }`, 0},
	{"ragged-no-reuse-detector", `enum E { A(i32[]), B(i32[], i32[]) }
function f(): i32 { let a: E = A([1, 2]); let t: i32 = 0; match (a) { A(_) => { t = 5; }, B(_, _) => { t = 6; } } let c: E = B([3, 4], [5, 6]); let v: i32 = 0; match (c) { A(w) => { v = w[0]; }, B(w, x) => { v = w[0] + x[1]; } } if (t + v != 14) { return 99; } return __rc_underflow_count(); }
function main(): i32 { return f(); }`, 0},
}

func TestSelfHostEnumCrossReuseIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range enumCrossReuseIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			for _, target := range []string{"x86-64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
