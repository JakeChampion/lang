package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// recursiveLocalCases cover self-recursive local functions, which the
// self-host desugars to `var f = lambda` and then lifts to a top-level
// function in hoist_local_funcs_module when the body captures nothing
// from its enclosing scope (recursion resolves once it's top-level).
// Covers direct self-recursion, tree recursion, and a recursive local
// that calls a top-level function. Exit codes cross-checked vs the Go
// backend (which supports recursive locals natively).
var recursiveLocalCases = []struct {
	name string
	src  string
	exit int
}{
	{"factorial", "function main(): i32 { function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } return fact(5); }", 120},
	{"fib", "function main(): i32 { function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } return fib(10); }", 55},
	{"calls-toplevel", "function dbl(x: i32): i32 { return x * 2; } function main(): i32 { function sumto(n: i32): i32 { if (n <= 0) { return 0; } return dbl(1) + sumto(n - 1); } return sumto(5); }", 10},
	// Statements *around* the hoisted recursive local must survive the
	// rebuild (regression: the lift dropped non-lifted `var`s that shared
	// the body). A plain var before it, and a capturing closure alongside.
	{"var-before", "function main(): i32 { var x: i32 = 5; function r(n: i32): i32 { if (n <= 0) { return 0; } return r(n - 1); } return x + r(3); }", 5},
	{"with-sibling-closure", "function main(): i32 { var base: i32 = 100; var add = function(x: i32): i32 { return x + base; }; function cd(n: i32): i32 { if (n <= 0) { return 0; } return 1 + cd(n - 1); } return add(cd(5) + 17); }", 122},
	// Capturing recursive locals: lambda-lifted with the captured enclosing
	// names threaded through as trailing params + at every call site.
	{"capture-one", "function main(): i32 { var base: i32 = 10; function f(n: i32): i32 { if (n <= 0) { return base; } return 1 + f(n - 1); } return f(3); }", 13},
	{"capture-two", "function main(): i32 { var acc: i32 = 0; var step: i32 = 2; function go(n: i32): i32 { if (n <= 0) { return acc; } return step + go(n - 1); } return go(4); }", 8},
	{"capture-2calls", "function main(): i32 { var base: i32 = 100; function f(n: i32): i32 { if (n <= 0) { return base; } return 1 + f(n - 1); } return f(2) + f(3); }", 205},
	{"capture-inferred", "function main(): i32 { var base = 7; function f(n: i32): i32 { if (n <= 0) { return base; } return 1 + f(n - 1); } return f(3); }", 10},
}

// TestSelfHostRecursiveLocalX86_64 — recursive local hoisting via the
// self-hosted x86-64 compiler.
func TestSelfHostRecursiveLocalX86_64(t *testing.T) {
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

	for _, tc := range recursiveLocalCases {
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

// TestSelfHostRecursiveLocalArm64 — CI-gated arm64 counterpart.
func TestSelfHostRecursiveLocalArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range recursiveLocalCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64")
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
