package e2eselfhost

import "testing"

// i64FieldWidthIRCases pin an i32 STRUCT FIELD or i32 TUPLE ELEMENT consumed in
// an i64 arithmetic context (`s64 + p.x`, `s64 + t.0`) to the self-host IR path
// on x86-64 + wasm. lower_i64's ExprFieldAccess arm must not lower only i64
// struct fields / i64 tuple elements (8-byte struct_get_i64 / tuple_get_w) and
// bail every other field via `return s.fail()`, dropping the whole module to
// the legacy AST emitter. #2691 widens it: an i32/u32 struct field or tuple
// element had its value lowered via lower_expr and sign/zero-extended to i64
// (op_int_extend). The checker forbids i64 + u32 (E009), so a plain i32 member
// here is signed; the u32 flag stays defensive. This is the struct/tuple sibling
// of the i32-ident and i32-array-element widenings. Each case narrows the i64
// result with `as i32` (valid wasm exit code in [0,126)) and is oracle-checked.
var i64FieldWidthIRCases = []struct {
	name string
	main string
}{
	// i64 local + i32 struct field. 30 + 12 = 42.
	{"struct-field", `struct P { x: i32 } function main(): i32 { var p: P = P { x: 12 }; var s: i64 = 30; return (s + p.x) as i32; }`},
	// Sign-extension: a NEGATIVE i32 field must sign-extend. 50 + (-8) = 42.
	{"struct-neg", `struct P { x: i32 } function main(): i32 { var p: P = P { x: -8 }; var s: i64 = 50; return (s + p.x) as i32; }`},
	// Two i32 fields summed into i64. 20 + 22 = 42.
	{"struct-two", `struct P { x: i32, y: i32 } function main(): i32 { var p: P = P { x: 20, y: 22 }; var s: i64 = 0; return (s + p.x + p.y) as i32; }`},
	// i64 local + i32 tuple element. 30 + 12 = 42.
	{"tuple-elem", `function main(): i32 { var t: (i32, i32) = (12, 7); var s: i64 = 30; return (s + t.0) as i32; }`},
	// Sign-extension on a tuple element. 50 + (-8) = 42.
	{"tuple-neg", `function main(): i32 { var t: (i32, i32) = (-8, 1); var s: i64 = 50; return (s + t.0) as i32; }`},
	// Regression: an i64 struct field still uses the 8-byte read. 0 + 42 = 42.
	{"struct-i64-keep", `struct P { x: i64 } function main(): i32 { var p: P = P { x: 42 }; var s: i64 = 0; return (s + p.x) as i32; }`},
	// Regression: an i64 tuple element still uses the 8-byte read. 0 + 42 = 42.
	{"tuple-i64-keep", `function main(): i32 { var t: (i64, i32) = (42, 1); var s: i64 = 0; return (s + t.0) as i32; }`},
}

// TestSelfHostI64FieldWidthIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostI64FieldWidthIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range i64FieldWidthIRCases {
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
