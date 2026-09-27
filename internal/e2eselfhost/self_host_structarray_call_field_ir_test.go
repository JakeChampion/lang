package e2eselfhost

import "testing"

// structArrayCallFieldIRCases pin field access on a struct ELEMENT indexed directly
// from a function that returns an array of structs (`mk()[i].field`) to the
// self-host IR path on x86-64 + wasm. The neighbours already lowered — binding
// the array first (`var a = mk(); a[i].field`) and indexing without a field
// (`var p = mk()[i]`) — but the inline `mk()[i].field` shape bailed to the
// legacy AST emitter: expr_struct_type's ExprIndex arm had no ExprCall case, so
// it couldn't recover the element type for `mk()[i]` and the field read failed.
// #2691 adds that case (struct_ret_type already records the P[]-return element type
// "P", #3035). Each case is oracle-checked against the interpreter and returns
// <= 126. Mirrors self_host_nested_array_ir_test.go.
var structArrayCallFieldIRCases = []struct {
	name string
	main string
}{
	// Field reads off two elements indexed from the call. 3 + 2 = 5.
	{"call-idx-field-sum", `struct P { x: i32, y: i32 } function mk(): P[] { return [P { x: 1, y: 2 }, P { x: 3, y: 4 }]; } function main(): i32 { return mk()[1].x + mk()[0].y; }`},
	// First element's x. 1.
	{"call-idx-field-x", `struct P { x: i32, y: i32 } function mk(): P[] { return [P { x: 1, y: 2 }, P { x: 3, y: 4 }]; } function main(): i32 { return mk()[0].x; }`},
	// Second element's y. 4.
	{"call-idx-field-y", `struct P { x: i32, y: i32 } function mk(): P[] { return [P { x: 1, y: 2 }, P { x: 3, y: 4 }]; } function main(): i32 { return mk()[1].y; }`},
	// Regression: binding the array first (already lowered) stays on the IR path. 5.
	{"bind-first", `struct P { x: i32, y: i32 } function mk(): P[] { return [P { x: 1, y: 2 }, P { x: 3, y: 4 }]; } function main(): i32 { var a: P[] = mk(); return a[1].x + a[0].y; }`},
	// Regression: index-only (no field, already lowered) stays on the IR path. 3.
	{"index-only", `struct P { x: i32, y: i32 } function mk(): P[] { return [P { x: 1, y: 2 }, P { x: 3, y: 4 }]; } function main(): i32 { var p = mk()[1]; return p.x; }`},
}

// TestSelfHostStructArrayCallFieldIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostStructArrayCallFieldIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range structArrayCallFieldIRCases {
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
