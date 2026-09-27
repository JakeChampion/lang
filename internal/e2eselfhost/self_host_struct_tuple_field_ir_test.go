package e2eselfhost

import "testing"

// structTupleFieldIRCases widen the self-host IR subset: a struct field whose type
// is a nested tuple (`(i32, (i32, i32))`) — or a tuple carrying an Option/Result —
// now lowers on the IR path. Flat-tuple struct fields already lowered; the gate
// `is_leaksafe_tuple_field` rejected any element that wasn't a bare scalar/string,
// so a nested-tuple / Option / Result element bailed the whole struct (and thus
// the module) to AST. The fix recurses that gate on a nested-tuple element and
// accepts an Option/Result element (all leak-only one-pointer boxes); the field
// access `p.t.N.M` already typed correctly via the depth-aware `expr_tuple_elem_tag`.
//
// Each case is oracle-checked against the interpreter and returns a value
// <= 126 (cf. the wasmtime exit-code gap #2908).
var structTupleFieldIRCases = []struct {
	name string
	main string
}{
	// Nested-tuple struct field, read the deep element.
	{"nested-deep", `struct P { t: (i32, (i32, i32)) } function main(): i32 { var p = P{t: (1, (2, 3))}; return p.t.1.1; }`},
	// Sum across the nesting boundary of a struct field.
	{"nested-sum", `struct P { t: (i32, (i32, i32)) } function main(): i32 { var p = P{t: (1, (2, 3))}; return p.t.0 + p.t.1.0 + p.t.1.1; }`},
	// A tuple struct field carrying an Option element.
	{"tuple-option", `struct P { t: (Option[i32], i32) } function main(): i32 { var p = P{t: (Some(5), 3)}; match (p.t.0) { Some(n) => { return n + p.t.1; }, None => { return 0; } } }`},
	// A tuple struct field carrying a Result element.
	{"tuple-result", `struct P { t: (Result[i32, string], i32) } function main(): i32 { var p = P{t: (Ok(5), 3)}; match (p.t.0) { Ok(n) => { return n + p.t.1; }, Err(e) => { return 0; } } }`},
	// Flat-tuple struct field regression (must stay on the IR path).
	{"flat-regress", `struct P { t: (i32, i32) } function main(): i32 { var p = P{t: (5, 6)}; return p.t.0 + p.t.1; }`},
	// A tuple struct field carrying a STRING-ARRAY element `(i32, string[])`: the
	// array is a heap pointer in one tuple slot, leaking with the (leak-only) tuple
	// field — the set tuple_elems_lowerable already admits for tuple construction /
	// return, now aligned in the struct-field admission gate. `p.t.1.len()` reads
	// the array length → 5 + 2 = 7.
	{"tuple-strarr", `struct P { t: (i32, string[]) } function main(): i32 { var p = P{t: (5, ["a", "bb"])}; return p.t.0 + p.t.1.len(); }`},
	// A SCALAR-array element `(i32, i32[])`, indexing an element too → 5 + 3 + 30 = 38.
	{"tuple-i32arr", `struct P { t: (i32, i32[]) } function main(): i32 { var p = P{t: (5, [10, 20, 30])}; return p.t.0 + p.t.1.len() + p.t.1[2]; }`},
	// Reading a STRING element OUT of the tuple field's string array — `p.t.1[1]`
	// is a string whose `.len()` must resolve → 5 + 3 = 8.
	{"tuple-strarr-elem", `struct P { t: (i32, string[]) } function main(): i32 { var p = P{t: (5, ["a", "bbb"])}; return p.t.0 + p.t.1[1].len(); }`},
	// An 8-byte f64-array element alongside a plain scalar field → 3 + 5 + 2 = 10.
	{"tuple-f64arr", `struct P { n: i32, t: (i32, f64[]) } function main(): i32 { var p = P{n: 3, t: (5, [1.0, 2.0])}; return p.n + p.t.0 + p.t.1.len(); }`},
	// A STRUCT-array element `(i32, Inner[])`: the struct view (is_leaksafe_tuple_
	// field_d) classifies it, and the `.N` read binds the slot mark_arr + the
	// element struct name so `p.t.1[i].field` resolves → 5 + 2 + 2 = 9.
	{"tuple-structarr", `struct Inner { a: i32 } struct P { t: (i32, Inner[]) } function main(): i32 { var p = P{t: (5, [Inner{a:1}, Inner{a:2}])}; return p.t.0 + p.t.1.len() + p.t.1[1].a; }`},
	// An ENUM-array element `(i32, Color[])` — read the length → 5 + 2 = 7.
	{"tuple-enumarr", `enum Color { Red, Green, Blue } struct P { t: (i32, Color[]) } function main(): i32 { var p = P{t: (5, [Color.Red, Color.Green])}; return p.t.0 + p.t.1.len(); }`},
	// An enum-array element with a MATCH over an indexed element — the element's
	// enum name must resolve for `match (p.t.1[1])` → 5 + 2 + 100 = 107.
	{"tuple-enumarr-match", `enum Color { Red, Green, Blue } struct P { t: (i32, Color[]) } function main(): i32 { var p = P{t: (5, [Color.Red, Color.Blue])}; var s = p.t.0 + p.t.1.len(); match (p.t.1[1]) { Color.Blue => { s = s + 100; }, _ => {} } return s; }`},
}

// TestSelfHostStructTupleFieldIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostStructTupleFieldIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range structTupleFieldIRCases {
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
