package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// functionalUpdateCases exercise the immutable-update idiom on the
// self-hosted compiler: a struct value is "updated" by building a
// fresh one with a struct-update literal (`T { ...old, field: v }`)
// and rebinding, and "mutate through a function call" becomes
// return-the-new-value + rebind at the call site. These are the
// functional rewrites of the field-assignment programs the language
// used to allow before field mutation was banned (E048) — the
// self-host compiler supports struct-update end-to-end (parse + emit
// + run), so it compiles modern Fern. Exit codes match the originals.
var functionalUpdateCases = []struct {
	name string
	src  string
	exit int
}{
	// Return-the-new-value instead of mutate-through-call: bump
	// returns a fresh Box and the caller rebinds.
	{"bump-through-fn", "struct Box { v: i32 } function bump(b: Box): Box { return Box { ...b, v: b.v + 1 }; } function main(): i32 { var b: Box = Box { v: 10 }; b = bump(b); b = bump(b); return b.v; }", 12},
	// Two struct-updates, each overriding one field.
	{"two-fields", "struct P { x: i32, y: i32 } function main(): i32 { var p: P = P { x: 1, y: 2 }; p = P { ...p, x: 10 }; p = P { ...p, y: 30 }; return p.x + p.y; }", 40},
	// Update-in-loop: inc returns the new value, the loop rebinds.
	{"update-in-loop", "struct C { n: i32 } function inc(c: C): C { return C { ...c, n: c.n + 1 }; } function main(): i32 { var c: C = C { n: 0 }; var i: i32 = 0; while (i < 5) { c = inc(c); i = i + 1; } return c.n; }", 5},
	// The functional form of a compound field assign (`a.v += 35`).
	{"compound-via-update", "struct A { v: i32 } function main(): i32 { var a: A = A { v: 7 }; a = A { ...a, v: a.v + 35 }; return a.v; }", 42},
}

// TestSelfHostFunctionalUpdateX86_64 compiles the immutable-update
// programs with the self-hosted x86-64 compiler and checks exit codes.
func TestSelfHostFunctionalUpdateX86_64(t *testing.T) {
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

	for _, tc := range functionalUpdateCases {
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

// TestSelfHostFunctionalUpdateArm64 — CI-gated arm64 counterpart.
func TestSelfHostFunctionalUpdateArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range functionalUpdateCases {
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
