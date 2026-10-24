package e2ecompiler

import "testing"

var fstringIRCases = []struct {
	name string
	main string
	want int
}{
	{"i32-eq-len", `let n: i32 = 42; let s: string = f"v={n}"; if (s == "v=42") { return s.len(); } return 99;`, 4},
	{"literal-interp", `let x: i32 = 7; let s: string = f"x={x}"; return s.len();`, 3},
	{"multi-interp", `let a: i32 = 1; let b: i32 = 2; let s: string = f"{a}-{b}"; return s.len();`, 3},
	{"string-interp", `let name: string = "hi"; let s: string = f"[{name}]"; return s.len();`, 4},
	{"byte-index", `let n: i32 = 5; let s: string = f"n{n}"; return s[1] as i32;`, 53},
	{"multi-digit", `let n: i32 = 100; let s: string = f"={n}="; return s.len();`, 5},
	{"empty", `let s: string = f""; return s.len();`, 0},
	{"method-len", `let s: string = "abcd"; let t: string = f"L{s.len()}"; return t.len();`, 2},
	{"method-byte", `let s: string = "abc"; let t: string = f"{s.len()}"; return t[0] as i32;`, 51},
	{"arith", `let n: i32 = 20; let s: string = f"={n + 5}="; return s.len();`, 4},
	{"arith-byte", `let a: i32 = 4; let b: i32 = 5; let s: string = f"{a + b}"; return s[0] as i32;`, 57},
	{"nested-call-chain", `let s: string = "ab"; let t: string = f"{s.repeat(s.len()).repeat(2).len()}"; return t[0] as i32;`, 56},
	{"brace-literal", `let t: string = f"<{"}{".len()}>"; return t[1] as i32;`, 50},
	{"brace-literal-arg", `let t: string = f"{"a{b}}c".count("}")}"; return t[0] as i32;`, 50},
	{"nested-fstring", `let n: i32 = 7; let t: string = f"[{f"{n}"}]"; return t.len();`, 3},
}

func fstringIRSrc(mainBody string) string {
	return "import \"std/i32\";\nimport \"std/string\";\n" + "function main(): i32 { " + mainBody + " }\n"
}

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
