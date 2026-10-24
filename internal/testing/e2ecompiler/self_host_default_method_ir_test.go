package e2ecompiler

import "testing"

var defaultMethodIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"inherited",
		`trait Counter { function base(self: Self): i32; function score(self: Self): i32 { return self.base() + 10; } } struct Dog { v: i32 } impl Counter for Dog { function base(self: Self): i32 { return self.v; } } function main(): i32 { let d: Dog = Dog { v: 5 }; return d.score(); }`, 15},
	{"override",
		`trait Counter { function base(self: Self): i32; function score(self: Self): i32 { return self.base() + 10; } } struct Cat { v: i32 } impl Counter for Cat { function base(self: Self): i32 { return self.v; } function score(self: Self): i32 { return 99; } } function main(): i32 { let c: Cat = Cat { v: 5 }; return c.score(); }`, 99},
	{"calls-abstract",
		`trait Greet { function name(self: Self): string; function tag(self: Self): i32 { return self.name().len() + 4; } } struct Pet { age: i32 } impl Greet for Pet { function name(self: Self): string { return "rex"; } } function main(): i32 { let p: Pet = Pet { age: 1 }; return p.tag(); }`, 7},
	{"two-impls",
		`trait Counter { function base(self: Self): i32; function score(self: Self): i32 { return self.base() + 1; } } struct A { v: i32 } impl Counter for A { function base(self: Self): i32 { return self.v; } } struct B { v: i32 } impl Counter for B { function base(self: Self): i32 { return self.v * 10; } } function main(): i32 { let a: A = A { v: 2 }; let b: B = B { v: 3 }; return a.score() + b.score(); }`, 34},
}

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
