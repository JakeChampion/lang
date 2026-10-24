package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostArm64DataGas(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64 data gas e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64DataGasSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64 data gas self-test")
	}
	watPath := filepath.Join(dir, "arm64_data_gas_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("arm64 data gas self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

func TestSelfHostArm64DarwinMachOSymbolRuns(t *testing.T) {
	assertMachORuns(t, machoRun{name: "sym42", main: arm64MachOSymbolDriverMain, wantExit: 42, wantData: true})
	assertMachORuns(t, machoRun{name: "const42", main: strings.Replace(arm64MachOSymbolDriverMain, "__DATA,__data", "__TEXT,__const", 1), wantExit: 42})
}

const arm64DataGasSelfTestMain = `
function main(): i32 {
    // .quad in __data: 8 bytes, symbol at offset 0.
    let p1: Arm64GasProg = arm64_gas_program(".section __DATA,__data\nanswer:\n.quad 42\n");
    if (p1.data.len() != 8) { return 1; }
    if (p1.data[0] != 42 || p1.data[1] != 0) { return 2; }
    if (arm64_gas_dlabel_off(p1, "answer") != 0) { return 3; }
    // .byte + .4byte (258 = 0x102 -> 02 01 00 00).
    let p2: Arm64GasProg = arm64_gas_program(".data\n.byte 7\n.4byte 258\n");
    if (p2.data[0] != 7 || p2.data[1] != 2 || p2.data[2] != 1 || p2.data[3] != 0 || p2.data[4] != 0) { return 4; }
    // .asciz "hi" -> 68 69 00.
    let p3: Arm64GasProg = arm64_gas_program(".data\nmsg:\n.asciz \"hi\"\n");
    if (p3.data[0] != 104 || p3.data[1] != 105 || p3.data[2] != 0) { return 5; }
    // @PAGE/@PAGEOFF queue two fixups, then link patches adrp + ldr.
    let p4: Arm64GasProg = arm64_gas_program("adrp x1, answer@PAGE\nldr x0, [x1, answer@PAGEOFF]\nmov x16, #1\nsvc #0x80\n.section __DATA,__data\nanswer:\n.quad 42\n");
    if (p4.pf_sites.len() != 2) { return 6; }
    p4 = arm64_gas_link(p4, 0x100000310, 0x100004000);
    let as4: Arm64Asm = p4.asm;
    // adrp x1, #4 -> 0x90000021 -> 21 00 00 90
    if (as4.text[0] != 33 || as4.text[1] != 0 || as4.text[2] != 0 || as4.text[3] != 144) { return 7; }
    // ldr x0, [x1, #0] -> 0xF9400020 -> 20 00 40 F9
    if (as4.text[4] != 32 || as4.text[5] != 0 || as4.text[6] != 64 || as4.text[7] != 249) { return 8; }
    // A __TEXT,__const constant follows the code, 16-aligned, and resolves
    // against the text address: no data bytes, and the page fixup lands in
    // __TEXT.
    let p5: Arm64GasProg = arm64_gas_program("adrp x1, answer@PAGE\nldr x0, [x1, answer@PAGEOFF]\n.section __TEXT,__const\nanswer:\n.quad 42\n");
    if (p5.data.len() != 0 || p5.ro_base != 16 || p5.asm.text.len() != 24 || p5.asm.text[16] != 42) { return 9; }
    p5 = arm64_gas_link(p5, 0x100000000, 0x100004000);
    let as5: Arm64Asm = p5.asm;
    // adrp x1, #0 -> 0x90000001 -> 01 00 00 90; ldr x0, [x1, #16] -> 0xF9400820
    if (as5.text[0] != 1 || as5.text[3] != 144 || as5.text[4] != 32 || as5.text[5] != 8 || as5.text[6] != 64 || as5.text[7] != 249) { return 10; }
    // A load-time address cannot live in read-only data.
    let p6: Arm64GasProg = arm64_gas_program(".section __TEXT,__const\nslot:\n.quad answer\nanswer:\n.quad 1\n");
    if (p6.unknown.len() != 1) { return 11; }
    return 0;
}
`

const arm64MachOSymbolDriverMain = "\n" +
	"function main(): i32 {\n" +
	"    let asm: string = \"\";\n" +
	"    asm = asm + \"_main:\\n\";\n" +
	"    asm = asm + \"    adrp x1, answer@PAGE\\n\";\n" +
	"    asm = asm + \"    ldr x0, [x1, answer@PAGEOFF]\\n\";\n" +
	"    asm = asm + \"    mov x16, #1\\n\";\n" +
	"    asm = asm + \"    svc #0x80\\n\";\n" +
	"    asm = asm + \".section __DATA,__data\\n\";\n" +
	"    asm = asm + \"answer:\\n\";\n" +
	"    asm = asm + \"    .quad 42\\n\";\n" +
	"    let p: Arm64GasProg = arm64_gas_program(asm);\n" +
	"    let pa: Arm64Asm = p.asm;\n" +
	"    let tvaddr: i64 = macho_text_vaddr(pa.text.len(), 0, p.data.len(), p.bss_size);\n" +
	"    let dvaddr: i64 = macho_data_vaddr(pa.text.len(), 0, p.data.len(), p.bss_size);\n" +
	"    p = arm64_gas_link(p, tvaddr, dvaddr);\n" +
	"    let pa2: Arm64Asm = p.asm;\n" +
	"    let none: i32[] = [];\n" +
	"    let bin: i32[] = macho_executable(pa2.text, none, p.data, \"fern\", macho_entry_off(pa2), p.bss_size, arm64_gas_rebase_offs(p));\n" +
	"    write(string_from_bytes_unchecked(to_u8(bin)));\n" +
	"    return 0;\n" +
	"}\n"
