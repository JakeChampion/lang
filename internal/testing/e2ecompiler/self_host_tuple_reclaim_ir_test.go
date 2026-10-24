package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var tupleReclaimIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"loop-body-scalar-tuple",
		`function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let a: (i32, i32) = (i, 1); sum = sum + a.0 + a.1; i = i + 1; } return sum; }`,
		10},
	{"nested-if-scalar-tuple",
		`function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { if (i > 0) { let a: (i32, i32) = (i, 1); sum = sum + a.0 + a.1; } i = i + 1; } return sum; }`,
		9},
	{"i64-f64-scalar-tuple",
		`function main(): i32 { let sum: i64 = 0; let i: i32 = 0; while (i < 4) { let a: (i64, f64) = (5, 2.0); sum = sum + a.0 + (a.1 as i64); i = i + 1; } return sum as i32; }`,
		28},
	{"scalar-tuple-churn-safe",
		`function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 5000000) { let a: (i32, i32) = (i, 1); sum = (sum + a.0 + a.1) % 1000; i = i + 1; } return sum; }`,
		0},
	{"unannotated-scalar-tuple",
		`function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let a = (i, 1); sum = sum + a.0 + a.1; i = i + 1; } return sum; }`,
		10},
	{"unannotated-scalar-tuple-churn-safe",
		`function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 5000000) { let a = (i, 1); sum = (sum + a.0 + a.1) % 1000; i = i + 1; } return sum; }`,
		0},
}

func TestSelfHostTupleReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range tupleReclaimIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
