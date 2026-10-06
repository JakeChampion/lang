package e2ecompiler

import "testing"

// u64ArrayIRCases widen the self-host IR subset to u64[] arrays. i64[] / f64[]
// already used the 8-byte-element path (op_arr_make_i64 + the i64arr element-width
// mark); u64[] was deferred. The fix uses the SAME 8-byte path and marks the slot
// u64 for UNSIGNED element arithmetic — and crucially is_i64_slot now excludes
// array (pointer) slots, so a u64[] local stays an i32 pointer (the wasm verifier
// rejects an i32 array pointer stored into an i64 local). u64[] as a struct field
// still bails for now (field-tag width dispatch — a separate increment).
//
// Each case is oracle-checked against the interpreter and returns a value
// <= 120 (cf. the wasmtime exit-code gap #2908).
var u64ArrayIRCases = []struct {
	name string
	main string
}{
	{"len", `function main(): i32 { let xs: u64[] = [1 as u64, 2 as u64, 3 as u64]; return xs.len(); }`},
	{"index", `function main(): i32 { let xs: u64[] = [10 as u64, 20 as u64, 30 as u64]; return xs[1] as i32; }`},
	{"iterate", `function main(): i32 { let xs: u64[] = [1 as u64, 2 as u64, 3 as u64, 4 as u64]; let s: u64 = 0 as u64; for x in xs { s = s + x; } return s as i32; }`},
	{"alias", `function main(): i32 { let xs: u64[] = [7 as u64, 8 as u64]; let ys: u64[] = xs; return ys[0] as i32; }`},
	{"wide-value", `function main(): i32 { let xs: u64[] = [5000000007 as u64]; return (xs[0] % 1000 as u64) as i32; }`},
	{"as-param", `function total(xs: u64[]): u64 { let s: u64 = 0 as u64; for x in xs { s = s + x; } return s; }
function main(): i32 { let xs: u64[] = [5 as u64, 6 as u64, 7 as u64]; return total(xs) as i32; }`},
	{"i64-regress", `function main(): i32 { let xs: i64[] = [10, 20, 30]; let s: i64 = 0; for x in xs { s = s + x; } return s as i32; }`},
}

// TestSelfHostU64ArrayIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostU64ArrayIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range u64ArrayIRCases {
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
