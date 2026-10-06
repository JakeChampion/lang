package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostArm64FrameGas byte-checks the frame prologue/epilogue ops
// added in slice 3h (stp/ldp pre/post, ldr/str writeback, and the `mov
// Xd, sp` add-immediate alias) as parsed by arm64_gas.fern. The expected
// bytes are pinned against llvm-mc. Run through the self-host wasm pipeline;
// exit 0 = all pass, else the 1-based failing check id.
func TestSelfHostArm64FrameGas(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64 frame gas e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64FrameGasSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64 frame gas self-test")
	}
	watPath := filepath.Join(dir, "arm64_frame_gas_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("arm64 frame gas self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

// TestSelfHostArm64DarwinMachOFnGasRuns proves the GAS assembler can now
// handle a real function shape — the prologue/epilogue + frame idiom the
// self-host arm64-darwin backend actually emits (stp/mov-sp/str-push/
// ldr-pop/ldp/ret), with a `bl` from `_main`. A Fern program assembles
// that text, wraps it with macho.fern, and the signed Mach-O exits 42 —
// no external tool.
func TestSelfHostArm64DarwinMachOFnGasRuns(t *testing.T) {
	assertMachORuns(t, machoRun{name: "fn42", main: arm64MachOFnGasDriverMain, wantExit: 42})
}

// arm64FrameGasSelfTestMain asserts the frame ops assemble to the
// llvm-mc-pinned bytes. Each `return N` is a distinct failing-check id.
const arm64FrameGasSelfTestMain = `
function main(): i32 {
    // stp x29, x30, [sp, #-16]! -> 0xA9BF7BFD -> FD 7B BF A9
    let a: Arm64Asm = arm64_gas_assemble("stp x29, x30, [sp, #-16]!");
    if (a.text[0] != 253 || a.text[1] != 123 || a.text[2] != 191 || a.text[3] != 169) { return 1; }
    // ldp x29, x30, [sp], #16 -> 0xA8C17BFD -> FD 7B C1 A8
    let b: Arm64Asm = arm64_gas_assemble("ldp x29, x30, [sp], #16");
    if (b.text[0] != 253 || b.text[1] != 123 || b.text[2] != 193 || b.text[3] != 168) { return 2; }
    // str x0, [sp, #-16]! -> 0xF81F0FE0 -> E0 0F 1F F8
    let c: Arm64Asm = arm64_gas_assemble("str x0, [sp, #-16]!");
    if (c.text[0] != 224 || c.text[1] != 15 || c.text[2] != 31 || c.text[3] != 248) { return 3; }
    // ldr x0, [sp], #16 -> 0xF84107E0 -> E0 07 41 F8
    let d: Arm64Asm = arm64_gas_assemble("ldr x0, [sp], #16");
    if (d.text[0] != 224 || d.text[1] != 7 || d.text[2] != 65 || d.text[3] != 248) { return 4; }
    // mov x29, sp -> add x29, sp, #0 -> 0x910003FD -> FD 03 00 91
    let e: Arm64Asm = arm64_gas_assemble("mov x29, sp");
    if (e.text[0] != 253 || e.text[1] != 3 || e.text[2] != 0 || e.text[3] != 145) { return 5; }
    // mov sp, x29 -> add sp, x29, #0 -> 0x910003BF -> BF 03 00 91
    let f: Arm64Asm = arm64_gas_assemble("mov sp, x29");
    if (f.text[0] != 191 || f.text[1] != 3 || f.text[2] != 0 || f.text[3] != 145) { return 6; }
    // offset (non-writeback) form still works: str x0, [sp, #8] -> 0xF90007E0
    let g: Arm64Asm = arm64_gas_assemble("str x0, [sp, #8]");
    if (g.text[0] != 224 || g.text[1] != 7 || g.text[2] != 0 || g.text[3] != 249) { return 7; }
    return 0;
}
`

// arm64MachOFnGasDriverMain assembles the real prologue/epilogue + frame
// idiom the arm64-darwin backend emits, computing exit(42) via a push/pop
// round-trip of the value through the stack frame.
const arm64MachOFnGasDriverMain = "\n" +
	"function main(): i32 {\n" +
	"    let asm: string = \"\";\n" +
	"    asm = asm + \"_main:\\n\";\n" +
	"    asm = asm + \"    bl fn\\n\";\n" +
	"    asm = asm + \"    mov x16, #1\\n\";\n" +
	"    asm = asm + \"    svc #0x80\\n\";\n" +
	"    asm = asm + \"fn:\\n\";\n" +
	"    asm = asm + \"    stp x29, x30, [sp, #-16]!\\n\";\n" +
	"    asm = asm + \"    mov x29, sp\\n\";\n" +
	"    asm = asm + \"    mov x0, #42\\n\";\n" +
	"    asm = asm + \"    str x0, [sp, #-16]!\\n\";\n" +
	"    asm = asm + \"    ldr x0, [sp], #16\\n\";\n" +
	"    asm = asm + \"    mov sp, x29\\n\";\n" +
	"    asm = asm + \"    ldp x29, x30, [sp], #16\\n\";\n" +
	"    asm = asm + \"    ret\\n\";\n" +
	"    let a: Arm64Asm = arm64_gas_assemble(asm);\n" +
	"    let none: i32[] = [];\n" +
	"    let bin: i32[] = macho_executable(a.text, none, none, \"fern\", macho_entry_off(a), 0, none);\n" +
	"    write(string_from_bytes_unchecked(to_u8(bin)));\n" +
	"    return 0;\n" +
	"}\n"
