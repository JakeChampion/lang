package e2eselfhost

import "testing"

// fstringIRCases pin f-string interpolation. An f-string `f"...{e}..."`
// desugars (in parser.fern) to the literal parts as string literals and each
// interpolant as `(e).to_string()`, folded left to right with `+`. The
// interpolants are i32 and string values, bare locals and COMPUTED ones
// (`${x.len()}`, `${a + b}`) alike: the interpolant is re-parsed into a full
// expression. Each case builds an f-string and returns a small deterministic
// int (a `.len()`, a byte value, or an equality flag; all <= 126);
// expectations were verified against the native interpreter. FEATURE-AUDIT
// f-strings / interpolation row.
var fstringIRCases = []struct {
	name string
	main string
	want int
}{
	// single i32 interpolation + a literal prefix: "v=42" (len 4), also checked
	// for string equality against the literal "v=42".
	{"i32-eq-len", `var n: i32 = 42; var s: string = f"v={n}"; if (s == "v=42") { return s.len(); } return 99;`, 4},
	// literal + single interpolation: "x=7" (len 3).
	{"literal-interp", `var x: i32 = 7; var s: string = f"x={x}"; return s.len();`, 3},
	// two interpolants with a literal between: "1-2" (len 3).
	{"multi-interp", `var a: i32 = 1; var b: i32 = 2; var s: string = f"{a}-{b}"; return s.len();`, 3},
	// string-valued interpolant (identity to_string) inside literals: "[hi]" (len 4).
	{"string-interp", `var name: string = "hi"; var s: string = f"[{name}]"; return s.len();`, 4},
	// byte content: "n5"[1] == '5' == 53 — proves the interpolant text lands at
	// the right offset, not just that the length is right.
	{"byte-index", `var n: i32 = 5; var s: string = f"n{n}"; return s[1] as i32;`, 53},
	// multi-digit interpolation between literals: "=100=" (len 5).
	{"multi-digit", `var n: i32 = 100; var s: string = f"={n}="; return s.len();`, 5},
	// empty f-string desugars to "" (len 0).
	{"empty", `var s: string = f""; return s.len();`, 0},
	// COMPUTED interpolants (not a bare local) — the interpolant is re-parsed by
	// parser.parse_expr_from_text into a full expression, so `${e}` desugars to
	// `(e).to_string()` for any i32-valued `e`. These pin that a method-call result
	// and an arithmetic expression interpolate through the same i32 `to_string`
	// fast-path the bare-local cases use (`expr_recv_prim_type` classifies the call
	// / arith result as i32). The most common real f-string shape — `${x.len()}`.
	// i32 method-call result: "L4" (len 2).
	{"method-len", `var s: string = "abcd"; var t: string = f"L{s.len()}"; return t.len();`, 2},
	// i32 method-call result, byte-checked: "3"[0] == '3' == 51 — proves the
	// interpolated digit text is correct, not just the length.
	{"method-byte", `var s: string = "abc"; var t: string = f"{s.len()}"; return t[0] as i32;`, 51},
	// arithmetic interpolant: "=25=" (len 4).
	{"arith", `var n: i32 = 20; var s: string = f"={n + 5}="; return s.len();`, 4},
	// nested arithmetic, byte-checked: "9"[0] == '9' == 57.
	{"arith-byte", `var a: i32 = 4; var b: i32 = 5; var s: string = f"{a + b}"; return s[0] as i32;`, 57},
}

func fstringIRSrc(mainBody string) string {
	return "import \"std/i32\";\nimport \"std/string\";\n" + "function main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostFStringIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostFStringIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range fstringIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, fstringIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
