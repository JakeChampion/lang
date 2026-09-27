package e2eselfhost

import "testing"

// nestedTupleRetIRCases extend nested-tuple support to the RETURN/PARAM positions:
// a function whose return type or a parameter type is a nested tuple
// (`(i32, (i32, i32))`) now lowers on the IR path. Construction/access landed in
// the prior nested-tuple change; this widens the gate `tuple_elems_lowerable` to
// (a) split element tags depth-aware and (b) admit a nested-tuple element by
// recursing — the same leak-only-pointer treatment a struct/Option element gets.
//
// Each case is oracle-checked against the interpreter and returns a value
// <= 126 (cf. the wasmtime exit-code gap #2908).
var nestedTupleRetIRCases = []struct {
	name string
	main string
}{
	// Return a right-nested tuple, read the inner element.
	{"ret-right-nest", "function f(): (i32, (i32, i32)) { return (1, (2, 3)); }\nfunction main(): i32 { var t = f(); return t.1.1; }"},
	// Return + sum across the nesting boundary.
	{"ret-sum-across", "function f(): (i32, (i32, i32)) { return (1, (2, 3)); }\nfunction main(): i32 { var t = f(); return t.0 + t.1.0 + t.1.1; }"},
	// Left-nested return with a string sibling (both pointer elements).
	{"ret-left-nest-str", "function f(): ((i32, i32), string) { return ((4, 5), \"ab\"); }\nfunction main(): i32 { var t = f(); return t.0.0 + t.0.1 + t.1.len(); }"},
	// A nested tuple in PARAM position.
	{"param-nested", "function f(t: (i32, (i32, i32))): i32 { return t.0 + t.1.1; }\nfunction main(): i32 { return f((1, (2, 3))); }"},
	// Flat-tuple return regression (must stay on the IR path).
	{"ret-flat", "function f(): (i32, i32) { return (3, 4); }\nfunction main(): i32 { var t = f(); return t.0 + t.1; }"},
}

// TestSelfHostNestedTupleRetIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostNestedTupleRetIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range nestedTupleRetIRCases {
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
