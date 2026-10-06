package e2ecompiler

import "testing"

// u32DivRemIRCases guard a real correctness bug: u32 `/` and `%` were lowered as
// SIGNED div/rem on the self-host IR path, so a u32 numerator >= 2^31 (which reads
// as signed-negative in a 32-bit slot) produced the wrong quotient on x86-64 and
// wasm (arm64 happened to be right because its 64-bit register held the value
// zero-extended). The interpreter computes unsigned. The fix remaps u32 div_s/rem_s
// to div_u/rem_u (the same treatment u32 ordering compares and u64 div already get)
// and adds i32.div_u/i32.rem_u to the wasm backend.
//
// Each case is oracle-checked against the interpreter and returns a value
// <= 126 (the high-bit-set quotients are reduced to a small code via an
// equality branch, cf. #2908).
var u32DivRemIRCases = []struct {
	name string
	main string
}{
	// 3e9 / 3 = 1e9 (unsigned). Signed div of 3e9-as-i32 (negative) differs.
	{"div-highbit", `function main(): i32 { let u: u32 = 3000000000 as u32; if (u / (3 as u32) == (1000000000 as u32)) { return 5; } return 9; }`},
	// 3000000003 % 10 = 3 (unsigned).
	{"rem-highbit", `function main(): i32 { let u: u32 = 3000000003 as u32; if (u % (10 as u32) == (3 as u32)) { return 5; } return 9; }`},
	// 4e9 / 2 = 2e9 (unsigned).
	{"div-4e9", `function main(): i32 { let u: u32 = 4000000000 as u32; if (u / (2 as u32) == (2000000000 as u32)) { return 5; } return 9; }`},
	// Low-value u32 div still works and fits the exit code directly: 100/4 = 25.
	{"div-low", `function main(): i32 { let u: u32 = 100 as u32; return (u / (4 as u32)) as i32; }`},
	// u32 rem low value: 100 % 7 = 2.
	{"rem-low", `function main(): i32 { let u: u32 = 100 as u32; return (u % (7 as u32)) as i32; }`},
}

// TestSelfHostU32DivRemIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostU32DivRemIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range u32DivRemIRCases {
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
