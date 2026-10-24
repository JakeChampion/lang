package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var annotateSliceCases = []struct {
	name string
	src  string
}{
	{"slice_of_if_expr_f64", `function main(): i32 {
    let c: boolean = true;
    let xs: [f64] = (if (c) { [1.5, 2.5, 4.5] } else { [3.5, 4.5, 5.5] })[0:2];
    return (xs[1] * 10.0) as i32;
}`},
	{"slice_of_call_field_f64", `struct Box { data: f64[] }
function mk(): Box { return Box { data: [1.5, 2.5, 4.5] }; }
function main(): i32 {
    let xs: [f64] = mk().data[0:2];
    return (xs[1] * 10.0) as i32;
}`},
	{"slice_of_call_field_i64", `struct Box { data: i64[] }
function mk(): Box { return Box { data: [7000000000, 9000000000, 1] }; }
function main(): i32 {
    let xs: [i64] = mk().data[0:2];
    return (xs[1] / 1000000000) as i32;
}`},
	{"slice_of_if_expr_i32_narrow", `function main(): i32 {
    let c: boolean = true;
    let xs: [i32] = (if (c) { [10, 20, 30] } else { [40, 50, 60] })[0:2];
    return xs[1] + 22;
}`},
	{"slice_of_call_field_string", `struct Box { data: string[] }
function mk(): Box { return Box { data: ["ab", "cd", "ef"] }; }
function main(): i32 {
    let xs: [string] = mk().data[0:2];
    return xs[1].len() + 40;
}`},
	{"slice_of_local_f64", `function main(): i32 {
    let a: f64[] = [1.5, 2.5, 4.5];
    let xs: [f64] = a[0:2];
    return (xs[1] * 10.0) as i32;
}`},
	{"slice_of_nested_index_f64", `function main(): i32 {
    let m: f64[][] = [[1.5, 2.5, 4.5], [9.5, 8.5, 7.5]];
    let xs: [f64] = m[1][0:2];
    return (xs[1] * 10.0) as i32;
}`},
}

func TestSelfHostAnnotateSliceIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)

	for _, tc := range annotateSliceCases {
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
				t.Fatalf("%s routed %q, want \"ir\" (case no longer exercises the IR annotate path)", tc.name, got)
			}

			asm, cerr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot).Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "annslice_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (IR annotate path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
