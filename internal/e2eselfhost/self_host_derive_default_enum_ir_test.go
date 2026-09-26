package e2eselfhost

import "testing"

// deriveDefaultEnumIRCases exercise `@derive(Default)` on concrete ENUMS
// through the stack-IR path. An enum defaults to its FIRST variant, each
// payload defaulted. The synthesized `default()` is an associated function
// (receiver-less `Enum.default()`) that constructs a variant; both the
// associated call and the variant construction now lower through the IR path
// (the enum-in-IR slice: enum returns are registered in struct_ret_fns, and
// the assoc-fn lowering recognises an enum target by its registered return
// type). `match` reads the variant via shape-pointer identity — the same
// representation IR `struct_make` writes — so a freshly-defaulted variant
// matches correctly.
//
// The inline `trait Default` keeps the program valid for the native compiler
// too (the self-host discards trait decls).
var deriveDefaultEnumIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// First variant is a UNIT variant → bare variant value. 3.
	{"unit-first",
		`trait Default { function default(): Self; } @derive(Default) enum E { A, B(i32) } function main(): i32 { var e: E = E.default(); match (e) { A => { return 3; }, B(n) => { return n; } } }`, 3},
	// First variant has an i32 payload → defaulted to 0. 0 + 4 = 4.
	{"payload-i32",
		`trait Default { function default(): Self; } @derive(Default) enum E { Wrap(i32), Other } function main(): i32 { var e: E = E.default(); match (e) { Wrap(n) => { return n + 4; }, Other => { return 1; } } }`, 4},
	// First variant has a string payload → defaulted to "". 0 + 9 = 9.
	{"payload-string",
		`trait Default { function default(): Self; } @derive(Default) enum E { Msg(string), None } function main(): i32 { var e: E = E.default(); match (e) { Msg(s) => { return s.len() + 9; }, None => { return 1; } } }`, 9},
	// First variant has a boolean payload → defaulted to false. 6.
	{"payload-boolean",
		`trait Default { function default(): Self; } @derive(Default) enum E { Flag(boolean), Off } function main(): i32 { var e: E = E.default(); match (e) { Flag(b) => { if (b) { return 1; } return 6; }, Off => { return 2; } } }`, 6},
	// Inferred binding: `var e = E.default()` recovers the enum type from the
	// registered associated-call return type. First variant A → 7.
	{"inferred-binding",
		`trait Default { function default(): Self; } @derive(Default) enum E { A, B(i32) } function main(): i32 { var e = E.default(); match (e) { A => { return 7; }, B(n) => { return n; } } }`, 7},
}

// TestSelfHostDeriveDefaultEnumIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostDeriveDefaultEnumIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range deriveDefaultEnumIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src, target); code != tc.expected {
					t.Errorf("exited %d, want %d\n%s", code, tc.expected, stderr)
				}
			})
		}
	}
}
