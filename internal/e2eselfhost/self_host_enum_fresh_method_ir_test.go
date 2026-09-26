package e2eselfhost

import "testing"

// enumFreshMethodIRCases exercise an enum method called DIRECTLY on a freshly
// constructed variant — a payload variant `Has(5).m()` or a bare unit variant
// `Nil.m()` — through the stack-IR path. A fresh variant isn't a typed local,
// so recovering the receiver's enum for `<Enum>.<method>` dispatch takes extra
// work; without it such calls bail (and a unit variant was
// mis-read as an associated-function TYPE target). The parser now records each
// variant's owning enum on its desugared StructDecl (`enum_owner`), and
// irlower's `expr_enum_type` recovers it. Exit codes are the oracle.
var enumFreshMethodIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// Payload variant receiver. Has(5) → 5 + 1 = 6.
	{"payload-receiver",
		`enum E { Has(i32), Nil } function (self: E) tagval(): i32 { match (self) { Has(n) => { return n + 1; }, Nil => { return 99; } } } function main(): i32 { return Has(5).tagval(); }`, 6},
	// Unit variant receiver (a bare ident, not a type). Nil → 99.
	{"unit-receiver",
		`enum E { Has(i32), Nil } function (self: E) tagval(): i32 { match (self) { Has(n) => { return n + 1; }, Nil => { return 99; } } } function main(): i32 { return Nil.tagval(); }`, 99},
	// Both forms in one expression. 6 + 99 = 105.
	{"payload-and-unit",
		`enum E { Has(i32), Nil } function (self: E) tagval(): i32 { match (self) { Has(n) => { return n + 1; }, Nil => { return 99; } } } function main(): i32 { return Has(5).tagval() + Nil.tagval(); }`, 105},
	// A method taking an argument, dispatched on a fresh variant. 5 + 10 = 15.
	{"method-with-arg",
		`enum E { Has(i32), Nil } function (self: E) addto(k: i32): i32 { match (self) { Has(n) => { return n + k; }, Nil => { return k; } } } function main(): i32 { return Has(5).addto(10); }`, 15},
	// Derived Eq on an enum, all comparisons on fresh variants (the
	// trait-derive-enum-eq shape). 1 + 2 + 4 + 8 = 15.
	{"derived-eq",
		`trait Eq { function eq(self: Self, other: Self): boolean; } impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } } @derive(Eq) enum Opt { Has(i32), Nil } function main(): i32 { var r: i32 = 0; if (Has(5).eq(Has(5))) { r = r + 1; } if (!Has(5).eq(Has(6))) { r = r + 2; } if (!Has(5).eq(Nil)) { r = r + 4; } if (Nil.eq(Nil)) { r = r + 8; } return r; }`, 15},
}

// TestSelfHostEnumFreshMethodIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostEnumFreshMethodIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range enumFreshMethodIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src, target); code != tc.expected {
					t.Errorf("exited %d, want %d\n%s", code, tc.expected, stderr)
				}
			})
		}
	}
}
