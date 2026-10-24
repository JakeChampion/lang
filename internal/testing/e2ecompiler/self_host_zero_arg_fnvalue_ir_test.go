package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostZeroArgFnValueIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		{"i32_return", `function mk(): i32 { return 9; }
function main(): i32 { let f = mk; return f(); }`, 9},
		{"struct_return", `struct P { x: i32 }
function mk(): P { return P { x: 9 }; }
function main(): i32 { let f = mk; return f().x; }`, 9},
		{"called_twice", `function five(): i32 { return 5; }
function main(): i32 { let f = five; return f() + f(); }`, 10},
		{"loop_call", `function one(): i32 { return 1; }
function main(): i32 { let f = one; let s = 0; let i = 0; while (i < 4) { s = s + f(); i = i + 1; } return s; }`, 4},
		{"arg_fn", `function dbl(n: i32): i32 { return n * 2; }
function main(): i32 { let f = dbl; return f(7); }`, 14},
		{"const_as_value", `const TEN: i32 = 10;
function main(): i32 { let f = TEN; return f + 5; }`, 15},
		{"array_index_call", `function mk(): i32 { return 7; }
function main(): i32 { let fns = [mk]; return fns[0](); }`, 7},
		{"array_two_fns", `function a(): i32 { return 3; }
function b(): i32 { return 4; }
function main(): i32 { let fns = [a, b]; return fns[0]() + fns[1](); }`, 7},
		{"array_loop_call", `function one(): i32 { return 1; }
function main(): i32 { let fns = [one]; let s = 0; let i = 0; while (i < 5) { s = s + fns[0](); i = i + 1; } return s; }`, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("driver produced no asm")
			}
			progBin := buildBin(t, gcc, dir, "zfv_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("zero-arg fn-value %q exit %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
