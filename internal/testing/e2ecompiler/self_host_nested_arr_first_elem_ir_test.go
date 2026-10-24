package e2ecompiler

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func TestSelfHostNestedArrFirstElemIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"f64-call-first",
			`function mk(): f64[] { return [1.5, 2.5]; }
function main(): i32 { let m = [mk(), [0.25]]; let s = m[0][0] + m[0][1] + m[1][0]; if (s > 4.24 && s < 4.26) { return 42; } return 7; }`, 42},
		{"f64-ident-first",
			`function mk(): f64[] { return [1.5, 2.5]; }
function main(): i32 { let inner = mk(); let m = [inner, [0.25]]; let s = m[0][0] + m[0][1] + m[1][0]; if (s > 4.24 && s < 4.26) { return 42; } return 7; }`, 42},
		{"string-call-first",
			`function mk(): string[] { return ["ab", "cde"]; }
function main(): i32 { let m = [mk(), ["f"]]; let s = m[0][0].len() + m[0][1].len() + m[1][0].len(); if (s == 6) { return 42; } return 7; }`, 42},
		{"i64-call-first",
			`function mk(): i64[] { return [5000000000, 2]; }
function main(): i32 { let m = [mk(), [1 as i64]]; if (m[0][0] + m[0][1] + m[1][0] == 5000000003) { return 42; } return 7; }`, 42},
		{"f64-literal-first",
			`function main(): i32 { let m = [[1.5, 2.5], [0.25]]; let s = m[0][0] + m[0][1] + m[1][0]; if (s > 4.24 && s < 4.26) { return 42; } return 7; }`, 42},
		{"f64-unannotated-ret",
			`function mk2() { return [1.5, 2.5]; }
function main(): i32 { let a = mk2(); let s = a[0] + a[1]; if (s > 3.99 && s < 4.01) { return 42; } return 7; }`, 42},
		{"string-unannotated-ret",
			`function mk3() { return ["ab", "cde"]; }
function main(): i32 { let a = mk3(); if (a[0].len() + a[1].len() == 5) { return 42; } return 7; }`, 42},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("%s: driver failed: %v", tc.name, err)
			}
			if !strings.Contains(string(asm), ".Lssa_") {
				t.Fatalf("%s: did not lower through the IR (no .Lssa_ labels)", tc.name)
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var run *exec.Cmd
			if len(runner) == 0 {
				run = exec.Command(bin)
			} else {
				run = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
