package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lambdaRetInferCases cover the return type of a LIFTED lambda whose body is an
// index or a tuple-element read.
//
// A lambda is hoisted to a synthesised `__lam_N` FuncDecl carrying `ret_type:
// lam.ret_type`, which for an unannotated arrow lambda is "". `irt_guess`
// (parser.fern) then infers it from the return expression — but it had arms only
// for literals, idents, binaries, struct literals, array literals and calls.
// An `ExprIndex` or `ExprFieldAccess` body fell through to "", the function
// defaulted to returning i32, and an f64 body failed the wasm validator INSIDE
// the lifted function: "type mismatch: expected i32, found f64" in `__lam_0`.
//
// The inference needs no slot table and no annotation: a captured value arrives
// as a PARAM of the lifted function (named by irt_param_type) and a call source
// is named by irt_func_ret — the two sources irt_guess already consults. The
// tuple arm reuses me_tuple_elem_type, the depth-aware "(T0, T1)" decoder that
// already existed for match-scrutinee typing.
//
// Note these cases route "ir" and the compiler exits 0 either way; only running
// the output catches them.
var lambdaRetInferCases = []struct {
	name string
	src  string
}{
	// Index of a CAPTURED array — the capture arrives as a lifted-fn param.
	{"lambda_index_captured_f64_array", `function main(): i32 {
    var a: f64[] = [1.5, 4.5];
    var f: () => f64 = () => a[1];
    return (f() * 10.0) as i32;
}`}, // 45
	// Index of a CALL result inside the lambda — named by irt_func_ret.
	{"lambda_index_call_f64_array", `function mk(): f64[] { return [1.5, 4.5]; }
function main(): i32 {
    var f: () => f64 = () => mk()[1];
    return (f() * 10.0) as i32;
}`}, // 45
	// Tuple element of a captured tuple — the ExprFieldAccess arm.
	{"lambda_tuple_elem_captured", `function mk(): (f64, i32) { return (4.5, 7); }
function main(): i32 {
    var t: (f64, i32) = mk();
    var f: () => f64 = () => t.0;
    return (f() * 10.0) as i32 + t.1;
}`}, // 52
	// Regression guard: a float LITERAL body already inferred correctly and must
	// keep doing so (the arm this fix sits beside).
	{"lambda_literal_f64", `function main(): i32 {
    var f: () => f64 = () => 4.5;
    return (f() * 10.0) as i32;
}`}, // 45
	// Regression guard: a CALL body already inferred via irt_func_ret.
	{"lambda_call_f64", `function mk(): f64 { return 4.5; }
function main(): i32 {
    var f: () => f64 = () => mk();
    return (f() * 10.0) as i32;
}`}, // 45
	// Negative guard: an i32 element must NOT be inferred f64 — the element type
	// comes from the array's own spelling, not from a default.
	{"lambda_index_i32_array_narrow", `function main(): i32 {
    var a: i32[] = [1, 40];
    var f: () => i32 = () => a[1];
    return f() + 2;
}`}, // 42
	// Negative guard: a string element keeps its pointer shape.
	{"lambda_index_string_array", `function main(): i32 {
    var a: string[] = ["ab", "cdef"];
    var f: () => string = () => a[1];
    return f().len() + 38;
}`}, // 42
	// Struct FIELD body — needs the struct table threaded into the inference
	// (me_field_type_of).
	{"lambda_struct_field", `struct P { x: f64 }
function main(): i32 {
    var p: P = P { x: 4.5 };
    var f: () => f64 = () => p.x;
    return (f() * 10.0) as i32;
}`}, // 45
	// Unary body — `-x` keeps the operand's type.
	{"lambda_unary_neg", `function main(): i32 {
    var a: f64[] = [1.5, 4.5];
    var f: () => f64 = () => -a[1];
    return (f() * -10.0) as i32;
}`}, // 45
	// METHOD-call body — irt_func_ret only sees receiver-less functions, so this
	// needed a receiver-keyed sibling (irt_method_ret).
	{"lambda_method_call", `struct P { x: f64 }
function (p: P) scaled(): f64 { return p.x * 2.0; }
function main(): i32 {
    var p: P = P { x: 2.25 };
    var f: () => f64 = () => p.scaled();
    return (f() * 10.0) as i32;
}`}, // 45
	// Slice-then-index body — pins the `[T]` slice spelling round-tripping
	// through both the slice arm and the index arm.
	{"lambda_slice_index", `function main(): i32 {
    var a: f64[] = [1.5, 2.5, 4.5];
    var f: () => f64 = () => a[0:2][1];
    return (f() * 10.0) as i32;
}`}, // 25
	// Regression guard: a binary body already delegated to its left operand.
	{"lambda_binary_index", `function main(): i32 {
    var a: f64[] = [1.5, 2.25];
    var f: () => f64 = () => a[1] * 2.0;
    return (f() * 10.0) as i32;
}`}, // 45
	// A BUILTIN-method body, which inference cannot answer without absorbing the
	// whole map/array/string surface. Resolved from the binding's DECLARED return
	// type instead — the annotation outranking the inference.
	{"lambda_builtin_map_get_or", `import "core/map";
function main(): i32 {
    var m: Map[string, f64] = map_new(4);
    m = m.insert("k", 4.5);
    var f: () => f64 = () => m.get_or("k", 0.0);
    return (f() * 10.0) as i32;
}`}, // 45
	// The declared type must not override an EXPLICIT lambda annotation, and an
	// i32 declared return must stay i32 — the stamp only ever fills a hole.
	{"lambda_declared_i32_narrow", `function main(): i32 {
    var a: i32[] = [1, 40];
    var f: () => i32 = () => a[1];
    return f() + 2;
}`}, // 42
}

// TestSelfHostLambdaRetInferIR_X86_64 pins irt_guess's ExprIndex /
// ExprFieldAccess arms through the self-host x86-64 IR path.
func TestSelfHostLambdaRetInferIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, interpBin := annotateF64ProjDir(t)
	_, runner := x86_64Tooling(t)

	for _, tc := range lambdaRetInferCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}

			route, derr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot, "-decide").Output()
			if derr != nil {
				t.Fatalf("route decide: %v", derr)
			}
			if got := strings.TrimSpace(string(route)); got != "ir" {
				t.Fatalf("%s routed %q, want \"ir\"", tc.name, got)
			}

			asm, cerr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot).Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "lamret_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
