package e2ecompiler

import "testing"

// arrPushIRCases exercise `arr.append(v)` (op_arr_push) through the self-host IR
// path on x86-64 + wasm.
//
// Like string_from_bytes_unchecked, the wasm IR backend emitted `op_arr_push` as a `call
// $__fern_arr_push` but `wasm_ir_run` had no gate to emit that helper — so any
// IR-path program using `.append` produced a wasm module with a dangling call that
// failed to link. x86-64 / arm64 already emitted it. The fix gates the standalone
// `wasm.arr_push_helper()` (push-only, so it doesn't double-define the separately
// gated slice helpers) on `module_emits_op(mod, "arr_push")`.
//
// Each case is oracle-checked against the interpreter and returns a
// non-negative value <= 126 (cf. #2908).
var arrPushIRCases = []struct {
	name string
	main string
}{
	// Append three, read length.
	{"len", `function main(): i32 { let a: i32[] = []; a = a.append(1); a = a.append(2); a = a.append(3); return a.len(); }`},
	// Append to a non-empty array, index the new element.
	{"index", `function main(): i32 { let a: i32[] = [10]; a = a.append(20); return a[1]; }`},
	// Append in a loop (exercises geometric growth / realloc), index midway.
	{"loop-grow", `function main(): i32 { let a: i32[] = []; let i: i32 = 0; while (i < 10) { a = a.append(i * i); i = i + 1; } return a[7]; }`},
	// Sum a loop-built array.
	{"loop-sum", `function main(): i32 { let a: i32[] = []; let i: i32 = 0; while (i < 5) { a = a.append(i + 1); i = i + 1; } let s: i32 = 0; for x in a { s = s + x; } return s; }`},
}

// TestSelfHostArrPushIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostArrPushIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range arrPushIRCases {
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
