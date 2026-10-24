package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var tupleReuseIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"reuse-fires",
		`function main(): i32 { let a: (i32, i32) = (5, 7); let s: i32 = a.0 + a.1; let b: (i32, i32) = (2, 3); return s + b.0 + b.1; }`,
		17},
	{"donor-live-no-reuse",
		`function main(): i32 { let a: (i32, i32) = (5, 7); let b: (i32, i32) = (2, 3); return a.0 + a.1 + b.0 + b.1; }`,
		17},
	{"arity-mismatch-no-reuse",
		`function main(): i32 { let a: (i32, i32) = (5, 7); let s: i32 = a.0 + a.1; let b: (i32, i32, i32) = (1, 2, 3); return s + b.0 + b.1 + b.2; }`,
		18},
	{"reuse-i64",
		`function main(): i32 { let a: (i64, i64) = (5, 7); let s: i64 = a.0 + a.1; let b: (i64, i64) = (30, 20); return (s + b.0 + b.1) as i32; }`,
		62},
	{"reuse-f64",
		`function main(): i32 { let a: (f64, f64) = (1.5, 2.5); let s: f64 = a.0 + a.1; let b: (f64, f64) = (10.0, 3.0); return (s + b.0 + b.1) as i32; }`,
		17},
	{"reuse-string",
		`function main(): i32 { let a: (string, i32) = ("hi", 5); let s: i32 = a.1; let b: (string, i32) = ("yo", 9); return s + b.1 + b.0.len(); }`,
		16},
	{"reuse-struct-elem",
		`struct P { x: i32, y: i32 } function main(): i32 { let k: i32 = 1; let a: (P, i32) = (P { x: 1 * k, y: 2 }, 5); let s: i32 = a.1; let b: (P, i32) = (P { x: 10 * k, y: 20 }, 9); return s + b.0.x + b.0.y + b.1; }`,
		44},
	{"loop-body-reuse-fires",
		`function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let a: (i32, i32) = (i, i + 1); let s: i32 = a.0 + a.1; let b: (i32, i32) = (i, i * 2); sum = sum + s + b.0 + b.1; i = i + 1; } return sum; }`,
		34},
}

func TestSelfHostTupleReuseIRX86_64(t *testing.T) {
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

	for _, tc := range tupleReuseIRCases {
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
