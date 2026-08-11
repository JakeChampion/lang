package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// genericsCases cover user-defined generics — generic functions
// (`id[T]`, `fst[A, B]`) and generic structs (`Box[T]`, `Pair[A, B]`).
// The self-host handles them by type ERASURE rather than
// monomorphisation: its ABI is a uniform 8-byte stack slot for every
// value (i32, ptr, …), so one emitted body / field layout is correct
// for every instantiation. The parser consumes + discards the `[…]`
// type-parameter list on the declaration. Exit codes cross-checked vs
// the Go backend (which monomorphises; the observable result is the
// same).
var genericsCases = []struct {
	name string
	src  string
	exit int
}{
	// The two upstream parity cases (conformance/cases).
	{"generic-id", "function id[T](x: T): T { return x; } function main(): i32 { var a = id(42); var s = id(\"hi\"); if (s == \"hi\") { return a; } return 0; }", 42},
	{"generic-box", "struct Box[T] { value: T } function unbox[T](b: Box[T]): T { return b.value; } function main(): i32 { var b: Box[i32] = Box { value: 42 }; return unbox(b); }", 42},
	// Multiple type params on a function and a struct.
	{"two-type-params-fn", "function fst[A, B](a: A, b: B): A { return a; } function main(): i32 { return fst(42, 99); }", 42},
	{"two-type-params-struct", "struct Pair[A, B] { fst: A, snd: B } function main(): i32 { var p = Pair { fst: 40, snd: 2 }; return p.fst + p.snd; }", 42},
	// One generic body used at two concrete types in the same program.
	{"mixed-instantiation", "function id[T](x: T): T { return x; } function main(): i32 { var n: i32 = id(40); var s: string = id(\"hi\"); return n + s.len(); }", 42},
}

// TestSelfHostGenericsX86_64 — user generics (erasure) with the
// self-hosted x86-64 compiler.
func TestSelfHostGenericsX86_64(t *testing.T) {
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

	for _, tc := range genericsCases {
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

// TestSelfHostGenericsArm64 — CI-gated arm64 counterpart. The generics
// support lives entirely in the shared parser, so the arm64 emitter
// needed no change; this guards that the shared path stays sound on
// arm64.
func TestSelfHostGenericsArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range genericsCases {
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
