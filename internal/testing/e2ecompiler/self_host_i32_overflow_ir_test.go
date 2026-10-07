package e2ecompiler

import "testing"

// i32OverflowIRCases exercise i32 signed-overflow WRAP through the self-host IR
// path (#3581). Plain-i32 arithmetic is computed in a 64-bit slot, so it must be
// narrowed after a +/-/*/<<: `2147483647 + 1` wraps to -2147483648, as in the
// interpreter, rather than keeping the wide value 2147483648. Each case is
// oracle-checked against the interpreter and returns a small non-negative value.
var i32OverflowIRCases = []struct {
	name string
	main string
}{
	// Add overflow wraps to negative.
	{"add", `function main(): i32 { let x = 2147483647; let y = x + 1; if (y < 0) { return 1; } return 0; }`},
	// 65536 * 65536 == 2^32, which wraps to 0 in i32.
	{"mul", `function main(): i32 { let x = 65536; let y = x * x; if (y == 0) { return 5; } return 0; }`},
	// Left shift past bit 31 drops the high bits: 1 << 31 is INT_MIN (< 0).
	{"shl", `function main(): i32 { let x = 1; let y = x << 31; if (y < 0) { return 3; } return 0; }`},
	// Subtraction underflow wraps: -2e9 - 2e9 == -4e9, which wraps up to the
	// positive 294967296 in i32. (In-range literals throughout — a literal at
	// exactly INT_MIN's magnitude is a separate i32/i64-typing concern.)
	{"sub", `function main(): i32 { let x = 0 - 2000000000; let y = x - 2000000000; if (y > 0) { return 7; } return 0; }`},
}

// TestSelfHostI32OverflowIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostI32OverflowIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range i32OverflowIRCases {
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
