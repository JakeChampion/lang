package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestSelfHostX86Encode exercises the self-hosted x86-64 machine-code
// encoding primitives (examples/self_host/x86_encode.fern) — slice 2a of
// the native binary backend (the assembler half; the container half is
// elf.fern). It mirrors internal/native/x86_64/asm.go's byte emission.
//
// x86_encode.fern is import-free, so this test concatenates it with a
// self-test main() that encodes each instruction and asserts the bytes
// against the ground-truth encodings (cross-checked with `as`/objdump),
// then runs the combined program through the self-host wasm pipeline
// (wasm_run -> WAT -> wasmtime). Exit 0 = all checks pass; a failing
// check returns its 1-based id.
func TestSelfHostX86Encode(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host x86_encode e2e")
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
	source := string(enc) + "\n" + x86EncodeSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the x86_encode self-test")
	}
	watPath := filepath.Join(dir, "x86_encode_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("x86_encode self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

// TestSelfHostX86ElfExitRuns is the first true end-to-end proof of the
// native-binary track: a Fern program (x86_encode.fern + elf.fern + a
// driver) assembles an exit(42) program to machine code, wraps it in a
// static ELF via elf.fern, and writes the raw binary to stdout. The Go
// test captures that binary, writes it 0o755, and runs it *natively* on
// x86-64 — asserting the process exits 42. This exercises the whole chain
// (Fern instruction encoder -> ELF writer -> kernel load -> syscall) with
// no external assembler or linker.
func TestSelfHostX86ElfExitRuns(t *testing.T) {
	runX86NativeDriver(t, "exit42", x86ElfExitDriverMain, 42)
}

// TestSelfHostX86LoopRuns extends the end-to-end proof to control flow: a
// Fern program assembles a real loop (acc=0; repeat 7×: acc += 6;
// exit(acc)) — exercising the immediate ALU encoders and a backward
// conditional branch (jne rel32) — wraps it in an ELF via elf.fern, and
// the binary runs natively on x86-64 exiting 42 (= 6 × 7).
func TestSelfHostX86LoopRuns(t *testing.T) {
	runX86NativeDriver(t, "loop42", x86ElfLoopDriverMain, 42)
}

// runX86NativeDriver compiles x86_encode.fern + elf.fern + driverMain
// through the self-host wasm emitter, runs the resulting WAT under
// wasmtime to obtain the raw ELF the Fern program assembled and wrote to
// stdout, then executes that ELF natively on x86-64 and asserts its exit
// code — the whole chain (Fern encoder -> ELF writer -> kernel -> syscall)
// with no external assembler or linker.
func runX86NativeDriver(t *testing.T, name, driverMain string, wantExit int) {
	t.Helper()
	if runtime.GOARCH != "amd64" {
		t.Skip("native x86-64 run requires an amd64 host")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host x86 ELF run")
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
	elf, err := os.ReadFile("../../examples/self_host/elf.fern")
	if err != nil {
		t.Fatalf("read elf.fern: %v", err)
	}
	source := string(enc) + "\n" + string(elf) + "\n" + driverMain

	// Stage 1: compile the driver source to WAT via the self-host emitter.
	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatalf("wasm emitter produced 0 bytes for the %s driver", name)
	}
	watPath := filepath.Join(dir, name+"_driver.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}

	// Stage 2: run the WAT under wasmtime; its stdout is the raw ELF binary
	// the Fern program assembled and wrote via write(string_from_bytes(...)).
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

// x86EncodeSelfTestMain asserts each encoder against the ground-truth
// bytes (verified with `as` / objdump). Each `return N` is a distinct
// failing-check id (0 = all pass). Decimal byte values: 0xB8=184,
// 0x3C=60, 0xBF=191, 0x2A=42, 0x48=72, 0x89=137, 0xF8=248, 0x01=1,
// 0xC8=200, 0x29=41, 0x55=85, 0x5D=93, 0x0F=15, 0x05=5, 0xC3=195.
const x86EncodeSelfTestMain = `
function main(): i32 {
    // mov eax, 60  ->  B8 3C 00 00 00
    var a: i32[] = x86_mov_r32_imm32([], x86_rax(), 60);
    if (a.len() != 5 || a[0] != 184 || a[1] != 60 || a[2] != 0 || a[3] != 0 || a[4] != 0) { return 1; }
    // mov edi, 42  ->  BF 2A 00 00 00
    var b: i32[] = x86_mov_r32_imm32([], x86_rdi(), 42);
    if (b.len() != 5 || b[0] != 191 || b[1] != 42) { return 2; }
    // mov rax, rdi ->  48 89 F8
    var c: i32[] = x86_mov_r64_r64([], x86_rax(), x86_rdi());
    if (c.len() != 3 || c[0] != 72 || c[1] != 137 || c[2] != 248) { return 3; }
    // add rax, rcx ->  48 01 C8
    var d: i32[] = x86_add_r64_r64([], x86_rax(), x86_rcx());
    if (d.len() != 3 || d[0] != 72 || d[1] != 1 || d[2] != 200) { return 4; }
    // sub rax, rcx ->  48 29 C8
    var e: i32[] = x86_sub_r64_r64([], x86_rax(), x86_rcx());
    if (e.len() != 3 || e[0] != 72 || e[1] != 41 || e[2] != 200) { return 5; }
    // push rbp ->  55 ; pop rbp ->  5D
    var f: i32[] = x86_push_r64([], x86_rbp());
    if (f.len() != 1 || f[0] != 85) { return 6; }
    var g: i32[] = x86_pop_r64([], x86_rbp());
    if (g.len() != 1 || g[0] != 93) { return 7; }
    // syscall ->  0F 05
    var h: i32[] = x86_syscall([]);
    if (h.len() != 2 || h[0] != 15 || h[1] != 5) { return 8; }
    // ret ->  C3
    var i: i32[] = x86_ret([]);
    if (i.len() != 1 || i[0] != 195) { return 9; }
    // ModR/M direct-form helper: mod=3, reg=rdi(7), rm=rax(0) -> 0xF8.
    if (x86_modrm(3, x86_rdi(), x86_rax()) != 248) { return 10; }
    // add rax, 6 -> 48 81 C0 06 00 00 00
    var j: i32[] = x86_add_r64_imm32([], x86_rax(), 6);
    if (j.len() != 7 || j[0] != 72 || j[1] != 129 || j[2] != 192 || j[3] != 6 || j[4] != 0) { return 11; }
    // sub rcx, 1 -> 48 81 E9 01 00 00 00
    var k: i32[] = x86_sub_r64_imm32([], x86_rcx(), 1);
    if (k.len() != 7 || k[0] != 72 || k[1] != 129 || k[2] != 233 || k[3] != 1) { return 12; }
    // cmp rcx, 0 -> 48 81 F9 00 00 00 00
    var l: i32[] = x86_cmp_r64_imm32([], x86_rcx(), 0);
    if (l.len() != 7 || l[0] != 72 || l[1] != 129 || l[2] != 249 || l[3] != 0) { return 13; }
    // cmp rax, rcx -> 48 39 C8 (0x39 /r, reg=rcx rm=rax)
    var m: i32[] = x86_cmp_r64_r64([], x86_rax(), x86_rcx());
    if (m.len() != 3 || m[0] != 72 || m[1] != 57 || m[2] != 200) { return 14; }
    // jne rel=-27 -> 0F 85 E5 FF FF FF
    var n: i32[] = x86_jne_rel32([], 0 - 27);
    if (n.len() != 6 || n[0] != 15 || n[1] != 133 || n[2] != 229 || n[3] != 255 || n[4] != 255 || n[5] != 255) { return 15; }
    // je rel=0 -> 0F 84 00 00 00 00
    var o: i32[] = x86_je_rel32([], 0);
    if (o.len() != 6 || o[0] != 15 || o[1] != 132 || o[2] != 0) { return 16; }
    // jmp rel=0 -> E9 00 00 00 00
    var pp: i32[] = x86_jmp_rel32([], 0);
    if (pp.len() != 5 || pp[0] != 233 || pp[1] != 0) { return 17; }
    // rel math: branch at 31, len 6, target 10 -> -27.
    if (x86_branch_rel(10, 31, 6) != (0 - 27)) { return 18; }
    return 0;
}
`

// x86ElfExitDriverMain assembles exit(42) (mov edi,42 ; mov eax,60 ;
// syscall), wraps it in a static x86-64 ELF, and writes the raw binary to
// stdout for the Go test to run natively.
const x86ElfExitDriverMain = `
function main(): i32 {
    var code: i32[] = [];
    code = x86_mov_r32_imm32(code, x86_rdi(), 42); // exit code
    code = x86_mov_r32_imm32(code, x86_rax(), 60); // __NR_exit
    code = x86_syscall(code);
    var bin: i32[] = elf_static_executable_x86(code); // R+X, text-only
    write(string_from_bytes(bin));
    return 0;
}
`

// x86ElfLoopDriverMain assembles a real loop — acc=0; for i in 7 { acc +=
// 6 }; exit(acc) — exercising the immediate ALU + a backward conditional
// branch (jne rel32, target known) end-to-end. 6 * 7 = 42. The branch
// displacement is computed from the recorded loop offset via
// x86_branch_rel, the same math a label resolver will use.
const x86ElfLoopDriverMain = `
function main(): i32 {
    var code: i32[] = [];
    code = x86_mov_r32_imm32(code, x86_rax(), 0); // acc = 0
    code = x86_mov_r32_imm32(code, x86_rcx(), 7); // counter = 7
    var loop_off: i32 = code.len();               // backward-branch target
    code = x86_add_r64_imm32(code, x86_rax(), 6);  // acc += 6
    code = x86_sub_r64_imm32(code, x86_rcx(), 1);  // counter -= 1
    code = x86_cmp_r64_imm32(code, x86_rcx(), 0);  // counter == 0 ?
    var jne_off: i32 = code.len();
    var rel: i32 = x86_branch_rel(loop_off, jne_off, 6);
    code = x86_jne_rel32(code, rel);               // loop while counter != 0
    code = x86_mov_r64_r64(code, x86_rdi(), x86_rax()); // exit code = acc
    code = x86_mov_r32_imm32(code, x86_rax(), 60);  // __NR_exit
    code = x86_syscall(code);
    var bin: i32[] = elf_static_executable_x86(code);
    write(string_from_bytes(bin));
    return 0;
}
`
