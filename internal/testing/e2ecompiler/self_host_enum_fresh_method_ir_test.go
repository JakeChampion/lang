package e2ecompiler

import "testing"

var enumFreshMethodIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"payload-receiver",
		`enum E { Has(i32), Nil } function (self: E) tagval(): i32 { match (self) { Has(n) => { return n + 1; }, Nil => { return 99; } } } function main(): i32 { return Has(5).tagval(); }`, 6},
	{"unit-receiver",
		`enum E { Has(i32), Nil } function (self: E) tagval(): i32 { match (self) { Has(n) => { return n + 1; }, Nil => { return 99; } } } function main(): i32 { return Nil.tagval(); }`, 99},
	{"payload-and-unit",
		`enum E { Has(i32), Nil } function (self: E) tagval(): i32 { match (self) { Has(n) => { return n + 1; }, Nil => { return 99; } } } function main(): i32 { return Has(5).tagval() + Nil.tagval(); }`, 105},
	{"method-with-arg",
		`enum E { Has(i32), Nil } function (self: E) addto(k: i32): i32 { match (self) { Has(n) => { return n + k; }, Nil => { return k; } } } function main(): i32 { return Has(5).addto(10); }`, 15},
	{"derived-eq",
		`trait Eq { function eq(self: Self, other: Self): boolean; } impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } } @derive(Eq) enum Opt { Has(i32), Nil } function main(): i32 { let r: i32 = 0; if (Has(5).eq(Has(5))) { r = r + 1; } if (!Has(5).eq(Has(6))) { r = r + 2; } if (!Has(5).eq(Nil)) { r = r + 4; } if (Nil.eq(Nil)) { r = r + 8; } return r; }`, 15},
}

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
