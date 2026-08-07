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

// TestSelfHostArm64DataGas exercises the data-section + named-symbol
// relocation added in slice 3i of arm64_gas.fern: the data directives
// (.quad/.4byte/.byte/.asciz) build a __DATA blob with a symbol table, and
// adrp `sym@PAGE` / ldr `sym@PAGEOFF` queue fixups that arm64_gas_link
// resolves against the (hand-supplied) segment addresses. Run through the
// self-host wasm pipeline; exit 0 = all pass, else the failing check id.
func TestSelfHostArm64DataGas(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64 data gas e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

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

// TestSelfHostArm64DarwinMachOSymbolRuns is the end-to-end proof of
// named-symbol relocation through the GAS assembler: a Fern program feeds
// AArch64 text that defines a `.quad 42` in __const and loads it via
// `adrp x1, answer@PAGE` + `ldr x0, [x1, answer@PAGEOFF]`, links the page
// fixups against macho.fern's segment addresses, wraps it into an
// ad-hoc-signed Mach-O, and the binary exits 42 — assembly text with a
// real cross-segment symbol reference to a runnable binary, no external
// tool. Structural everywhere; executed on Apple Silicon.
func TestSelfHostArm64DarwinMachOSymbolRuns(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64-darwin symbol run")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64MachOSymbolDriverMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64-darwin symbol driver")
	}
	watPath := filepath.Join(dir, "arm64_macho_symbol_driver.wat")
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
	if f.Segment("__DATA") == nil {
		t.Fatalf("expected a __DATA segment")
	}

	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return
	}
	binPath := filepath.Join(dir, "sym42")
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
		t.Errorf("self-host arm64-darwin symbol exit = %d, want 42", got)
	}
}

// arm64DataGasSelfTestMain checks data-directive emission, the data symbol
// table, and the @PAGE/@PAGEOFF fixup + link. Each `return N` is a
// distinct failing-check id. The link addresses model the real layout
// (adrp at 0x100000310, __DATA at 0x100004000 -> page delta 4, off 0).
const arm64DataGasSelfTestMain = `
function main(): i32 {
    // .quad in __const: 8 bytes, symbol at offset 0.
    var p1: Arm64GasProg = arm64_gas_program(".section __TEXT,__const\nanswer:\n.quad 42\n");
    if (p1.data.len() != 8) { return 1; }
    if (p1.data[0] != 42 || p1.data[1] != 0) { return 2; }
    if (arm64_gas_dlabel_off(p1, "answer") != 0) { return 3; }
    // .byte + .4byte (258 = 0x102 -> 02 01 00 00).
    var p2: Arm64GasProg = arm64_gas_program(".data\n.byte 7\n.4byte 258\n");
    if (p2.data[0] != 7 || p2.data[1] != 2 || p2.data[2] != 1 || p2.data[3] != 0 || p2.data[4] != 0) { return 4; }
    // .asciz "hi" -> 68 69 00.
    var p3: Arm64GasProg = arm64_gas_program(".data\nmsg:\n.asciz \"hi\"\n");
    if (p3.data[0] != 104 || p3.data[1] != 105 || p3.data[2] != 0) { return 5; }
    // @PAGE/@PAGEOFF queue two fixups, then link patches adrp + ldr.
    var p4: Arm64GasProg = arm64_gas_program("adrp x1, answer@PAGE\nldr x0, [x1, answer@PAGEOFF]\nmov x16, #1\nsvc #0x80\n.section __TEXT,__const\nanswer:\n.quad 42\n");
    if (p4.pf_sites.len() != 2) { return 6; }
    p4 = arm64_gas_link(p4, 0x100000310, 0x100004000);
    var as4: Arm64Asm = p4.asm;
    // adrp x1, #4 -> 0x90000021 -> 21 00 00 90
    if (as4.code[0] != 33 || as4.code[1] != 0 || as4.code[2] != 0 || as4.code[3] != 144) { return 7; }
    // ldr x0, [x1, #0] -> 0xF9400020 -> 20 00 40 F9
    if (as4.code[4] != 32 || as4.code[5] != 0 || as4.code[6] != 64 || as4.code[7] != 249) { return 8; }
    return 0;
}
`

// arm64MachOSymbolDriverMain assembles a program that loads a __const
// `.quad 42` by name (adrp/ldr @PAGE/@PAGEOFF) and exits with it.
const arm64MachOSymbolDriverMain = "\n" +
	"function main(): i32 {\n" +
	"    var asm: string = \"\";\n" +
	"    asm = asm + \"_main:\\n\";\n" +
	"    asm = asm + \"    adrp x1, answer@PAGE\\n\";\n" +
	"    asm = asm + \"    ldr x0, [x1, answer@PAGEOFF]\\n\";\n" +
	"    asm = asm + \"    mov x16, #1\\n\";\n" +
	"    asm = asm + \"    svc #0x80\\n\";\n" +
	"    asm = asm + \".section __TEXT,__const\\n\";\n" +
	"    asm = asm + \"answer:\\n\";\n" +
	"    asm = asm + \"    .quad 42\\n\";\n" +
	"    var p: Arm64GasProg = arm64_gas_program(asm);\n" +
	"    var pa: Arm64Asm = p.asm;\n" +
	"    var tvaddr: i64 = macho_text_vaddr(pa.code.len(), p.data.len());\n" +
	"    var dvaddr: i64 = macho_data_vaddr(pa.code.len(), p.data.len());\n" +
	"    p = arm64_gas_link(p, tvaddr, dvaddr);\n" +
	"    var pa2: Arm64Asm = p.asm;\n" +
	"    var bin: i32[] = macho_executable(pa2.code, p.data, \"fern\", macho_entry_off(pa2), p.bss_size, arm64_gas_rebase_offs(p));\n" +
	"    write(string_from_bytes_unchecked(bin));\n" +
	"    return 0;\n" +
	"}\n"
