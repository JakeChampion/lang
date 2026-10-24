package e2ecompiler

import "testing"

const dynArrayArgPrelude = `trait Sh { function area(self: Self): i32; }
struct C { r: i32 }
impl Sh for C { function area(self: Self): i32 { return self.r * self.r; } }
impl Sh for i32 { function area(self: Self): i32 { return self + 1; } }
impl Sh for string { function area(self: Self): i32 { return self.len(); } }
function total(xs: dyn Sh[]): i32 { let t: i32 = 0; for x in xs { t = t + x.area(); } return t; }
`

var dynArrayArgIRCases = []struct {
	name string
	main string
}{
	{"arg-literal-i32", `function main(): i32 { return total([1, 22, 333]) - 300; }`},
	{"arg-literal-string", `function main(): i32 { return total(["ab", "cde"]); }`},
	{"arg-literal-struct", `function main(): i32 { return total([C { r: 3 }, C { r: 4 }]); }`},
	{"arg-literal-mixed", `function main(): i32 { return total([C { r: 5 }, 16]); }`},
	{"arg-literal-second-param", `function twice(k: i32, xs: dyn Sh[]): i32 { return k + total(xs); }
function main(): i32 { return twice(10, [1, 2]); }`},
	{"arg-local-binding", `function main(): i32 { let ys: dyn Sh[] = [1, 22, 333]; return total(ys) - 300; }`},
	{"arg-literal-empty", `function main(): i32 { return total([]) + 7; }`},
}

func TestSelfHostDynArrayArgIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range dynArrayArgIRCases {
		src := dynArrayArgPrelude + tc.main + "\n"
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
