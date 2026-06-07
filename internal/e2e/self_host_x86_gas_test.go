package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestSelfHostX86Gas exercises the self-hosted GAS (AT&T) assembly
// front-end (examples/self_host/x86_gas.fern, slice 2g) — the parser that
// turns asm.fern's text into x86_encode.fern calls. It concatenates
// x86_encode.fern + x86_gas.fern + a self-test main() that checks the
// operand parsers and a small assembled program, run through the self-host
// wasm pipeline (wasm_run -> WAT -> wasmtime). Exit 0 = pass.
func TestSelfHostX86Gas(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host x86 gas e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "wasm.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	enc, err := os.ReadFile("../../examples/self_host/x86_encode.fern")
	if err != nil {
		t.Fatalf("read x86_encode.fern: %v", err)
	}
	gas, err := os.ReadFile("../../examples/self_host/x86_gas.fern")
	if err != nil {
		t.Fatalf("read x86_gas.fern: %v", err)
	}
	source := string(enc) + "\n" + string(gas) + "\n" + x86GasSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the x86 gas self-test")
	}
	watPath := filepath.Join(dir, "x86_gas_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("x86 gas self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

// TestSelfHostX86GasLoopRuns is the end-to-end proof of the GAS front-end:
// a Fern program feeds a hand-written GAS loop (acc=0; 7×: acc += 6;
// exit(acc)) to x86_gas_assemble, wraps the result in an ELF via elf.fern,
// and the binary runs natively on x86-64 exiting 42 — assembly text ->
// machine code -> ELF, no external `as` or `ld`.
func TestSelfHostX86GasLoopRuns(t *testing.T) {
	runX86GasNativeDriver(t, "gasloop42", x86GasLoopDriverMain, 42)
}

// TestSelfHostX86GasRodataRuns feeds a GAS program with a `.section
// .rodata` `.quad` constant loaded via `leaq sym(%rip)`, proving the
// front-end's directive + rip-relative path end-to-end (exits 42).
func TestSelfHostX86GasRodataRuns(t *testing.T) {
	runX86GasNativeDriver(t, "gasrodata42", x86GasRodataDriverMain, 42)
}

// runX86GasNativeDriver concatenates x86_encode.fern + x86_gas.fern +
// elf.fern + driverMain, compiles it through the self-host wasm emitter,
// runs the WAT under wasmtime to get the raw ELF the driver assembled and
// wrote to stdout, then executes that ELF natively on x86-64 and asserts
// the exit code.
func runX86GasNativeDriver(t *testing.T, name, driverMain string, wantExit int) {
	t.Helper()
	if runtime.GOARCH != "amd64" {
		t.Skip("native x86-64 run requires an amd64 host")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host x86 gas run")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, n := range []string{"lexer.fern", "parser.fern", "wasm.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", n))
		if err != nil {
			t.Fatalf("read %s: %v", n, err)
		}
		if err := os.WriteFile(filepath.Join(dir, n), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	enc, err := os.ReadFile("../../examples/self_host/x86_encode.fern")
	if err != nil {
		t.Fatalf("read x86_encode.fern: %v", err)
	}
	gas, err := os.ReadFile("../../examples/self_host/x86_gas.fern")
	if err != nil {
		t.Fatalf("read x86_gas.fern: %v", err)
	}
	elf, err := os.ReadFile("../../examples/self_host/elf.fern")
	if err != nil {
		t.Fatalf("read elf.fern: %v", err)
	}
	source := string(enc) + "\n" + string(gas) + "\n" + string(elf) + "\n" + driverMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatalf("wasm emitter produced 0 bytes for the %s driver", name)
	}
	watPath := filepath.Join(dir, name+"_driver.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}

	bin, err := exec.Command("wasmtime", "run", watPath).Output()
	if err != nil {
		t.Fatalf("wasmtime run (driver): %v", err)
	}
	if len(bin) < 4 || bin[0] != 0x7f || bin[1] != 'E' || bin[2] != 'L' || bin[3] != 'F' {
		t.Fatalf("output is not an ELF (bad magic): % x", bin[:min(4, len(bin))])
	}

	binPath := filepath.Join(dir, name)
	if err := os.WriteFile(binPath, bin, 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	got := 0
	if err := exec.Command(binPath).Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run failed (not an exit code): %v", err)
		}
		got = ee.ExitCode()
	}
	if got != wantExit {
		t.Fatalf("exit code = %d, want %d", got, wantExit)
	}
}

// x86GasSelfTestMain checks the operand parsers and a small assembled
// program. Each `return N` is a failing-check id (0 = pass). The embedded
// GAS source uses \n / \t escapes (interpreted by the Fern lexer). 184 =
// 0xB8 (mov eax,imm), 185 = 0xB9 (mov ecx,imm).
const x86GasSelfTestMain = `
function main(): i32 {
    if (x86_gas_reg("%rax") != 0 || x86_gas_reg("%rdi") != 7 || x86_gas_reg("rbp") != 5) { return 1; }
    if (x86_gas_reg("%rip") != (0 - 1)) { return 2; }
    if (x86_gas_atoi("$60") != 60 || x86_gas_atoi("42") != 42 || x86_gas_atoi("-8") != (0 - 8)) { return 3; }
    var m: GasMem = x86_gas_parse_mem("-8(%rbp)");
    if (m.is_rip || m.base != 5 || m.disp != (0 - 8)) { return 4; }
    var m2: GasMem = x86_gas_parse_mem("(%rax)");
    if (m2.is_rip || m2.base != 0 || m2.disp != 0) { return 5; }
    var m3: GasMem = x86_gas_parse_mem("answer(%rip)");
    if (!m3.is_rip || m3.label != "answer") { return 6; }
    var a: X86Asm = x86_gas_assemble("\tmovq $0, %rax\n\tmovq $7, %rcx\nloop:\n\taddq $6, %rax\n\tsubq $1, %rcx\n\tcmpq $0, %rcx\n\tjne loop\n\tmovq %rax, %rdi\n\tmovq $60, %rax\n\tsyscall\n");
    // first instr movq $0,%rax -> B8 00 00 00 00; second movq $7,%rcx -> B9 07 ...
    if (a.code.len() < 10 || a.code[0] != 184 || a.code[1] != 0) { return 7; }
    if (a.code[5] != 185 || a.code[6] != 7) { return 8; }
    return 0;
}
`

// x86GasLoopDriverMain assembles a hand-written GAS loop and writes the
// resulting ELF to stdout. acc=0; repeat 7×: acc += 6; exit(acc) -> 42.
const x86GasLoopDriverMain = `
function main(): i32 {
    var src: string = ".text\n.globl _start\n_start:\n\tmovq $0, %rax\n\tmovq $7, %rcx\nloop:\n\taddq $6, %rax\n\tsubq $1, %rcx\n\tcmpq $0, %rcx\n\tjne loop\n\tmovq %rax, %rdi\n\tmovq $60, %rax\n\tsyscall\n";
    var a: X86Asm = x86_gas_assemble(src);
    var bin: i32[] = elf_static_executable_data_x86(a.code, a.rodata);
    write(string_from_bytes(bin));
    return 0;
}
`

// x86GasRodataDriverMain assembles a GAS program that loads a .rodata
// .quad via leaq sym(%rip), exercising the directive + rip path -> 42.
const x86GasRodataDriverMain = `
function main(): i32 {
    var src: string = ".text\n_start:\n\tleaq answer(%rip), %rax\n\tmovq (%rax), %rax\n\tmovq %rax, %rdi\n\tmovq $60, %rax\n\tsyscall\n.section .rodata\nanswer:\n\t.quad 42\n";
    var a: X86Asm = x86_gas_assemble(src);
    var bin: i32[] = elf_static_executable_data_x86(a.code, a.rodata);
    write(string_from_bytes(bin));
    return 0;
}
`
