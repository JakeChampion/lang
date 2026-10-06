package e2ecompiler

import "testing"

// dynArrayArgIRCases exercise a `dyn Trait[]` ARRAY LITERAL written directly at
// a call — `render([1, 22, 333])` where render's param is `dyn Sh[]` (#6906).
//
// This is the third coercion site. The two already wired detect a `dyn`
// destination from a scalar parameter's signature and from a
// `let xs: dyn Sh[] = […]` annotation; an ARRAY-typed parameter is neither, so
// the literal's elements were built into the buffer RAW. A struct/enum element
// carries its own shape and so survived that — which is why the existing
// dyn tests passed — but a primitive/string element does not, and the callee's
// op_dyn_dispatch then read a shape pointer out of a scalar: SIGSEGV on the
// register backends, a validation failure on wasm.
//
// The struct rows are the controls: they must keep working, and their presence
// is what shows the fix is about ELEMENT coercion rather than the call itself.
//
// Each case is oracle-checked against the interpreter.
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
	// The reported shape: i32 elements written as a literal at the call.
	// (1+1) + (22+1) + (333+1) = 359.
	{"arg-literal-i32", `function main(): i32 { return total([1, 22, 333]) - 300; }`},
	// String elements take the same box (the value word is the string's box
	// pointer): 2 + 3 = 5.
	{"arg-literal-string", `function main(): i32 { return total(["ab", "cde"]); }`},
	// CONTROL: struct elements carry their own shape, so they flowed in
	// correctly before the fix and must still.
	{"arg-literal-struct", `function main(): i32 { return total([C { r: 3 }, C { r: 4 }]); }`},
	// Mixed: one boxed primitive next to one shape-carrying struct.
	{"arg-literal-mixed", `function main(): i32 { return total([C { r: 5 }, 16]); }`},
	// The literal is not the first argument — the flag is read per position.
	{"arg-literal-second-param", `function twice(k: i32, xs: dyn Sh[]): i32 { return k + total(xs); }
function main(): i32 { return twice(10, [1, 2]); }`},
	// CONTROL: the same literal bound to a local first is the already-wired
	// site, and must be unaffected.
	{"arg-local-binding", `function main(): i32 { let ys: dyn Sh[] = [1, 22, 333]; return total(ys) - 300; }`},
	// Empty literal: no elements to coerce, and the buffer is still built.
	{"arg-literal-empty", `function main(): i32 { return total([]) + 7; }`},
}

// TestSelfHostDynArrayArgIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
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
