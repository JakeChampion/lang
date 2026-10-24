package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var annotateIndexCases = []struct {
	name string
	src  string
}{
	{"if_expr_index_f64", `function main(): i32 {
    let c: boolean = true;
    let v: f64 = (if (c) { [1.5, 2.5] } else { [3.5, 4.5] })[1];
    return (v * 10.0) as i32;
}`},
	{"if_expr_index_i32", `function main(): i32 {
    let c: boolean = false;
    let v: i32 = (if (c) { [1, 2] } else { [30, 40] })[1];
    return v + 2;
}`},
	{"f64_local_index", `function main(): i32 {
    let xs: f64[] = [1.5, 2.5, 8.25];
    return (xs[2] * 4.0) as i32;
}`},
	{"call_struct_field_f64_index", `struct Box { data: f64[] }
function mk(): Box { return Box { data: [1.5, 2.5, 4.5] }; }
function main(): i32 { return (mk().data[2] * 10.0) as i32; }`},
	{"call_struct_field_i64_index", `struct Box { data: i64[] }
function mk(): Box { return Box { data: [7000000000, 9000000000] }; }
function main(): i32 { return (mk().data[1] / 1000000000) as i32; }`},
}

func TestSelfHostAnnotateIndexIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)

	for _, tc := range annotateIndexCases {
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
			progBin := buildBin(t, gcc, dir, "annidx_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (IR annotate path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
