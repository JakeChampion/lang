package e2ecompiler

import "testing"

var deriveDefaultEnumIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"unit-first",
		`trait Default { function default(): Self; } @derive(Default) enum E { A, B(i32) } function main(): i32 { let e: E = E.default(); match (e) { A => { return 3; }, B(n) => { return n; } } }`, 3},
	{"payload-i32",
		`trait Default { function default(): Self; } @derive(Default) enum E { Wrap(i32), Other } function main(): i32 { let e: E = E.default(); match (e) { Wrap(n) => { return n + 4; }, Other => { return 1; } } }`, 4},
	{"payload-string",
		`trait Default { function default(): Self; } @derive(Default) enum E { Msg(string), None } function main(): i32 { let e: E = E.default(); match (e) { Msg(s) => { return s.len() + 9; }, None => { return 1; } } }`, 9},
	{"payload-boolean",
		`trait Default { function default(): Self; } @derive(Default) enum E { Flag(boolean), Off } function main(): i32 { let e: E = E.default(); match (e) { Flag(b) => { if (b) { return 1; } return 6; }, Off => { return 2; } } }`, 6},
	{"inferred-binding",
		`trait Default { function default(): Self; } @derive(Default) enum E { A, B(i32) } function main(): i32 { let e = E.default(); match (e) { A => { return 7; }, B(n) => { return n; } } }`, 7},
}

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
