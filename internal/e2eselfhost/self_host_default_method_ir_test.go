package e2eselfhost

import "testing"

// defaultMethodIRCases exercise trait DEFAULT methods through the
// self-hosted compiler (parser.fern's parse_trait_decl retains a method's
// `{ … }` body and parse_module synthesises a copy onto each impl that
// omits it — Self-host parity with the Go checker's synthesizeTraitDefaults,
// see docs/TRAITS.md). Each `trait` declaration is real source the native
// compiler also honours; the self-host now inherits the default instead of
// discarding the trait.
var defaultMethodIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// Inherited default: Dog omits `score`, so it inherits `function
	// score(self) { return self.base() + 10; }`. 5 + 10 = 15.
	{"inherited",
		`trait Counter { function base(self: Self): i32; function score(self: Self): i32 { return self.base() + 10; } } struct Dog { v: i32 } impl Counter for Dog { function base(self: Self): i32 { return self.v; } } function main(): i32 { var d: Dog = Dog { v: 5 }; return d.score(); }`, 15},
	// Override: Cat provides its own `score`, which must win over the default.
	{"override",
		`trait Counter { function base(self: Self): i32; function score(self: Self): i32 { return self.base() + 10; } } struct Cat { v: i32 } impl Counter for Cat { function base(self: Self): i32 { return self.v; } function score(self: Self): i32 { return 99; } } function main(): i32 { var c: Cat = Cat { v: 5 }; return c.score(); }`, 99},
	// Default body calls another (abstract) method on `self`, plus a string
	// length — "rex".len() (3) + 4 = 7.
	{"calls-abstract",
		`trait Greet { function name(self: Self): string; function tag(self: Self): i32 { return self.name().len() + 4; } } struct Pet { age: i32 } impl Greet for Pet { function name(self: Self): string { return "rex"; } } function main(): i32 { var p: Pet = Pet { age: 1 }; return p.tag(); }`, 7},
	// Two impls of the same trait each inherit the default independently.
	// a.score() = 2 + 1 = 3; b.score() = 3*10 + 1 = 31; total 34.
	{"two-impls",
		`trait Counter { function base(self: Self): i32; function score(self: Self): i32 { return self.base() + 1; } } struct A { v: i32 } impl Counter for A { function base(self: Self): i32 { return self.v; } } struct B { v: i32 } impl Counter for B { function base(self: Self): i32 { return self.v * 10; } } function main(): i32 { var a: A = A { v: 2 }; var b: B = B { v: 3 }; return a.score() + b.score(); }`, 34},
}

// TestSelfHostDefaultMethodIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostDefaultMethodIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range defaultMethodIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src, target); code != tc.expected {
					t.Errorf("exited %d, want %d\n%s", code, tc.expected, stderr)
				}
			})
		}
	}
}
