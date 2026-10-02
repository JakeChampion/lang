package e2eselfhost

import "testing"

// dynArrayLocalIRCases exercise method dispatch on the elements of a `dyn Trait[]`
// LOCAL through the self-host IR path on x86-64 + wasm.
//
// A `dyn Trait[]` PARAM already recorded the coarse `"dyn Trait"` element type on
// its slot (so `for x in param` / `param[i].m()` dispatched dynamically), but the
// local-`let` path marked the slot `is_arr` with NO element type — so every method
// call on an element of a dyn-array LOCAL (`xs[i].m()`, `let e = xs[i]; e.m()`,
// `for x in xs { x.m() }`) bailed to the AST path. The fix records the same coarse
// element type on the local slot, mirroring the param path.
//
// Each case is oracle-checked against the interpreter and returns a
// non-negative value <= 126 (cf. #2908).
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
	// Inline index then method call: C{5}.area() = 25.
	{"inline-index", `function main(): i32 { let xs: dyn Sh[] = [C { r: 5 }]; return xs[0].area(); }`},
	// Bind an element, then dispatch on the binding: 25.
	{"bind-element", `function main(): i32 { let xs: dyn Sh[] = [C { r: 5 }]; let e = xs[0]; return e.area(); }`},
	// Heterogeneous local iterated in a loop: 9 + 10 = 19.
	{"loop", `function main(): i32 { let xs: dyn Sh[] = [C { r: 3 }, R { w: 2, h: 5 }]; let t: i32 = 0; for x in xs { t = t + x.area(); } return t; }`},
	// Two inline indices added: 9 + 10 = 19.
	{"two-index", `function main(): i32 { let xs: dyn Sh[] = [C { r: 3 }, R { w: 2, h: 5 }]; return xs[0].area() + xs[1].area(); }`},
	// Three heterogeneous elements in a loop: 4 + 6 + 9 = 19.
	{"loop-three", `function main(): i32 { let xs: dyn Sh[] = [C { r: 2 }, R { w: 2, h: 3 }, C { r: 3 }]; let t: i32 = 0; for x in xs { t = t + x.area(); } return t; }`},
}

// TestSelfHostDynArrayLocalIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
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
