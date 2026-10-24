package e2ecompiler

import "testing"

var deriveDefaultIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"basic-scalars",
		`trait Default { function default(): Self; } @derive(Default) struct Cfg { a: i32, s: string, b: boolean } function main(): i32 { let c: Cfg = Cfg.default(); return c.a + c.s.len() + 5; }`, 5},
	{"chained",
		`trait Default { function default(): Self; } @derive(Default) struct Cfg { a: i32, s: string } function main(): i32 { return Cfg.default().a + Cfg.default().s.len() + 6; }`, 6},
	{"inferred-binding",
		`trait Default { function default(): Self; } @derive(Default) struct Cfg { a: i32, b: i32 } function main(): i32 { let c = Cfg.default(); return c.a + c.b + 7; }`, 7},
	{"boolean-default",
		`trait Default { function default(): Self; } @derive(Default) struct F { flag: boolean, x: i32 } function main(): i32 { let f: F = F.default(); if (f.flag) { return 1; } return f.x + 8; }`, 8},
	{"multi-i32",
		`trait Default { function default(): Self; } @derive(Default) struct M { a: i32, b: i32, c: i32 } function main(): i32 { let m: M = M.default(); return m.a + m.b + m.c + 10; }`, 10},
}

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
