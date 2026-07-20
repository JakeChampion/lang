package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// f64ToStringProgram formats a spread of f64 values one per line. The
// expected output matches std/float's __float_to_string (k=15, trailing
// zeros trimmed) exactly — including the IEEE noise digits — so the
// self-host's native __fern_f64_to_string helper is held to the same
// contract as the pure-Fern formatter the Go backend compiles. The last
// two lines pin the #5363 defaults on the self-host: a bare unsuffixed
// literal is f64 (1.0/3.0 renders at the 15-digit f64 precision, not
// f32's 7) and `float` is the f64 alias.
const f64ToStringProgram = `function main(): i32 {
    print((3.5 as f64).to_string());
    print((0.0 as f64 - 2.25).to_string());
    print((0.0 as f64).to_string());
    print((1.0 as f64).to_string());
    print((100.0 as f64).to_string());
    print((123456.789 as f64).to_string());
    print((0.1 as f64).to_string());
    print((0.5 as f64).to_string());
    print((9999999.99 as f64).to_string());
    print((0.0 as f64 - 0.000125).to_string());
    print((1.0 / 3.0).to_string());
    var fx: float = 1.0;
    print((fx / 3.0).to_string());
    return 0;
}`

const f64ToStringWant = "3.5\n" +
	"-2.25\n" +
	"0\n" +
	"1\n" +
	"100\n" +
	"123456.789000000004307\n" +
	"0.1\n" +
	"0.5\n" +
	"9999999.990000000223517\n" +
	"-0.000125\n" +
	"0.333333333333333\n" +
	"0.333333333333333\n"

// TestSelfHostF64ToStringX86_64 compiles the float-formatting program
// with the self-hosted x86-64 emitter and checks its stdout against the
// std/float-matching reference output.
func TestSelfHostF64ToStringX86_64(t *testing.T) {
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

	asm := runCapture(t, gcc, runner, driverBin, []byte(f64ToStringProgram))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	progBin := buildBin(t, gcc, dir, "f64prog", string(asm))

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), progBin)...)
	}
	out, _ := cmd.Output()
	if string(out) != f64ToStringWant {
		t.Errorf("f64.to_string output mismatch:\n got: %q\nwant: %q", string(out), f64ToStringWant)
	}
}

// TestSelfHostF64ToStringArm64 is the ARM64 counterpart: same program
// through the self-hosted ARM64 emitter, run under qemu-aarch64.
// CI-gated; skips cleanly without the cross toolchain.
func TestSelfHostF64ToStringArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "asm.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	prog, _, err := modload.Load(filepath.Join(dir, "asm_ir_run.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	driverBin := buildBin(t, x86gcc, dir, "driver", asm)

	f64Asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(f64ToStringProgram), "-target", "arm64")
	if len(f64Asm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes for the f64 program")
	}
	f64Bin := buildBin(t, arm64gcc, dir, "f64prog", string(f64Asm))

	cmd := runArm64Bin(qemu, f64Bin)
	out, _ := cmd.Output()
	if string(out) != f64ToStringWant {
		t.Errorf("f64.to_string (arm64) output mismatch:\n got: %q\nwant: %q", string(out), f64ToStringWant)
	}
}
