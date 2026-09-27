package e2eselfhost

import "testing"

// nestedTupleIRCases widen the self-host IR subset: a tuple element that is itself
// a tuple — `(1, (2, 3))`, accessed `t.1.1` — now lowers on the IR path. Before,
// the tuple-element tag encoding split on commas, so any element whose own tag
// contained a comma (a nested tuple) was rejected and the whole module bailed.
// The fix makes the tag decoders (`tuple_elem_tag`, `csv_nth`)
// depth-aware — counting `(`/`[` … `)`/`]` so inner commas don't split the outer
// tag — adds an `ExprTuple` arm to `elem_type_tag` (a nested element gets its own
// `(t0,t1,…)` spelling), admits a tuple element at construction (it is a leak-only
// heap-tuple pointer, one slot like a struct/string/array element), and recovers
// the `t.N.M` element type via `expr_tuple_elem_tag`.
//
// Each case is oracle-checked against the interpreter and returns a value
// <= 126 (cf. the wasmtime exit-code gap #2908).
var nestedTupleIRCases = []struct {
	name string
	main string
}{
	// Right-nested: element 1 is (i32, i32); read its element 1.
	{"right-nest", `function main(): i32 { var t: (i32, (i32, i32)) = (1, (2, 3)); return t.1.1; }`},
	// Sum across the nesting boundary.
	{"sum-across", `function main(): i32 { var t: (i32, (i32, i32)) = (1, (2, 3)); return t.0 + t.1.0 + t.1.1; }`},
	// Left-nested: element 0 is the inner tuple.
	{"left-nest", `function main(): i32 { var t: ((i32, i32), i32) = ((4, 5), 6); return t.0.0 + t.0.1 + t.1; }`},
	// Triple nesting: t.1.1.1.
	{"triple-nest", `function main(): i32 { var t: (i32, (i32, (i32, i32))) = (1, (2, (3, 4))); return t.1.1.1; }`},
	// A string inside a nested tuple (pointer element) round-trips.
	{"nest-with-string", `function main(): i32 { var t: (i32, (string, i32)) = (1, ("ab", 9)); return t.1.0.len() + t.1.1; }`},
	// An i64 sibling after a nested tuple element exercises the depth-aware kind
	// decode (the nested element must not shift the i64's store width).
	{"nest-then-i64", `function main(): i32 { var t: ((i32, i32), i64) = ((1, 2), 5000000000); return t.0.0 + t.0.1 + (t.1 / 1000000000) as i32; }`},
}

// TestSelfHostNestedTupleIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostNestedTupleIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range nestedTupleIRCases {
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
