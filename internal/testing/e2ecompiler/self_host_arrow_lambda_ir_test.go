package e2ecompiler

import "testing"

var arrowLambdaIRCases = []struct {
	name string
	main string
}{
	{"noncap", `function main(): i32 { let f = (x: i32): i32 => x + 1; return f(5); }`},
	{"capture", `function main(): i32 { let n = 10; let f = (x: i32): i32 => x + n; return f(5); }`},
	{"capture-noargs", `function main(): i32 { let n = 7; let f = (): i32 => n * 2; return f(); }`},
	{"two-params-cap", `function main(): i32 { let k = 3; let f = (a: i32, b: i32): i32 => a + b + k; return f(4, 5); }`},
	{"capture-twice", `function main(): i32 { let n = 6; let f = (x: i32): i32 => x * n + n; return f(4); }`},
	{"fn-form-regress", `function main(): i32 { let n = 10; let f = (x: i32): i32 => { return x + n; }; return f(5); }`},
	{"own-first-param", `function main(): i32 { let f = (own a: string[]): string[] => a; let xs: string[] = f(["x", "y"]); return xs.len(); }`},
	{"void-call-body", `function bump(c: Cell[i32], by: i32): void { c.set(c.get() + by); }
function twice(c: Cell[i32]): void { return bump(c, 2); }
function main(): i32 { let c: Cell[i32] = cell_new(1); let f = (n: i32): void => bump(c, n); f(4); twice(c); return c.get(); }`},
}

func TestSelfHostArrowLambdaIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range arrowLambdaIRCases {
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
