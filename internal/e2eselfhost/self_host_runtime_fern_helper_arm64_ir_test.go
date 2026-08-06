package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #2649 — the arm64 sibling of TestSelfHostRuntimeHelperStrToI32IsFernIR.
//
// random_bytes is the first SYSCALL leaf to reach arm64 as a Fern runtime
// function: asmcore.rt_src_random_bytes, lowered through the IR pipeline by
// asm_arm64_ir.emit_ir_runtime_fern_fn over the __syscall3 sub-floor (which this
// slice added to the arm64 op emitter). The register-ABI hand-asm it replaces —
// two ~30-line getrandom / getentropy bodies forked on `darwin` — is gone.
//
// Behaviour is covered by TestSelfHostAsmIRArm64Path/random-bytes-* (under
// qemu). This test is the emission lock-in and runs on every x86 lane: the
// emitted aarch64 asm must define the Fern-compiled symbol, must NOT define the
// hand-asm one, and must contain the __syscall3 op's number load — the same
// instruction darwinize keys its Mach-O rewrite off.
func TestSelfHostRuntimeHelperRandomBytesIsFernArm64IR(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("needs a native x86 host to run the aarch64-emitting driver")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	mmc := buildSelfHostBin(t, x86gcc, dir, "asm_load_run.fern", "mmc_arm64_rb")

	prog := "function main(): i32 { var b: string = random_bytes(8); return b.len(); }\n"
	srcFile := filepath.Join(t.TempDir(), "rb_ir.fern")
	if err := os.WriteFile(srcFile, []byte(prog), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}

	out, err := exec.Command(mmc, srcFile, "-target", "arm64").Output()
	if err != nil {
		t.Fatalf("self-host arm64 emit failed: %v", err)
	}
	asm := string(out)

	if !strings.Contains(asm, "__fn___fern_random_bytes:") {
		t.Error("__fn___fern_random_bytes not defined — the Fern helper did not lower")
	}
	if !strings.Contains(asm, "bl __fn___fern_random_bytes") {
		t.Error("op_random_bytes does not call the stack-ABI Fern symbol")
	}
	if strings.Contains(asm, "\n__fern_random_bytes:") {
		t.Error("the register-ABI hand-asm __fern_random_bytes is back")
	}
	// The __syscall3 op's number load. darwinize rewrites exactly this line to
	// `ldr x16, ...` and flips the following trap, so a change to the operand
	// order here silently breaks the Mach-O path — pin the instruction.
	if !strings.Contains(asm, "    ldr x8, [sp], #16\n    svc #0\n") {
		t.Error("__syscall3 did not emit the `ldr x8` + `svc #0` sequence darwinize matches")
	}
}

// TestSelfHostRandomBytesDarwinizedArm64 pins the Mach-O half of the same
// migration. darwinize cannot remap a syscall number it only sees at runtime
// (its rule needs a literal `mov x8, #N`), so the generic __syscall3 op instead
// carries the target's number in the Fern source — getentropy (BSD 500) here —
// and darwinize rewrites only the number register and the trap. Without that
// rule the Mach-O binary would issue a Linux `svc #0` with a Linux number.
func TestSelfHostRandomBytesDarwinizedArm64(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("needs a native x86 host to run the aarch64-emitting driver")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	mmc := buildSelfHostBin(t, x86gcc, dir, "asm_load_run.fern", "mmc_darwin_rb")

	prog := "function main(): i32 { var b: string = random_bytes(8); return b.len(); }\n"
	srcFile := filepath.Join(t.TempDir(), "rb_darwin.fern")
	if err := os.WriteFile(srcFile, []byte(prog), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}

	out, err := exec.Command(mmc, srcFile, "-target", "arm64-darwin").Output()
	if err != nil {
		t.Fatalf("self-host arm64-darwin emit failed: %v", err)
	}
	asm := string(out)

	if !strings.Contains(asm, "mov x0, #500") {
		t.Error("the Darwin getentropy number (500) was not baked into the helper source")
	}
	if strings.Contains(asm, "ldr x8, [sp], #16") {
		t.Error("darwinize left the __syscall3 number load on x8 (Linux form) in Mach-O output")
	}
	if !strings.Contains(asm, "    ldr x16, [sp], #16\n    svc #0x80\n") {
		t.Error("darwinize did not rewrite the __syscall3 sequence to the Darwin trap")
	}
	// The generic syscall is fallible, so darwinize must also normalise the
	// carry-flag errno back to Linux's -errno (what `if (r < 0)` in the helper
	// tests). Without it a Darwin failure reads as a positive byte count.
	if !strings.Contains(asm, "    svc #0x80\n    b.cc ") {
		t.Error("darwinize did not emit the errno normalisation after the generic syscall")
	}
}
