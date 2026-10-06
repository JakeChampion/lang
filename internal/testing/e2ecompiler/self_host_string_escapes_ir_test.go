package e2ecompiler

import "testing"

// stringEscapeIRCases exercise string-literal C-style escape sequences through
// the self-host IR path on x86-64 + wasm. Escapes are decoded in the lexer
// (scan_string in compiler/lexer.fern: \t \n \r \0 \\ \" plus \xNN
// hex bytes), so a literal carrying any of them is an ordinary string box and
// lowers exactly like a plain literal — `.len()`, byte indexing (`s[i] as i32`),
// and `+` concat all stay on the IR path.
//
// This pins the foundational "String literals + escape sequences" audit row
// (docs/FEATURE-AUDIT.md) on the self-hosted compiler. Each escape is exactly
// one byte in a length-prefixed string (an embedded NUL via \0 / \x00 counts
// like any other byte — these are not C strings). Every case is oracle-checked
// against the interpreter and returns a value <= 126 (wasmtime exit-code
// truncation, cf. #2908).
var stringEscapeIRCases = []struct {
	name string
	main string
}{
	// \t and \n each count as one byte: a TAB b LF c -> 5.
	{"newline-tab-len", `function main(): i32 { return "a\tb\nc".len(); }`},
	// Escaped backslash and double-quote: x \ y " z -> 5.
	{"backslash-quote", `function main(): i32 { return "x\\y\"z".len(); }`},
	// \xNN hex byte, read back by index: '\x41' == 'A' == 65.
	{"hex-escape-A", `function main(): i32 { return ("\x41")[0] as i32; }`},
	// Lowercase hex byte: '\x7a' == 'z' == 122.
	{"hex-escape-z", `function main(): i32 { return ("\x7a")[0] as i32; }`},
	// CR followed by an embedded NUL: both count -> 2.
	{"cr-null-len", `function main(): i32 { return "\r\0".len(); }`},
	// \xNN form of NUL is still one byte.
	{"null-hex-len", `function main(): i32 { return "\x00".len(); }`},
	// Byte index lands on the decoded LF: "a\nb"[1] == '\n' == 10.
	{"escape-byte-index", `function main(): i32 { let s: string = "a\nb"; return s[1] as i32; }`},
	// Concat of two single-escape literals: "\t" + "\n" -> 2.
	{"concat-escapes", `function main(): i32 { return ("\t" + "\n").len(); }`},
	// A mix of every escape in one literal: a TAB b \ c " d LF e -> 9.
	{"mixed-escapes-len", `function main(): i32 { return "a\tb\\c\"d\ne".len(); }`},
}

// TestSelfHostStringEscapesIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostStringEscapesIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range stringEscapeIRCases {
		src := tc.main + "\n"
		want := interpExit(t, interpBin, src)
		for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("exited %d, want %d (interp oracle)\n%s", code, want, stderr)
				}
			})
		}
	}
}
