package e2ecompiler

import "testing"

// largeIntLiteralIRCases exercise i64 / u64 LITERALS above the i32 range through
// the self-host IR path. `n as i64` / `n as u64` widens its operand; a numeric
// literal operand is already a 64-bit value, so it must lower to an `i64.const`,
// not an `i32.const` (the latter truncates and, for a value above the i32 range,
// is invalid WAT — `i32.const 9000000000000000000` is rejected by wasm). #2928.
//
// The literals have nonzero low bits so a 32-bit truncation would give a
// different (wrong) answer rather than coincidentally matching. Each exit code
// is oracle-checked against the reference interpreter. (Return values are kept
// <= 126 so the comparison is clean: wasmtime maps a larger WASI exit value to
// 1, whereas the native exit path truncates mod 256 — a difference unrelated to
// the lowering under test.)
var largeIntLiteralIRCases = []struct {
	name string
	src  string
}{
	// u64 literal ~9e18 (> i32 and > 2^31, < 2^63): big % 1000 = 123.
	{"u64-literal-mod", `function main(): i32 {
    let big: u64 = 9000000000000000123 as u64;
    return (big % 1000 as u64) as i32;
}`},
	// i64 literal ~5e18 round-trips through an i64.const: a 32-bit truncation
	// would not compare equal. (`% 1000` would be 457 > 126, so a round-trip is
	// used instead to keep the exit code in range.)
	{"i64-literal-roundtrip", `function main(): i32 {
    let n: i64 = 5000000000000000457 as i64;
    if (n == 5000000000000000457 as i64) { return 77; }
    return 0;
}`},
	// Large u64 literal feeding an unsigned compare (literal + #2917 path):
	// 9e18 > 1 is true → 7.
	{"u64-literal-compare", `function main(): i32 {
    let big: u64 = 9000000000000000000 as u64;
    if (big > 1 as u64) { return 7; }
    return 0;
}`},
}

// TestSelfHostLargeIntLiteralIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostLargeIntLiteralIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range largeIntLiteralIRCases {
		src := tc.src
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
