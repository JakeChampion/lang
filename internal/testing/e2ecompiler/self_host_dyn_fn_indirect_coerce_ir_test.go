package e2ecompiler

import (
	"testing"
)

var dynFnIndirectCoerceCases = []struct {
	name string
	main string
}{
	{"prim-literal-at-indirect-call", `trait Speak { function say(self: Self): i32; }
impl Speak for i32 { function say(self: Self): i32 { return self + 100; } }
function apply(f: (dyn Speak) => i32): i32 { return f(7); }
function main(): i32 { return apply((s: dyn Speak) => s.say()); }`},
	{"two-impl-indirect", `trait Speak { function say(self: Self): i32; }
impl Speak for i32 { function say(self: Self): i32 { return self + 100; } }
struct Dog {}
impl Speak for Dog { function say(self: Self): i32 { return 5; } }
function apply(f: (dyn Speak) => i32): i32 { return f(7); }
function main(): i32 { return apply((s: dyn Speak) => s.say()); }`},
	{"dyn-param-index-1", `trait Speak { function say(self: Self): i32; }
impl Speak for i32 { function say(self: Self): i32 { return self + 100; } }
function apply(f: (i32, dyn Speak) => i32): i32 { return f(3, 7); }
function main(): i32 { return apply((n: i32, s: dyn Speak) => n + s.say()); }`},
	{"nondyn-fn-param-unboxed", `function apply(f: (i32) => i32): i32 { return f(7); }
function main(): i32 { return apply((x: i32) => x + 100); }`},
	{"struct-dyn-at-indirect-call", `trait Speak { function say(self: Self): i32; }
struct Cat { v: i32 }
impl Speak for Cat { function say(self: Self): i32 { return self.v + 100; } }
function apply(f: (dyn Speak) => i32): i32 { let c: Cat = Cat { v: 7 }; return f(c); }
function main(): i32 { return apply((s: dyn Speak) => s.say()); }`},
}

func TestSelfHostDynFnIndirectCoerceIRX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)

	for _, tc := range dynFnIndirectCoerceCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			if stderr, code := cli.exitOf(t, string(src), "x86-64-linux"); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)\n%s", tc.name, code, want, stderr)
			}
		})
	}
}
