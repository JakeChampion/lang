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
// The syscall leaves that have reached arm64 as Fern runtime functions:
// random_bytes over the __syscall3 sub-floor, the fs leaves (read_file /
// write_file / remove_file / temp_dir) over __syscall3 + __syscall4, and env
// over __raw_environ — each lowered through the IR pipeline by
// asm_arm64_ir.emit_ir_runtime_fern_fn. The register-ABI hand-asm they replace —
// including two ~30-line getrandom / getentropy bodies forked on `darwin` — is
// gone.
//
// Behaviour is covered under qemu by TestSelfHostAsmIRArm64Path/random-bytes-*
// and TestSelfHostIoErrorIRArm64. This test is the emission lock-in and runs on
// every x86 lane: the emitted aarch64 asm must define each Fern-compiled symbol,
// must NOT define the hand-asm one, and must contain the __syscall3 op's number
// load — the same instruction darwinize keys its Mach-O rewrite off.
func TestSelfHostRuntimeHelperSyscallLeavesAreFernArm64IR(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("needs a native x86 host to run the aarch64-emitting driver")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	mmc := buildSelfHostBin(t, x86gcc, dir, "asm_load_run.fern", "mmc_arm64_rb")

	// One program touching every migrated leaf, so a single emit covers them all.
	prog := "function main(): i32 {\n" +
		"    var b: string = random_bytes(8);\n" +
		"    match (write_file(\"/tmp/fern_lockin.txt\", \"x\")) { Ok(_) => {}, Err(_) => { return 1; } }\n" +
		"    match (read_file(\"/tmp/fern_lockin.txt\")) { Ok(_) => {}, Err(_) => { return 2; } }\n" +
		"    match (remove_file(\"/tmp/fern_lockin.txt\")) { Ok(_) => {}, Err(_) => { return 3; } }\n" +
		"    match (temp_dir(\"lockin\")) { Ok(_) => {}, Err(_) => { return 4; } }\n" +
		"    match (env(\"PATH\")) { Some(_) => {}, None => {} }\n" +
		"    return b.len();\n" +
		"}\n"
	srcFile := filepath.Join(t.TempDir(), "rb_ir.fern")
	if err := os.WriteFile(srcFile, []byte(prog), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}

	out, err := exec.Command(mmc, srcFile, "-target", "arm64").Output()
	if err != nil {
		t.Fatalf("self-host arm64 emit failed: %v", err)
	}
	asm := string(out)

	for _, leaf := range []string{"random_bytes", "read_file", "write_file", "remove_file", "temp_dir", "env"} {
		if !strings.Contains(asm, "__fn___fern_"+leaf+":") {
			t.Errorf("__fn___fern_%s not defined — the Fern helper did not lower", leaf)
		}
		if !strings.Contains(asm, "bl __fn___fern_"+leaf) {
			t.Errorf("op_%s does not call the stack-ABI Fern symbol", leaf)
		}
		if strings.Contains(asm, "\n__fern_"+leaf+":") {
			t.Errorf("the register-ABI hand-asm __fern_%s is back", leaf)
		}
	}
	// The fs leaves call a Fern __fern_io_error bundled with them, rather than
	// inlining the five-way errno classification — the "dependencies are the
	// call graph" shape #2649 is aiming at. The register-ABI hand-asm sibling
	// still exists for stat / read_dir / remove_dir_all / temp_dir, so assert
	// the Fern one specifically.
	if !strings.Contains(asm, "bl __fn___fern_io_error") {
		t.Error("the migrated fs leaves do not call the bundled Fern __fern_io_error")
	}
	// env has no syscall at all: it reads __fern_envp through the __raw_environ
	// op. The .bss slot must still be emitted — _start's save is gated on `heap`,
	// not on `env`, so a heap program that never calls env() stores here too.
	if !strings.Contains(asm, "__fern_envp: .quad 0") {
		t.Error("the __fern_envp .bss slot went with the hand-asm __fern_env")
	}
	if !strings.Contains(asm, "adrp x0, __fern_envp\n    add x0, x0, :lo12:__fern_envp\n    ldr x0, [x0]\n") {
		t.Error("__raw_environ did not emit the arm64 envp load")
	}
	// write_file's O_CREAT mode arg makes it the __syscall4 user; its number
	// load rides the same darwinize marker as __syscall3's.
	if !strings.Contains(asm, "    ldr x3, [sp], #16\n    ldr x2, [sp], #16\n    ldr x1, [sp], #16\n    ldr x0, [sp], #16\n    ldr x8, [sp], #16\n    svc #0\n") {
		t.Error("__syscall4 did not emit the arm64 5-pop + svc sequence")
	}
	// The __syscall3 op's number load. darwinize rewrites exactly this line to
	// `ldr x16, ...` and flips the following trap, so a change to the operand
	// order here silently breaks the Mach-O path — pin the instruction.
	if !strings.Contains(asm, "    ldr x8, [sp], #16\n    svc #0\n") {
		t.Error("__syscall3 did not emit the `ldr x8` + `svc #0` sequence darwinize matches")
	}
}

// TestSelfHostSyscallLeavesDarwinizedArm64 pins the Mach-O half of the same
// migration. darwinize cannot remap a syscall number it only sees at runtime
// (its rule needs a literal `mov x8, #N`), so the generic __syscall3 /
// __syscall4 ops instead carry the target's number in the Fern source, and
// darwinize rewrites only the number register and the trap. Without that rule
// the Mach-O binary would issue a Linux `svc #0` with a Linux number.
//
// It also pins the sticky-`pend_sys` fix: darwinize used to reset its pending
// syscall on EVERY line, so it only rewrote an `svc` that came IMMEDIATELY
// after the number load. The two abort paths load their exit status in between,
// which left exit(125) / exit(134) trapping through the Linux vector on XNU.
func TestSelfHostSyscallLeavesDarwinizedArm64(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("needs a native x86 host to run the aarch64-emitting driver")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	mmc := buildSelfHostBin(t, x86gcc, dir, "asm_load_run.fern", "mmc_darwin_rb")

	prog := "function main(): i32 {\n" +
		"    var b: string = random_bytes(8);\n" +
		"    match (write_file(\"/tmp/fern_lockin_d.txt\", \"x\")) { Ok(_) => {}, Err(_) => { return 1; } }\n" +
		"    match (read_file(\"/tmp/fern_lockin_d.txt\")) { Ok(_) => {}, Err(_) => { return 2; } }\n" +
		"    match (remove_file(\"/tmp/fern_lockin_d.txt\")) { Ok(_) => {}, Err(_) => { return 3; } }\n" +
		"    match (temp_dir(\"lockin\")) { Ok(_) => {}, Err(_) => { return 4; } }\n" +
		"    return b.len();\n" +
		"}\n"
	srcFile := filepath.Join(t.TempDir(), "rb_darwin.fern")
	if err := os.WriteFile(srcFile, []byte(prog), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}

	out, err := exec.Command(mmc, srcFile, "-target", "arm64-darwin").Output()
	if err != nil {
		t.Fatalf("self-host arm64-darwin emit failed: %v", err)
	}
	asm := string(out)

	// Each leaf's Darwin number / flag-set, straight out of asmcore.sysno /
	// oflag. These reach the syscall as an operand, so a wrong entry cannot be
	// caught anywhere downstream — the Linux number would just be issued
	// against the BSD vector (openat 56 is Darwin's `chdir`).
	for _, c := range []struct {
		what string
		imm  string
	}{
		{"getentropy", "500"},
		{"openat", "463"},
		{"unlinkat", "472"},
		{"O_WRONLY|O_CREAT|O_TRUNC", "1537"},
		{"lseek", "199"},
		{"mkdirat", "475"},
	} {
		if !strings.Contains(asm, "mov x0, #"+c.imm+"\n") {
			t.Errorf("the Darwin %s constant (%s) was not baked into the helper source", c.what, c.imm)
		}
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
	// The abort paths: `mov x8, #93` (exit) -> `mov x16, #1`, then the status
	// load, THEN the trap. Only a sticky pending-syscall survives the line in
	// between, and exit needs no errno normalisation.
	for _, status := range []string{"125", "134"} {
		if !strings.Contains(asm, "    mov x16, #1\n    mov x0, #"+status+"\n    svc #0x80\n") {
			t.Errorf("the exit(%s) abort path still traps through the Linux vector on Mach-O", status)
		}
	}
}
