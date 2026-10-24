package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var annotateTupleIndexCases = []struct {
	name string
	src  string
}{
	{"tuple_elem_call_array_f64", `function mk(): (f64, i32)[] { return [(1.5, 7), (2.5, 8)]; }
function main(): i32 { return (mk()[1].0 * 10.0) as i32 + mk()[1].1; }`},
	{"tuple_elem_call_array_i64", `function mk(): (i64, i32)[] { return [(7000000000, 7), (9000000000, 8)]; }
function main(): i32 { return (mk()[1].0 / 1000000000) as i32 + mk()[1].1; }`},
	{"tuple_elem_call_array_i32_narrow", `function mk(): (i32, i32)[] { return [(1, 2), (30, 12)]; }
function main(): i32 { return mk()[1].0 + mk()[1].1; }`},
	{"tuple_elem_local_array_f64", `function main(): i32 {
    let a: (f64, i32)[] = [(1.5, 7), (2.5, 8)];
    return (a[1].0 * 10.0) as i32 + a[1].1;
}`},
}

func TestSelfHostAnnotateTupleIndexIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)

	for _, tc := range annotateTupleIndexCases {
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
			progBin := buildBin(t, gcc, dir, "anntupidx_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (IR annotate path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
