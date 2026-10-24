package e2ecompiler

import "testing"

var f64RecvIRCases = []struct {
	name string
	main string
}{
	{"arith", `function (x: f64) dbl(): f64 { return x + x; }
function main(): i32 { let a: f64 = 3.5; return a.dbl() as i32; }`},
	{"id", `function (x: f64) id(): f64 { return x; }
function main(): i32 { let a: f64 = 5.5; return a.id() as i32; }`},
	{"div", `function (x: f64) half(): f64 { return x / 2.0; }
function main(): i32 { let a: f64 = 9.0; return a.half() as i32; }`},
	{"chain", `function (x: f64) inc(): f64 { return x + 1.0; }
function main(): i32 { let a: f64 = 4.0; let r: f64 = a.inc() + 2.0; return r as i32; }`},
	{"intrinsic", `function (x: f64) sq(): f64 { return __sqrt_f64(x); }
function main(): i32 { let a: f64 = 16.0; return a.sq() as i32; }`},
}

func TestSelfHostF64RecvIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range f64RecvIRCases {
		src := tc.main + "\n"
		want := interpExit(t, interpBin, src)
		for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("exited %d, want %d (interp oracle)\n%s", code, want, stderr)
				}
			})
		}
	}
}
