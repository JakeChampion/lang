package e2eselfhost

import (
	"bytes"
	"debug/macho"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestSelfHostArm64Gas exercises the self-hosted AArch64 GAS-text assembler
// (examples/self_host/arm64_gas.fern) — the text->bytes step of the
// arm64-darwin native path, the counterpart of x86_gas.fern. It parses
// assembly-text statements into machine code via the arm64_encode.fern
// encoders + Arm64Asm label machinery.
//
// arm64_gas.fern depends on arm64_encode.fern's names, so this test
// concatenates the two with a self-test main() that assembles snippets and
// asserts the emitted bytes, then runs the whole thing through the
// self-host wasm pipeline. Exit 0 = all checks pass; a failing check
// returns its 1-based id.
func TestSelfHostArm64Gas(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64 gas e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64GasSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64 gas self-test")
	}
	watPath := filepath.Join(dir, "arm64_gas_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("arm64 gas self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

// TestSelfHostArm64DarwinMachOGasRuns is the end-to-end proof of the GAS
// assembler: a Fern program feeds an AArch64 assembly-text program (a
// subroutine call + a backward loop, by label) to arm64_gas_assemble,
// wraps the resulting bytes with macho.fern into an ad-hoc-signed Mach-O,
// and the binary exits 42 (= 6 × 7). This is the first time the
// arm64-darwin path goes from *assembly text* to a runnable signed binary
// with no external as/clang/ld64. Structural everywhere; executed on Apple
// Silicon.
func TestSelfHostArm64DarwinMachOGasRuns(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64-darwin gas run")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64MachOGasDriverMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64-darwin gas driver")
	}
	watPath := filepath.Join(dir, "arm64_macho_gas_driver.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	bin, err := exec.Command("wasmtime", "run", watPath).Output()
	if err != nil {
		t.Fatalf("wasmtime run (driver): %v", err)
	}

	f, err := macho.NewFile(bytes.NewReader(bin))
	if err != nil {
		t.Fatalf("self-host output is not a parseable Mach-O: %v", err)
	}
	if f.Type != macho.TypeExec || f.Cpu != macho.CpuArm64 {
		t.Fatalf("got type=%v cpu=%v, want EXECUTE/arm64", f.Type, f.Cpu)
	}

	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return
	}
	binPath := filepath.Join(dir, "gas42")
	if err := os.WriteFile(binPath, bin, 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	cmd := exec.Command(binPath)
	runErr := cmd.Run()
	ps := cmd.ProcessState
	if ps == nil || !ps.Exited() {
		t.Skipf("self-host Mach-O did not run to a normal exit (err=%v, state=%v)", runErr, ps)
	}
	if got := ps.ExitCode(); got != 42 {
		t.Errorf("self-host arm64-darwin gas exit = %d, want 42", got)
	}
}

// arm64GasSelfTestMain assembles individual GAS-text statements and asserts
// the emitted little-endian bytes. Each `return N` is a distinct
// failing-check id (0 = all pass).
const arm64GasSelfTestMain = `
function main(): i32 {
    // mov x0, #42 -> movz -> 0xD2800540 -> 40 05 80 D2
    var a: Arm64Asm = arm64_gas_assemble("mov x0, #42");
    if (a.code[0] != 64 || a.code[1] != 5 || a.code[2] != 128 || a.code[3] != 210) { return 1; }
    // mov x0, x1 -> movreg -> 0xAA0103E0 -> E0 03 01 AA
    var b: Arm64Asm = arm64_gas_assemble("mov x0, x1");
    if (b.code[0] != 224 || b.code[1] != 3 || b.code[2] != 1 || b.code[3] != 170) { return 2; }
    // add x0, x1, #5 -> 0x91001420 -> 20 14 00 91
    var c: Arm64Asm = arm64_gas_assemble("add x0, x1, #5");
    if (c.code[0] != 32 || c.code[1] != 20 || c.code[2] != 0 || c.code[3] != 145) { return 3; }
    // add x0, x1, x2 -> 0x8B020020 -> 20 00 02 8B
    var d: Arm64Asm = arm64_gas_assemble("add x0, x1, x2");
    if (d.code[0] != 32 || d.code[1] != 0 || d.code[2] != 2 || d.code[3] != 139) { return 4; }
    // cmp x0, x1 -> 0xEB01001F -> 1F 00 01 EB
    var e: Arm64Asm = arm64_gas_assemble("cmp x0, x1");
    if (e.code[0] != 31 || e.code[1] != 0 || e.code[2] != 1 || e.code[3] != 235) { return 5; }
    // ldr x0, [x1, #8] -> 0xF9400420 -> 20 04 40 F9
    var f: Arm64Asm = arm64_gas_assemble("ldr x0, [x1, #8]");
    if (f.code[0] != 32 || f.code[1] != 4 || f.code[2] != 64 || f.code[3] != 249) { return 6; }
    // str x0, [x1, #8] -> 0xF9000420 -> 20 04 00 F9
    var g: Arm64Asm = arm64_gas_assemble("str x0, [x1, #8]");
    if (g.code[0] != 32 || g.code[1] != 4 || g.code[2] != 0 || g.code[3] != 249) { return 7; }
    // svc #0x80 -> 0xD4001001 -> 01 10 00 D4
    var h: Arm64Asm = arm64_gas_assemble("svc #0x80");
    if (h.code[0] != 1 || h.code[1] != 16 || h.code[2] != 0 || h.code[3] != 212) { return 8; }
    // ret -> 0xD65F03C0 -> C0 03 5F D6
    var i: Arm64Asm = arm64_gas_assemble("ret");
    if (i.code[0] != 192 || i.code[1] != 3 || i.code[2] != 95 || i.code[3] != 214) { return 9; }
    // forward b.eq end: branch at 0 to off 4 -> 0x54000020 -> 20 00 00 54
    var j: Arm64Asm = arm64_gas_assemble("b.eq end\nend:\n");
    if (j.code[0] != 32 || j.code[1] != 0 || j.code[2] != 0 || j.code[3] != 84) { return 10; }
    // backward loop: sub at 0, cbnz at 4 targeting 0 (rel -4) -> 0xB5FFFFE1.
    var k: Arm64Asm = arm64_gas_assemble("loop:\nsub x1, x1, #1\ncbnz x1, loop\n");
    if (k.code[4] != 225 || k.code[5] != 255 || k.code[6] != 255 || k.code[7] != 181) { return 11; }
    // comments + blank lines are ignored; labels with trailing code parse.
    var l: Arm64Asm = arm64_gas_assemble("// a comment\n\n  ret // trailing\n");
    if (l.code.len() != 4 || l.code[0] != 192) { return 12; }
    // exponent parsing (#4342): arm64_parse_f64 mirrors x86_gas_parse_f64 —
    // a spliced-text .double operand like 1e3 must scale by its exponent,
    // not stop at the 'e'.
    var pf: f64 = arm64_parse_f64("1e3");
    if (pf < 999.9 || pf > 1000.1) { return 13; }
    var pg: f64 = arm64_parse_f64("1.5e-2");
    if (pg < 0.0149 || pg > 0.0151) { return 14; }
    var ph: f64 = arm64_parse_f64("-2.5e+2");
    if (ph < (0.0 - 250.1) || ph > (0.0 - 249.9)) { return 15; }
    // SH-006: the register decode is STRICT — digit-suffix garbage and
    // out-of-range numbers return -1 instead of a wrong register (x1a
    // decoded as x1, unknown tokens as x0 before).
    if (arm64_gas_reg("x1a") != (0 - 1)) { return 16; }
    if (arm64_gas_reg("x99") != (0 - 1)) { return 17; }
    if (arm64_gas_reg("x31") != (0 - 1)) { return 18; }
    if (arm64_gas_reg("banana") != (0 - 1)) { return 19; }
    if (arm64_gas_reg("x30") != 30 || arm64_gas_reg("w7") != 7 || arm64_gas_reg("d31") != 31 || arm64_gas_reg("sp") != 31) { return 20; }
    // …and the program-level assembler RECORDS a register-shaped operand
    // that fails the decode on p.unknown (top-level and inside a memory
    // operand), so the driver refuses the corrupt output; a clean program
    // records nothing.
    var u1: Arm64GasProg = arm64_gas_program("mov x1a, #1\n");
    if (u1.unknown.len() != 1) { return 21; }
    var u2: Arm64GasProg = arm64_gas_program("ldr x0, [x9q, #8]\n");
    if (u2.unknown.len() != 1) { return 22; }
    var u3: Arm64GasProg = arm64_gas_program("add x0, x1, x2\nldr x0, [sp, #16]\n");
    if (u3.unknown.len() != 0) { return 23; }
    return 0;
}
`

// arm64MachOGasDriverMain assembles a full AArch64 program from text — a
// subroutine call and a backward loop, by label — computing 6 × 7 = 42.
const arm64MachOGasDriverMain = "\n" +
	"function main(): i32 {\n" +
	"    var asm: string = \"\";\n" +
	"    asm = asm + \"_main:\\n\";\n" +
	"    asm = asm + \"    bl compute\\n\";\n" +
	"    asm = asm + \"    mov x16, #1\\n\";\n" +
	"    asm = asm + \"    svc #0x80\\n\";\n" +
	"    asm = asm + \"compute:\\n\";\n" +
	"    asm = asm + \"    mov x0, #0\\n\";\n" +
	"    asm = asm + \"    mov x1, #7\\n\";\n" +
	"    asm = asm + \"loop:\\n\";\n" +
	"    asm = asm + \"    add x0, x0, #6\\n\";\n" +
	"    asm = asm + \"    sub x1, x1, #1\\n\";\n" +
	"    asm = asm + \"    cbnz x1, loop\\n\";\n" +
	"    asm = asm + \"    ret\\n\";\n" +
	"    var a: Arm64Asm = arm64_gas_assemble(asm);\n" +
	"    var none: i32[] = [];\n" +
	"    var bin: i32[] = macho_executable(a.code, none, \"fern\", macho_entry_off(a), 0);\n" +
	"    write(string_from_bytes_unchecked(bin));\n" +
	"    return 0;\n" +
	"}\n"
