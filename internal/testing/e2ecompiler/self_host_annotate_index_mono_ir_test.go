package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var annotateIndexMonoCases = []struct {
	name string
	src  string
}{
	{"generic_if_expr_index_f64", `function pick[T](flag: boolean, t: T): f64 {
    return (if (flag) { [1.5, 2.5] } else { [3.5, 4.5] })[1];
}
function main(): i32 {
    return (pick(true, 7) * 10.0) as i32;
}`},
	{"generic_call_struct_field_index", `struct Box { data: f64[] }
function mk(): Box { return Box { data: [1.5, 2.5, 4.5] }; }
function grab[T](t: T): f64 { return mk().data[2]; }
function main(): i32 { return (grab(3) * 10.0) as i32; }`},
	{"generic_if_expr_index_i32", `function idx[T](flag: boolean, t: T): i32 {
    return (if (flag) { [1, 2] } else { [30, 40] })[1];
}
function main(): i32 { return idx(false, 9) + 2; }`},
}

func TestSelfHostAnnotateIndexMonoIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)

	for _, tc := range annotateIndexMonoCases {
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
			progBin := buildBin(t, gcc, dir, "annidxmono_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (IR annotate path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
