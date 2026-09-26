package e2eselfhost

import "testing"

// u32InferredCmpIRCases pin the fix for #3537: an *inferred* unsigned local
// (`var a = X as u32` / `as u64`, with no explicit `: u32` annotation) must
// compare as UNSIGNED. Before the fix, irlower's StmtVar lowering only marked a
// slot u32/u64 from the explicit annotation, so an inferred binding kept the
// signed default and a later `a > b` emitted a signed IR compare — wrong once
// bit 31 (u32) / bit 63 (u64) is set. The register backends happened to be
// correct (the value sits zero-extended in a 64-bit GPR), so this only
// MIScompiled on wasm's true-32-bit `i32.gt_s`; the interp doesn't catch a
// wrong-value compile, hence the value-pinned IR regression. The x86-64 variant
// guards against a regression now that the fix marks inferred u32/u64 on every
// backend. Each program returns 1 (the comparison is true).
var u32InferredCmpIRCases = []struct {
	name string
	main string
	want int
}{
	// 3_000_000_000 (bit 31 set) > 5 — signed i32 would read it negative.
	{"u32-gt", `var a = 3000000000 as u32; var b = 5 as u32; if (a > b) { return 1; } return 0;`, 1},
	// same value on the right of a `<`.
	{"u32-lt", `var a = 3000000000 as u32; var b = 5 as u32; if (b < a) { return 1; } return 0;`, 1},
	// equal large values via `>=`.
	{"u32-ge", `var a = 3000000000 as u32; var b = 3000000000 as u32; if (a >= b) { return 1; } return 0;`, 1},
	// u64 sibling: 1.8e19 has bit 63 set — signed i64 would read it negative.
	{"u64-gt", `var a = 18000000000000000000 as u64; var b = 5 as u64; if (a > b) { return 1; } return 0;`, 1},
}

func u32InferredCmpIRSrc(mainBody string) string {
	return "function main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostU32InferredCmpIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostU32InferredCmpIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range u32InferredCmpIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, u32InferredCmpIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
