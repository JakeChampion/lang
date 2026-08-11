package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// higherOrderCases cover function types `(T) => R` and higher-order
// functions: a function passed by name as a value (lowered to a 1-slot
// closure box `[&__fn_name]`), called through the closure-call path. A
// real capturing closure (the `function(…)` lambda form) passed as an
// argument exercises the same box-in-register convention. Exit codes
// cross-checked vs the Go backend.
const hoDblHelper = "function dbl(x: i32): i32 { return x * 2; } "

var higherOrderCases = []struct {
	name string
	src  string
	exit int
}{
	{"apply-fn-value", hoDblHelper + "function apply(f: (i32) => i32, x: i32): i32 { return f(x); } function main(): i32 { return apply(dbl, 21); }", 42},
	{"multi-arg", hoDblHelper + "function combine(f: (i32) => i32, a: i32, b: i32): i32 { return f(a) + f(b); } function main(): i32 { return combine(dbl, 13, 8); }", 42},
	{"closure-arg", "function apply(f: (i32) => i32, x: i32): i32 { return f(x); } function main(): i32 { var n: i32 = 7; var g = function (x: i32): i32 { return x + n; }; return apply(g, 35); }", 42},
	{"twice", hoDblHelper + "function twice(f: (i32) => i32, x: i32): i32 { return f(f(x)); } function main(): i32 { return twice(dbl, 10) + 2; }", 42},
}

// TestSelfHostHigherOrderX86_64 — function values + higher-order
// functions with the self-hosted x86-64 compiler.
func TestSelfHostHigherOrderX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range higherOrderCases {
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
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostHigherOrderArm64 — CI-gated arm64 counterpart.
func TestSelfHostHigherOrderArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range higherOrderCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
