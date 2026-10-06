package e2ecompiler

import "testing"

// u64ArrArrIRCases exercise a nested u64[][] element read (m[i][j]) chained in
// an unsigned op. The arrarr layer records the inner element kind as a string;
// before #5206 a u64[][] inner element was classified "i64" (its width only),
// so expr_is_u64's nested-ExprIndex arm couldn't recover the sign and the read
// used a SIGNED shift / compare — diverging once bit 63 was set. The fix keeps a
// distinct "u64" arrarr tag (8-byte WIDTH still shared with "i64" via
// arr_index_is_i64) so shr_u / lt_u are selected.
//
// Oracle-checked against the interpreter, values <= 120 (the wasmtime
// exit-code gap #2908). The wide element 18000000000000000000 has bit 63 set,
// so signed vs unsigned shift/compare give DIFFERENT results — the case would
// pass trivially on the old signed path only if they coincided.
var u64ArrArrIRCases = []struct {
	name string
	main string
}{
	// m[0][0] >> 58: unsigned = 0xF9CCD8A1C5080000 >> 58 = 62; signed (arith) = 254.
	{"nested-shr", `function main(): i32 { let m: u64[][] = [[18000000000000000000, 1], [2, 3]]; let r: u64 = m[0][0] >> 58; return r as i32; }`},
	// m[0][0] > 100: unsigned true (7); signed (negative) false (9).
	{"nested-cmp", `function main(): i32 { let m: u64[][] = [[18000000000000000000, 1], [2, 3]]; if (m[0][0] > (100 as u64)) { return 7; } return 9; }`},
	// Alias a u64[][] local, then nested-index the alias: the "u64" arrarr tag
	// must propagate across the aliasing bind.
	{"nested-alias", `function main(): i32 { let m: u64[][] = [[18000000000000000000, 1], [2, 3]]; let n: u64[][] = m; let r: u64 = n[0][0] >> 58; return r as i32; }`},
	// i64[][] width regression: the shared 8-byte width path must still read the
	// full element (not truncate) — value fits so signed/unsigned agree here.
	{"i64-width-regress", `function main(): i32 { let m: i64[][] = [[10, 20], [30, 40]]; return m[1][0] as i32; }`},
}

// TestSelfHostU64ArrArrIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostU64ArrArrIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range u64ArrArrIRCases {
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
