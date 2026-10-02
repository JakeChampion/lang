package e2eselfhost

import "testing"

// constStringIRCases pin a direct STRING operation (`.len()`, concat) on a bare
// const-string reference to the self-host IR path on x86-64 + wasm. A
// `const NAME: string = "hi"` desugars to a zero-arg string-returning function;
// a bare `NAME` in value position lowers to a call of that const fn. The const
// ident has no LOCAL slot, so `expr_is_str`'s ExprIdent arm (which only checked
// local string slots) returned false for it — and the `.len()` dispatch site
// then emitted op_arr_len (an array-header read) on a STRING box instead of
// op_str_len. str_len and arr_len read different length fields, so `NAME.len()`
// silently miscompiled to 0 on the x86 IR backend (wasm + interp were correct).
// #2691 widens expr_is_str: a no-local-slot ident that is a const fn with a
// `string` return type now reads as a string, so the dispatch picks op_str_len.
// Each case is oracle-checked against the interpreter and returns <= 126.
var constStringIRCases = []struct {
	name string
	main string
}{
	// The regressing case: `.len()` on a const string. "hi" -> 2.
	{"const-str-len", `const NAME: string = "hi"; function main(): i32 { return NAME.len(); }`},
	// A longer const string. "abc" -> 3.
	{"const-str-len3", `const NAME: string = "abc"; function main(): i32 { return NAME.len(); }`},
	// Concat of a const string with a literal, then length. "hi"+"xx" -> 4.
	{"const-str-concat", `const NAME: string = "hi"; function main(): i32 { return (NAME + "xx").len(); }`},
	// Regression: a const-i32 reference (no string involvement) still resolves. 8.
	{"const-i32-ref", `const NAME: i32 = 8; function main(): i32 { return NAME; }`},
	// Regression: `.len()` on a LOCAL string slot was always correct. 2.
	{"local-str-len", `function main(): i32 { let s: string = "hi"; return s.len(); }`},
	// A literal carrying escaped bytes exercises asmcore.escape_for_ascii's
	// multi-byte-escape branches (\n and \") when the const is emitted as a
	// `.ascii` directive (#4379 rewrote that escaper to a u8[] buffer). The
	// decoded string is a\nb"c -> 5 bytes; oracle-checked against the interp.
	{"const-str-escapes", `const NAME: string = "a\nb\"c"; function main(): i32 { return NAME.len(); }`},
}

// TestSelfHostConstStringIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostConstStringIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range constStringIRCases {
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
