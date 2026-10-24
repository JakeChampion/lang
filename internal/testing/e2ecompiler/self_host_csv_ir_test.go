package e2ecompiler

import "testing"

// TestSelfHostCsvParseLineIR tests the public parser and its errors on both targets.
func TestSelfHostCsvParseLineIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	cases := []struct{ name, body string }{
		{"quoted-comma", `let Ok(f) = csv.parse_line("a,bb,\"c,d\",e") else { return 100; }; if (f.len() != 4 || f[2] != "c,d") { return 101; } return 42;`},
		{"doubled-quote", `let Ok(f) = csv.parse_line("\"a\"\"b\",x") else { return 100; }; if (f.len() != 2 || f[0] != "a\"b") { return 101; } return 42;`},
		{"plain", `let Ok(f) = csv.parse_line("x,y,z") else { return 100; }; if (f.len() != 3) { return 101; } return 42;`},
		{"unclosed", `match (csv.parse_line("\"x")) { Err(e) => { if (e.offset == 2 && e.kind == csv.ParseErrorKind.UnclosedQuote) { return 42; } }, Ok(_) => {} } return 100;`},
		{"quote-in-field", `match (csv.parse("a\"b")) { Err(e) => { if (e.offset == 1 && e.kind == csv.ParseErrorKind.UnexpectedQuote) { return 42; } }, Ok(_) => {} } return 100;`},
		{"byte-after-quote", `match (csv.parse("\"a\"x")) { Err(e) => { if (e.offset == 3 && e.kind == csv.ParseErrorKind.UnexpectedByteAfterQuote(120 as u8)) { return 42; } }, Ok(_) => {} } return 100;`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := `import "std/csv"; function main(): i32 { ` + tc.body + ` }`
			if got := interpExit(t, interpBin, src); got != 42 {
				t.Fatalf("interpreter exit %d, want 42", got)
			}
			for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != 42 {
					t.Errorf("%s exit %d, want 42\n%s", target, code, stderr)
				}
			}
		})
	}
}
