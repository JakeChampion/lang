package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var strAccumIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"accum-basic",
		`function main(): i32 { let s: string = ""; let i: i32 = 0; while (i < 4) { s = s + "x"; i = i + 1; } return s.len(); }`,
		4},
	{"accum-init-nonempty",
		`function main(): i32 { let s: string = "a"; let i: i32 = 0; while (i < 3) { s = s + "bc"; i = i + 1; } return s.len(); }`,
		7},
	{"accum-invariant-operand",
		`function main(): i32 { let x: string = "yy"; let s: string = ""; let i: i32 = 0; while (i < 3) { s = s + x; i = i + 1; } return s.len(); }`,
		6},
	{"accum-churn-safe",
		`function main(): i32 { let x: string = "yy"; let s: string = ""; let i: i32 = 0; while (i < 5000000) { s = s + x; if (s.len() > 40) { s = string_from_bytes_unchecked([65 as u8]); } i = i + 1; } return 0; }`,
		0},
	{"accum-unannotated",
		`function main(): i32 { let s = ""; let i: i32 = 0; while (i < 4) { s = s + "x"; i = i + 1; } return s.len(); }`,
		4},
	{"accum-unannotated-return",
		`function build(n: i32): string { let s = ""; let i: i32 = 0; while (i < n) { s = s + "ab"; i = i + 1; } return s; } function main(): i32 { return build(3).len(); }`,
		6},
	{"accum-int-not-reclaimed",
		`function main(): i32 { let n: i32 = 0; let i: i32 = 0; while (i < 5) { n = n + i; i = i + 1; } return n; }`,
		10},
	{"accum-return-builder",
		`function build(n: i32): string { let s: string = ""; let i: i32 = 0; while (i < n) { s = s + "x"; i = i + 1; } return s; } function main(): i32 { return build(5).len(); }`,
		5},
	{"accum-return-branchy",
		`function build(n: i32): string { let s: string = "start"; let i: i32 = 0; while (i < n) { s = s + "z"; if (s.len() > 8) { return s; } i = i + 1; } return s; } function main(): i32 { return build(100).len(); }`,
		9},
	{"accum-nonfresh-reassign-not-reclaimed",
		`function acc(x: string): i32 { let s: string = ""; let i: i32 = 0; while (i < 3) { s = s + x; i = i + 1; } s = "reset"; return s.len() + x.len(); } function main(): i32 { return acc("x"); }`,
		6},
}

func TestSelfHostStrAccumIRX86_64(t *testing.T) {
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

	for _, tc := range strAccumIRCases {
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
