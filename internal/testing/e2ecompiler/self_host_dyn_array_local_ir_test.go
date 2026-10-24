package e2ecompiler

import "testing"

const dynArrayLocalPrelude = `trait Sh { function area(self: Self): i32; }
struct C { r: i32 }
struct R { w: i32, h: i32 }
impl Sh for C { function area(self: Self): i32 { return self.r * self.r; } }
impl Sh for R { function area(self: Self): i32 { return self.w * self.h; } }
`

var dynArrayLocalIRCases = []struct {
	name string
	main string
}{
	{"inline-index", `function main(): i32 { let xs: dyn Sh[] = [C { r: 5 }]; return xs[0].area(); }`},
	{"bind-element", `function main(): i32 { let xs: dyn Sh[] = [C { r: 5 }]; let e = xs[0]; return e.area(); }`},
	{"loop", `function main(): i32 { let xs: dyn Sh[] = [C { r: 3 }, R { w: 2, h: 5 }]; let t: i32 = 0; for x in xs { t = t + x.area(); } return t; }`},
	{"two-index", `function main(): i32 { let xs: dyn Sh[] = [C { r: 3 }, R { w: 2, h: 5 }]; return xs[0].area() + xs[1].area(); }`},
	{"loop-three", `function main(): i32 { let xs: dyn Sh[] = [C { r: 2 }, R { w: 2, h: 3 }, C { r: 3 }]; let t: i32 = 0; for x in xs { t = t + x.area(); } return t; }`},
}

func TestSelfHostDynArrayLocalIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range dynArrayLocalIRCases {
		src := dynArrayLocalPrelude + tc.main + "\n"
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
