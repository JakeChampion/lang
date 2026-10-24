package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var lambdaRetInferCases = []struct {
	name string
	src  string
}{
	{"lambda_index_captured_f64_array", `function main(): i32 {
    let a: f64[] = [1.5, 4.5];
    let f: () => f64 = () => a[1];
    return (f() * 10.0) as i32;
}`},
	{"lambda_index_call_f64_array", `function mk(): f64[] { return [1.5, 4.5]; }
function main(): i32 {
    let f: () => f64 = () => mk()[1];
    return (f() * 10.0) as i32;
}`},
	{"lambda_tuple_elem_captured", `function mk(): (f64, i32) { return (4.5, 7); }
function main(): i32 {
    let t: (f64, i32) = mk();
    let f: () => f64 = () => t.0;
    return (f() * 10.0) as i32 + t.1;
}`},
	{"lambda_literal_f64", `function main(): i32 {
    let f: () => f64 = () => 4.5;
    return (f() * 10.0) as i32;
}`},
	{"lambda_call_f64", `function mk(): f64 { return 4.5; }
function main(): i32 {
    let f: () => f64 = () => mk();
    return (f() * 10.0) as i32;
}`},
	{"lambda_index_i32_array_narrow", `function main(): i32 {
    let a: i32[] = [1, 40];
    let f: () => i32 = () => a[1];
    return f() + 2;
}`},
	{"lambda_index_string_array", `function main(): i32 {
    let a: string[] = ["ab", "cdef"];
    let f: () => string = () => a[1];
    return f().len() + 38;
}`},
	{"lambda_struct_field", `struct P { x: f64 }
function main(): i32 {
    let p: P = P { x: 4.5 };
    let f: () => f64 = () => p.x;
    return (f() * 10.0) as i32;
}`},
	{"lambda_unary_neg", `function main(): i32 {
    let a: f64[] = [1.5, 4.5];
    let f: () => f64 = () => -a[1];
    return (f() * -10.0) as i32;
}`},
	{"lambda_method_call", `struct P { x: f64 }
function (p: P) scaled(): f64 { return p.x * 2.0; }
function main(): i32 {
    let p: P = P { x: 2.25 };
    let f: () => f64 = () => p.scaled();
    return (f() * 10.0) as i32;
}`},
	{"lambda_slice_index", `function main(): i32 {
    let a: f64[] = [1.5, 2.5, 4.5];
    let f: () => f64 = () => a[0:2][1];
    return (f() * 10.0) as i32;
}`},
	{"lambda_binary_index", `function main(): i32 {
    let a: f64[] = [1.5, 2.25];
    let f: () => f64 = () => a[1] * 2.0;
    return (f() * 10.0) as i32;
}`},
	{"lambda_builtin_map_get_or", `import "core/map";
function main(): i32 {
    let m: Map[string, f64] = map_new(4);
    m = m.insert("k", 4.5);
    let f: () => f64 = () => m.get_or("k", 0.0);
    return (f() * 10.0) as i32;
}`},
	{"lambda_declared_i32_narrow", `function main(): i32 {
    let a: i32[] = [1, 40];
    let f: () => i32 = () => a[1];
    return f() + 2;
}`},
}

func TestSelfHostLambdaRetInferIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)

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
