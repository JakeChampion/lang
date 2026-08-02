package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// closureCases exercise capturing closures (the `function(…)` lambda
// form), closures returned across a `(T) => R` return type, and a 0-arg
// function passed by name as a value (`() => R` param — distinguished
// from a const-call at the call site). All boxed `[fn_addr, caps…]` and
// invoked through the box-in-register convention. Cross-checked vs the
// Go backend.
var closureCases = []struct {
	name string
	src  string
	exit int
}{
	{"capture-local", "function main(): i32 { var n: i32 = 5; var f = function (x: i32): i32 { return x + n; }; return f(37); }", 42},
	{"multi-capture", "function main(): i32 { var a: i32 = 30; var b: i32 = 12; var f = function (): i32 { return a + b; }; return f(); }", 42},
	{"capture-string", "function main(): i32 { var s: string = \"hello\"; var f = function (): i32 { return s.len(); }; return f() + 37; }", 42},
	{"return-closure", "function adder(a: i32): (i32) => i32 { return function (b: i32): i32 { return a + b; }; } function main(): i32 { var add10 = adder(10); var add20 = adder(20); return add10(5) + add20(7); }", 42},
	{"zero-arg-fn-value", "function run(fn: () => i32): i32 { return fn(); } function work(): i32 { return 42; } function main(): i32 { return run(work); }", 42},
	{"predicate", "function count_if(arr: i32[], pred: (i32) => boolean): i32 { var c: i32 = 0; for x in arr { if (pred(x)) { c = c + 1; } } return c; } function is_big(n: i32): boolean { return n > 10; } function main(): i32 { var a: i32[] = [5, 20, 8, 30, 15]; return count_if(a, is_big) * 14; }", 42},
}

// TestSelfHostClosuresX86_64 — capturing closures + function values with
// the self-hosted x86-64 compiler.
func TestSelfHostClosuresX86_64(t *testing.T) {
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

	for _, tc := range closureCases {
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

// TestSelfHostClosuresArm64 — CI-gated arm64 counterpart.
func TestSelfHostClosuresArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range closureCases {
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
