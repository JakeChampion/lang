package e2eselfhost

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
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm.fern", "wasm_run.fern"} {
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

// TestSelfHostX86GasMulRuns assembles `imulq` (6 * 7 = 42) end-to-end.
func TestSelfHostX86GasMulRuns(t *testing.T) {
	runX86GasNativeDriver(t, "gasmul42", x86GasMulDriverMain, 42)
}

// TestSelfHostX86GasIncShlRuns assembles incq + shlq ((5+1)<<3 - 6 = 42).
func TestSelfHostX86GasIncShlRuns(t *testing.T) {
	runX86GasNativeDriver(t, "gasincshl42", x86GasIncShlDriverMain, 42)
}

// TestSelfHostX86GasDivRuns assembles cqto + idivq (84 / 2 = 42).
func TestSelfHostX86GasDivRuns(t *testing.T) {
	runX86GasNativeDriver(t, "gasdiv42", x86GasDivDriverMain, 42)
}

// TestSelfHostX86GasExtRegRuns exercises extended registers r8-r15 in
// arithmetic (imulq %r13,%r12; 6*7=42) — REX.R/.B on reg-reg + B8 imm.
func TestSelfHostX86GasExtRegRuns(t *testing.T) {
	runX86GasNativeDriver(t, "gasext42", x86GasExtRegDriverMain, 42)
}

// TestSelfHostX86GasExtMemRuns exercises extended registers in memory ops:
// store r8 to [rsp] (SIB) and load it into r9, exit(r9) = 42.
func TestSelfHostX86GasExtMemRuns(t *testing.T) {
	runX86GasNativeDriver(t, "gasextmem42", x86GasExtMemDriverMain, 42)
}

// TestSelfHostX86GasIndexRuns exercises SIB-index addressing: store 42 at
// [rsp + rcx*8] and load it back, exit(rdi) = 42.
func TestSelfHostX86GasIndexRuns(t *testing.T) {
	runX86GasNativeDriver(t, "gasindex42", x86GasIndexDriverMain, 42)
}

// TestSelfHostX86GasByteImmRuns exercises movb $imm + movzbq: store byte 42
// to [rsp], zero-extend-load it, exit 42.
func TestSelfHostX86GasByteImmRuns(t *testing.T) {
	runX86GasNativeDriver(t, "gasbyteimm42", x86GasByteImmDriverMain, 42)
}

// TestSelfHostX86GasByteRegRuns exercises movb %reg8 + movzbq into an
// extended register: store %cl to [rsp], load into %r8, exit 42.
func TestSelfHostX86GasByteRegRuns(t *testing.T) {
	runX86GasNativeDriver(t, "gasbytereg42", x86GasByteRegDriverMain, 42)
}

// TestSelfHostX86GasRuntimeOpsRuns exercises the mnemonics asm.fern's
// mmap-era alloc/RC runtime emits that the front-end grew for #4801:
// unsuffixed movabs, shrq, btq + jc (the RC sentinel-bit test), incl
// sym(%rip) + movl sym(%rip) (the rc-underflow counter), movl $imm →
// mem (refcount zeroing), and store-form cmpq %reg, mem (the array-push
// capacity check). Before, unknown mnemonics were silently dropped and
// the two mis-dispatched forms encoded a WRONG instruction — the
// assembled runtime then died in __fern_alloc's exit-137 bounds trap.
func TestSelfHostX86GasRuntimeOpsRuns(t *testing.T) {
	runX86GasNativeDriver(t, "gasruntimeops42", x86GasRuntimeOpsDriverMain, 42)
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
	for _, n := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm.fern", "wasm_run.fern"} {
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
    // paren-aware operand split + indexed memory parsing (slice 2j):
    if (x86_gas_top_comma("$0, (%r12,%r15,1)") != 2) { return 9; }
    if (x86_gas_top_comma("(%rax,%rcx,1), %rdx") != 13) { return 10; }
    var mi: GasMem = x86_gas_parse_mem("(%r12,%r15,1)");
    if (!mi.has_index || mi.base != 12 || mi.index != 15 || mi.scale != 1) { return 11; }
    var mi2: GasMem = x86_gas_parse_mem("8(%rsp,%rcx,8)");
    if (!mi2.has_index || mi2.base != 4 || mi2.index != 1 || mi2.scale != 8 || mi2.disp != 8) { return 12; }
    // 8-bit register parsing (slice 2k):
    if (x86_gas_reg8("%al") != 0 || x86_gas_reg8("%dl") != 2 || x86_gas_reg8("%r8b") != 8) { return 13; }
    // SH-005: an unsupported mnemonic is RECORDED, not silently dropped —
    // one-operand-shaped, two-operand-shaped, and the cmpb non-imm form.
    if (a.unknown.len() != 0) { return 14; }
    var u1: X86Asm = x86_gas_assemble("\tfrobnicate %rax\n");
    if (u1.unknown.len() != 1) { return 15; }
    var u2: X86Asm = x86_gas_assemble("\tpaddq %xmm0, %xmm1\n");
    if (u2.unknown.len() != 1) { return 16; }
    var u3: X86Asm = x86_gas_assemble("\tcmpb %al, %bl\n");
    if (u3.unknown.len() != 1) { return 17; }
    if (x86_gas_reg8("%rax") != (0 - 1)) { return 14; }
    // xmm parsing + float literal parsing (slice 2n):
    if (x86_gas_xmm("%xmm0") != 0 || x86_gas_xmm("%xmm12") != 12) { return 15; }
    if (!x86_gas_is_xmm("%xmm3") || x86_gas_is_xmm("%rax")) { return 16; }
    var fv: f64 = x86_gas_parse_f64("84.5");
    if (fv < 84.4 || fv > 84.6) { return 17; }
    var fz: f64 = x86_gas_parse_f64("2.0");
    if (fz < 1.9 || fz > 2.1) { return 18; }
    // .ascii byte extraction (slice 2o): "hi!" -> 104,105,33.
    var ab: i32[] = x86_gas_ascii([], ".ascii \"hi!\"");
    if (ab.len() != 3 || ab[0] != 104 || ab[1] != 105 || ab[2] != 33) { return 19; }
    // escape handling: "\n\t" -> 10, 9.
    var ac: i32[] = x86_gas_ascii([], ".ascii \"\\n\\t\"");
    if (ac.len() != 2 || ac[0] != 10 || ac[1] != 9) { return 20; }
    // atoi64 (slice 2p): decimal, hex, negative.
    var n1: i64 = x86_gas_atoi64("$4611686018427387904");
    var ref1: i64 = 4611686018427387904;
    if (n1 != ref1) { return 21; }
    var n2: i64 = x86_gas_atoi64("$0x40");
    var ref2: i64 = 64;
    if (n2 != ref2) { return 22; }
    var n3: i64 = x86_gas_atoi64("-5");
    var ref3: i64 = 0 - 5;
    if (n3 != ref3) { return 23; }
    // exponent parsing (#4342): the pre-fix parser stopped at the 'e',
    // so a spliced-text .double operand like 1e3 mis-assembled off by the
    // whole power of ten.
    var fe: f64 = x86_gas_parse_f64("1e3");
    if (fe < 999.9 || fe > 1000.1) { return 24; }
    var fs: f64 = x86_gas_parse_f64("1.5e-2");
    if (fs < 0.0149 || fs > 0.0151) { return 25; }
    var fm: f64 = x86_gas_parse_f64("-2.5e+2");
    if (fm < (0.0 - 250.1) || fm > (0.0 - 249.9)) { return 26; }
    return 0;
}
`

// x86GasLoopDriverMain assembles a hand-written GAS loop and writes the
// resulting ELF to stdout. acc=0; repeat 7×: acc += 6; exit(acc) -> 42.
const x86GasLoopDriverMain = `
function main(): i32 {
    var src: string = ".text\n.globl _start\n_start:\n\tmovq $0, %rax\n\tmovq $7, %rcx\nloop:\n\taddq $6, %rax\n\tsubq $1, %rcx\n\tcmpq $0, %rcx\n\tjne loop\n\tmovq %rax, %rdi\n\tmovq $60, %rax\n\tsyscall\n";
    var a: X86Asm = x86_gas_assemble(src);
    if (a.unknown.len() > 0) { return 2; }
    var bin: i32[] = elf_static_executable_data_x86(a.code, a.rodata);
    write(string_from_bytes_unchecked(bin));
    return 0;
}
`

// x86GasRodataDriverMain assembles a GAS program that loads a .rodata
// .quad via leaq sym(%rip), exercising the directive + rip path -> 42.
const x86GasRodataDriverMain = `
function main(): i32 {
    var src: string = ".text\n_start:\n\tleaq answer(%rip), %rax\n\tmovq (%rax), %rax\n\tmovq %rax, %rdi\n\tmovq $60, %rax\n\tsyscall\n.section .rodata\nanswer:\n\t.quad 42\n";
    var a: X86Asm = x86_gas_assemble(src);
    if (a.unknown.len() > 0) { return 2; }
    var bin: i32[] = elf_static_executable_data_x86(a.code, a.rodata);
    write(string_from_bytes_unchecked(bin));
    return 0;
}
`

// x86GasMulDriverMain: imulq (6 * 7 = 42).
const x86GasMulDriverMain = `
function main(): i32 {
    var src: string = "\tmovq $6, %rax\n\tmovq $7, %rcx\n\timulq %rcx, %rax\n\tmovq %rax, %rdi\n\tmovq $60, %rax\n\tsyscall\n";
    var a: X86Asm = x86_gas_assemble(src);
    if (a.unknown.len() > 0) { return 2; }
    write(string_from_bytes_unchecked(elf_static_executable_data_x86(a.code, a.rodata)));
    return 0;
}
`

// x86GasIncShlDriverMain: incq + shlq ((5+1)<<3 - 6 = 48 - 6 = 42).
const x86GasIncShlDriverMain = `
function main(): i32 {
    var src: string = "\tmovq $5, %rax\n\tincq %rax\n\tshlq $3, %rax\n\tsubq $6, %rax\n\tmovq %rax, %rdi\n\tmovq $60, %rax\n\tsyscall\n";
    var a: X86Asm = x86_gas_assemble(src);
    if (a.unknown.len() > 0) { return 2; }
    write(string_from_bytes_unchecked(elf_static_executable_data_x86(a.code, a.rodata)));
    return 0;
}
`

// x86GasDivDriverMain: cqto + idivq (84 / 2 = 42).
const x86GasDivDriverMain = `
function main(): i32 {
    var src: string = "\tmovq $84, %rax\n\tcqto\n\tmovq $2, %rcx\n\tidivq %rcx\n\tmovq %rax, %rdi\n\tmovq $60, %rax\n\tsyscall\n";
    var a: X86Asm = x86_gas_assemble(src);
    if (a.unknown.len() > 0) { return 2; }
    write(string_from_bytes_unchecked(elf_static_executable_data_x86(a.code, a.rodata)));
    return 0;
}
`

// x86GasExtRegDriverMain: extended-register arithmetic (imulq %r13,%r12).
const x86GasExtRegDriverMain = `
function main(): i32 {
    var src: string = "\tmovq $6, %r12\n\tmovq $7, %r13\n\timulq %r13, %r12\n\tmovq %r12, %rdi\n\tmovq $60, %rax\n\tsyscall\n";
    var a: X86Asm = x86_gas_assemble(src);
    if (a.unknown.len() > 0) { return 2; }
    write(string_from_bytes_unchecked(elf_static_executable_data_x86(a.code, a.rodata)));
    return 0;
}
`

// x86GasRuntimeOpsDriverMain: the #4801 runtime-mnemonic battery. rcx =
// (1<<34) >> 30 = 16 (movabs + shrq); btq $4 sets CF (bit 4 of 16) so jc
// takes the good path; counter(%rip) is incl'd twice and movl-loaded
// (eax=2); movl $24 lands in a movq-zeroed stack slot and reloads as 24;
// 24 + 2 + 16 = 42; store-form cmpq (24 vs 16) makes ja skip the
// failure overwrite. Any dropped/mis-encoded instruction exits != 42.
const x86GasRuntimeOpsDriverMain = `
function main(): i32 {
    var src: string = ".text\n.globl _start\n_start:\n\tmovabs $17179869184, %rcx\n\tshrq $30, %rcx\n\tbtq $4, %rcx\n\tjc bitok\n\tmovq $1, %rdi\n\tjmp done\nbitok:\n\tincl counter(%rip)\n\tincl counter(%rip)\n\tmovl counter(%rip), %eax\n\tsubq $16, %rsp\n\tmovq $0, 8(%rsp)\n\tmovl $24, 8(%rsp)\n\tmovq 8(%rsp), %rdi\n\taddq %rax, %rdi\n\taddq %rcx, %rdi\n\tcmpq %rcx, 8(%rsp)\n\tja done\n\tmovq $2, %rdi\ndone:\n\tmovq $60, %rax\n\tsyscall\n.section .bss\n.align 8\ncounter: .quad 0\n";
    var a: X86Asm = x86_gas_assemble(src);
    var entry: i32 = x86_label_off(a, "_start");
    write(string_from_bytes_unchecked(elf_static_executable_bss_x86_at(a.code, a.rodata, a.bss_size, entry)));
    return 0;
}
`

// x86GasExtMemDriverMain: store r8 to [rsp], reload into r9, exit(r9)=42.
const x86GasExtMemDriverMain = `
function main(): i32 {
    var src: string = "\tsubq $16, %rsp\n\tmovq $42, %r8\n\tmovq %r8, (%rsp)\n\tmovq (%rsp), %r9\n\tmovq %r9, %rdi\n\taddq $16, %rsp\n\tmovq $60, %rax\n\tsyscall\n";
    var a: X86Asm = x86_gas_assemble(src);
    if (a.unknown.len() > 0) { return 2; }
    write(string_from_bytes_unchecked(elf_static_executable_data_x86(a.code, a.rodata)));
    return 0;
}
`

// x86GasIndexDriverMain: SIB-index store/load — [rsp + rcx*8] = 42, reload.
const x86GasIndexDriverMain = `
function main(): i32 {
    var src: string = "\tsubq $64, %rsp\n\tmovq $42, %rax\n\tmovq $2, %rcx\n\tmovq %rax, (%rsp,%rcx,8)\n\tmovq (%rsp,%rcx,8), %rdi\n\taddq $64, %rsp\n\tmovq $60, %rax\n\tsyscall\n";
    var a: X86Asm = x86_gas_assemble(src);
    if (a.unknown.len() > 0) { return 2; }
    write(string_from_bytes_unchecked(elf_static_executable_data_x86(a.code, a.rodata)));
    return 0;
}
`

// x86GasByteImmDriverMain: movb $42, (%rsp) ; movzbq (%rsp), %rdi.
const x86GasByteImmDriverMain = `
function main(): i32 {
    var src: string = "\tsubq $16, %rsp\n\tmovb $42, (%rsp)\n\tmovzbq (%rsp), %rdi\n\taddq $16, %rsp\n\tmovq $60, %rax\n\tsyscall\n";
    var a: X86Asm = x86_gas_assemble(src);
    if (a.unknown.len() > 0) { return 2; }
    write(string_from_bytes_unchecked(elf_static_executable_data_x86(a.code, a.rodata)));
    return 0;
}
`

// x86GasByteRegDriverMain: movb %cl, (%rsp) ; movzbq (%rsp), %r8.
const x86GasByteRegDriverMain = `
function main(): i32 {
    var src: string = "\tsubq $16, %rsp\n\tmovq $42, %rcx\n\tmovb %cl, (%rsp)\n\tmovzbq (%rsp), %r8\n\tmovq %r8, %rdi\n\taddq $16, %rsp\n\tmovq $60, %rax\n\tsyscall\n";
    var a: X86Asm = x86_gas_assemble(src);
    if (a.unknown.len() > 0) { return 2; }
    write(string_from_bytes_unchecked(elf_static_executable_data_x86(a.code, a.rodata)));
    return 0;
}
`
