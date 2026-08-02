package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// trailingCommaCases cover a trailing comma before the closing brace of
// a struct literal — `P { a: 1, b: 2, }`. std/test's `TestRunner`
// literals use this form throughout; without it the self-host parser
// bailed mid-literal and cascaded into a run of ExprUnknown nodes,
// which was the sole parse gap blocking std/test / std/fuzz / std/tcp
// from parsing cleanly. Exit codes cross-checked vs the Go backend.
var trailingCommaCases = []struct {
	name string
	src  string
	exit int
}{
	{"two-field", "struct P { a: i32, b: i32 } function main(): i32 { var p = P { a: 40, b: 2, }; return p.a + p.b; }", 42},
	{"one-field", "struct Q { v: i32 } function main(): i32 { var q = Q { v: 42, }; return q.v; }", 42},
	{"no-trailing-still-works", "struct R { a: i32, b: i32 } function main(): i32 { var r = R { a: 40, b: 2 }; return r.a + r.b; }", 42},
	{"string-field-trailing", "struct S { name: string, n: i32 } function main(): i32 { var s = S { name: \"hi\", n: 40, }; return s.name.len() + s.n; }", 42},
}

// TestSelfHostTrailingCommaX86_64 — trailing comma in struct literals,
// self-hosted x86-64 compiler.
func TestSelfHostTrailingCommaX86_64(t *testing.T) {
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

	for _, tc := range trailingCommaCases {
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

// TestSelfHostTrailingCommaArm64 — CI-gated arm64 counterpart. The fix
// is in the shared parser, so the arm64 emitter needed no change; this
// guards the shared path on arm64.
func TestSelfHostTrailingCommaArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range trailingCommaCases {
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
