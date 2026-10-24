package e2ecompiler

import (
	"testing"
)

var f32IRCases = []struct {
	name string
	main string
}{
	{"id", `function id32(x: f32): f32 { return x; }
function main(): i32 { let a: f32 = 5.5 as f32; return id32(a) as i32; }`},
	{"add", `function add32(a: f32, b: f32): f32 { return a + b; }
function main(): i32 { return add32(2.5 as f32, 3.0 as f32) as i32; }`},
	{"mul", `function mul32(a: f32, b: f32): f32 { return a * b; }
function main(): i32 { return mul32(2.0 as f32, 3.5 as f32) as i32; }`},
	{"abs", `function fabs32(x: f32): f32 { return __abs_f64(x as f64) as f32; }
function main(): i32 { let a: f32 = 0.0 - 5.5 as f32; return fabs32(a) as i32; }`},
	{"sqrt", `function fsqrt32(x: f32): f32 { return __sqrt_f64(x as f64) as f32; }
function main(): i32 { return fsqrt32(16.0 as f32) as i32; }`},
	{"floor", `function ffloor32(x: f32): f32 { return __floor_f64(x as f64) as f32; }
function main(): i32 { return ffloor32(7.8 as f32) as i32; }`},
	{"round", `function fround32(x: f32): f32 { return __round_f64(x as f64) as f32; }
function main(): i32 { return fround32(2.5 as f32) as i32; }`},
	{"via-local", `function main(): i32 { let y: f32 = 9.99 as f32; let f: f32 = __floor_f64(y as f64) as f32; return f as i32; }`},
	{"array", `function main(): i32 { let a: f32[] = [1.0 as f32, 2.0 as f32, 3.0 as f32]; return a[1] as i32; }`},
	{"array-sum", `function main(): i32 {
    let a: f32[] = [1.5 as f32, 2.5 as f32, 3.0 as f32];
    let s: f32 = 0.0 as f32; let i: i32 = 0;
    while (i < a.len()) { s = s + a[i]; i = i + 1; }
    return s as i32;
}`},
	{"struct-field", `struct P { x: f32, y: f32 }
function main(): i32 { let p: P = P { x: 3.0 as f32, y: 4.0 as f32 }; return (p.x + p.y) as i32; }`},
	{"tuple", `function main(): i32 { let t: (f32, i32) = (3.5 as f32, 2); return (t.0 as i32) + t.1; }`},
	{"struct-field-call", `struct P { v: f32 }
function dbl(p: P): f32 { return p.v + p.v; }
function main(): i32 { let p: P = P { v: 5.5 as f32 }; return dbl(p) as i32; }`},
	{"u64-struct-field", `struct B { v: u64 }
function main(): i32 { let b: B = B { v: 4294967297 as u64 }; if (b.v == 4294967297 as u64) { return 7; } return 0; }`},
}

func TestSelfHostF32IR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range f32IRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.main + "\n"
			want := interpExit(t, interpBin, src)
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("%s on %s exited %d, want %d (interp oracle)\n%s", tc.name, target, code, want, stderr)
				}
			}
		})
	}
}
