package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tupleDestructureCases cover `var (a, b) = E` destructuring. The
// parser encodes the names as "a,b"; the emitter evaluates the tuple
// box and binds a = box.0, b = box.1. Exit codes cross-checked vs the
// Go backend.
var tupleDestructureCases = []struct {
	name string
	src  string
	exit int
}{
	{"from-fn-return", "function swap(a: i32, b: i32): (i32, i32) { return (b, a); } function main(): i32 { var (x, y) = swap(10, 32); return x + y; }", 42},
	{"from-local", "function main(): i32 { var t: (i32, i32) = (15, 27); var (a, b) = t; return a + b; }", 42},
	{"first-only", "function mk(): (i32, i32) { return (42, 7); } function main(): i32 { var (a, b) = mk(); return a; }", 42},
	{"second-only", "function mk(): (i32, i32) { return (7, 42); } function main(): i32 { var (a, b) = mk(); return b; }", 42},
	// `let (a, b) = E` binds the same way as `var (a, b)`. Before the
	// parser learned the `let` statement arm, these hung the self-host
	// compiler (no cursor progress on the `let` keyword → infinite
	// loop / SIGKILL). Same expected exit as the `var` forms above.
	{"let-from-fn-return", "function swap(a: i32, b: i32): (i32, i32) { return (b, a); } function main(): i32 { let (x, y) = swap(10, 32); return x + y; }", 42},
	{"let-from-local", "function main(): i32 { var t: (i32, i32) = (15, 27); let (a, b) = t; return a + b; }", 42},
}

// TestSelfHostTupleDestructureX86_64 — `var (a,b) = …` with the
// self-hosted x86-64 compiler.
func TestSelfHostTupleDestructureX86_64(t *testing.T) {
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

	for _, tc := range tupleDestructureCases {
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

// TestSelfHostTupleDestructureArm64 — CI-gated arm64 counterpart.
func TestSelfHostTupleDestructureArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleDestructureCases {
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
