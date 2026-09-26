package e2eselfhost

import "testing"

// inherentImplIRCases exercise INHERENT impl blocks — `impl Type { … }` with no
// `for Trait` (issue #2700) — through the self-host IR path. The parser desugars
// an inherent impl exactly like a trait impl: a receiver-less function becomes
// an associated function (`Type.f(args)`), a `self`-taking one becomes an
// ordinary method, and `Self` rewrites to the impl type — but with no trait to
// match, so constructors/static methods can live on a type without inventing a
// dummy trait. Exit codes are the oracle. Mirrors self_host_assoc_fn_ir_test.go
// (which covers the trait-impl form).
var inherentImplIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// Inherent associated function (constructor): bind, then sum fields. 3+4=7.
	{"struct-ctor",
		`struct Pt { x: i32, y: i32 } impl Pt { function make(a: i32, b: i32): Pt { return Pt { x: a, y: b }; } } function main(): i32 { var p: Pt = Pt.make(3, 4); return p.x + p.y; }`, 7},
	// Inherent impl mixing an associated fn (`Self` return) and a `self` method.
	// make(3,4) then p.sum() = 7.
	{"struct-assoc-and-method",
		`struct Pt { x: i32, y: i32 } impl Pt { function make(a: i32, b: i32): Self { return Pt { x: a, y: b }; } function sum(self: Self): i32 { return self.x + self.y; } } function main(): i32 { var p: Pt = Pt.make(3, 4); return p.sum(); }`, 7},
	// Zero-arg inherent constructor (empty-params path). 0 + 0 + 9 = 9.
	{"struct-zero-arg",
		`struct Pt { x: i32, y: i32 } impl Pt { function origin(): Pt { return Pt { x: 0, y: 0 }; } } function main(): i32 { var p: Pt = Pt.origin(); return p.x + p.y + 9; }`, 9},
	// Two inherent associated fns on one type. make(2,3)=5; scaled(5)={5,10}=15.
	// 5 + 15 = 20.
	{"struct-multi-assoc",
		`struct Pt { x: i32, y: i32 } impl Pt { function make(a: i32, b: i32): Pt { return Pt { x: a, y: b }; } function scaled(a: i32): Pt { return Pt { x: a, y: a + a }; } } function main(): i32 { var p: Pt = Pt.make(2, 3); var q: Pt = Pt.scaled(5); return p.x + p.y + q.x + q.y; }`, 20},
	// Inherent associated function on an ENUM returning the enum (nominal). 7.
	{"enum-ctor",
		`enum E { A(i32), B } impl E { function tag(n: i32): E { if (n > 0) { return A(n); } return B; } } function val(e: E): i32 { match (e) { A(n) => { return n; }, B => { return 99; } } return 0; } function main(): i32 { return val(E.tag(7)); }`, 7},
}

// TestSelfHostInherentImplIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostInherentImplIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range inherentImplIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src, target); code != tc.expected {
					t.Errorf("exited %d, want %d\n%s", code, tc.expected, stderr)
				}
			})
		}
	}
}
