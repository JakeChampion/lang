package e2eselfhost

import "testing"

// deriveDefaultIRCases exercise `@derive(Default)` on concrete structs
// through the stack-IR path. The self-host parser synthesizes the derived
// `default()` as an ASSOCIATED function (receiver-less `Type.default()`),
// which lowers via the associated-function IR path (issue #2779 item 1).
// Each field gets its type's zero: i32 → 0, string → "", boolean → false.
//
// Scope (issue #2779 item 2): concrete leaf-safe structs (scalar / string /
// boolean fields). Nested-struct composition is RC-tracked → still bails,
// and enum Default is a follow-up (a safe miss, like enum Eq/Ord derive).
// The inline `trait Default` keeps the program valid for the native
// compiler too (the self-host discards trait decls).
var deriveDefaultIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// Scalar + string + boolean fields all default to zero. 0 + 0 + 5 = 5.
	{"basic-scalars",
		`trait Default { function default(): Self; } @derive(Default) struct Cfg { a: i32, s: string, b: boolean } function main(): i32 { var c: Cfg = Cfg.default(); return c.a + c.s.len() + 5; }`, 5},
	// Chained: read a field straight off Cfg.default(). 0 + 6 = 6.
	{"chained",
		`trait Default { function default(): Self; } @derive(Default) struct Cfg { a: i32, s: string } function main(): i32 { return Cfg.default().a + Cfg.default().s.len() + 6; }`, 6},
	// Inferred binding: `var c = Cfg.default()` (no annotation) recovers the
	// struct type from the associated-call return type. 0 + 7 = 7.
	{"inferred-binding",
		`trait Default { function default(): Self; } @derive(Default) struct Cfg { a: i32, b: i32 } function main(): i32 { var c = Cfg.default(); return c.a + c.b + 7; }`, 7},
	// Boolean field defaults to false. 0 + 8 = 8.
	{"boolean-default",
		`trait Default { function default(): Self; } @derive(Default) struct F { flag: boolean, x: i32 } function main(): i32 { var f: F = F.default(); if (f.flag) { return 1; } return f.x + 8; }`, 8},
	// Several i32 fields, all zero. 0 + 0 + 0 + 10 = 10.
	{"multi-i32",
		`trait Default { function default(): Self; } @derive(Default) struct M { a: i32, b: i32, c: i32 } function main(): i32 { var m: M = M.default(); return m.a + m.b + m.c + 10; }`, 10},
}

// TestSelfHostDeriveDefaultIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostDeriveDefaultIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range deriveDefaultIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src, target); code != tc.expected {
					t.Errorf("exited %d, want %d\n%s", code, tc.expected, stderr)
				}
			})
		}
	}
}
