package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestSelfHostX86Encode exercises the self-hosted x86-64 machine-code
// encoding primitives (compiler/x86_native.fern) — slice 2a of
// the native binary backend (the assembler half; the container half is
// elf.fern).
//
// x86_native.fern is import-free, so this test concatenates it with a
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
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	nat, err := os.ReadFile("../../compiler/x86_native.fern")
	if err != nil {
		t.Fatalf("read x86_native.fern: %v", err)
	}
	source := string(nat) + "\n" + x86EncodeSelfTestMain

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
// native-binary track: a Fern program (x86_native.fern + elf.fern + a
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

// TestSelfHostX86MaxRuns exercises a *forward* conditional branch (jge
// over the else-arm), resolved via the placeholder + x86_patch_rel32
// path: max(42, 17) exits 42.
func TestSelfHostX86MaxRuns(t *testing.T) {
	runX86NativeDriver(t, "max42", x86ElfMaxDriverMain, 42)
}

// TestSelfHostX86CallRuns exercises a forward `call` + `ret`: main calls a
// subroutine (defined after the call site, so the rel32 is patched) that
// sets the result to 42 and returns; main exits with it.
func TestSelfHostX86CallRuns(t *testing.T) {
	runX86NativeDriver(t, "call42", x86ElfCallDriverMain, 42)
}

// TestSelfHostX86Labels byte-checks the named-label assembler (slice 2d):
// forward branch, backward branch and forward call (each patched by
// x86_resolve), and label lookup. Same wasm self-test shape
// as TestSelfHostX86Encode.
func TestSelfHostX86Labels(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host x86 labels e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	nat, err := os.ReadFile("../../compiler/x86_native.fern")
	if err != nil {
		t.Fatalf("read x86_native.fern: %v", err)
	}
	source := string(nat) + "\n" + x86LabelsSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the x86 labels self-test")
	}
	watPath := filepath.Join(dir, "x86_labels_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("x86 labels self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

// TestSelfHostX86LabelProgramRuns is the end-to-end proof of the label
// assembler: a Fern program uses the named-label API to assemble
// main { call compute; exit(result) } compute { loop 7×: acc += 6; ret }
// — a forward `call` and a backward loop branch, both resolved by name —
// wraps it in an ELF via elf.fern, and the binary runs natively exiting 42.
func TestSelfHostX86LabelProgramRuns(t *testing.T) {
	runX86NativeDriver(t, "label42", x86ElfLabelDriverMain, 42)
}

// TestSelfHostX86FrameRuns is the end-to-end proof of the memory operands
// (slice 2e): a Fern program assembles a stack-frame round-trip — set up
// rbp, store 42 to [rbp-8], clobber the register, reload it, tear the
// frame down — exercising mov reg,reg (rbp/rsp), push/pop, sub rsp, and
// rbp-relative store/load, then runs natively on x86-64 exiting 42.
func TestSelfHostX86FrameRuns(t *testing.T) {
	runX86NativeDriver(t, "frame42", x86ElfFrameDriverMain, 42)
}

// TestSelfHostX86RodataRuns is the end-to-end proof of rip-relative
// addressing + a .rodata section (slice 2f): a Fern program interns a
// `.quad 42` in .rodata, loads its address via `lea rax, [rip+answer]`,
// dereferences it, and exits with the value — wrapped in an R+W+X ELF via
// elf_static_executable_data_x86. A wrong rip displacement or .rodata base
// would not exit 42.
func TestSelfHostX86RodataRuns(t *testing.T) {
	runX86NativeDriver(t, "rodata42", x86ElfRodataDriverMain, 42)
}

// runX86NativeDriver compiles x86_native.fern + elf.fern + driverMain
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
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	nat, err := os.ReadFile("../../compiler/x86_native.fern")
	if err != nil {
		t.Fatalf("read x86_native.fern: %v", err)
	}
	elf, err := os.ReadFile("../../compiler/elf.fern")
	if err != nil {
		t.Fatalf("read elf.fern: %v", err)
	}
	source := string(nat) + "\n" + string(elf) + toU8Src + driverMain

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
	// the Fern program assembled and wrote via write(string_from_bytes_unchecked(...)).
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
function x86enc_selftest_1(): i32 {
    // mov eax, 60  ->  B8 3C 00 00 00
    let a: i32[] = x86_mov_r32_imm32([], x86_rax(), 60);
    if (a.len() != 5 || a[0] != 184 || a[1] != 60 || a[2] != 0 || a[3] != 0 || a[4] != 0) { return 1; }
    // mov edi, 42  ->  BF 2A 00 00 00
    let b: i32[] = x86_mov_r32_imm32([], x86_rdi(), 42);
    if (b.len() != 5 || b[0] != 191 || b[1] != 42) { return 2; }
    // mov rax, rdi ->  48 89 F8
    let c: i32[] = x86_mov_r64_r64([], x86_rax(), x86_rdi());
    if (c.len() != 3 || c[0] != 72 || c[1] != 137 || c[2] != 248) { return 3; }
    // add rax, rcx ->  48 01 C8
    let d: i32[] = x86_add_r64_r64([], x86_rax(), x86_rcx());
    if (d.len() != 3 || d[0] != 72 || d[1] != 1 || d[2] != 200) { return 4; }
    // sub rax, rcx ->  48 29 C8
    let e: i32[] = x86_sub_r64_r64([], x86_rax(), x86_rcx());
    if (e.len() != 3 || e[0] != 72 || e[1] != 41 || e[2] != 200) { return 5; }
    // push rbp ->  55 ; pop rbp ->  5D
    let f: i32[] = x86_push_r64([], x86_rbp());
    if (f.len() != 1 || f[0] != 85) { return 6; }
    let g: i32[] = x86_pop_r64([], x86_rbp());
    if (g.len() != 1 || g[0] != 93) { return 7; }
    // syscall ->  0F 05
    let h: i32[] = x86_syscall([]);
    if (h.len() != 2 || h[0] != 15 || h[1] != 5) { return 8; }
    // ret ->  C3
    let i: i32[] = x86_ret([]);
    if (i.len() != 1 || i[0] != 195) { return 9; }
    // ModR/M direct-form helper: mod=3, reg=rdi(7), rm=rax(0) -> 0xF8.
    if (x86_modrm(3, x86_rdi(), x86_rax()) != 248) { return 10; }
    return 0;
}

function x86enc_selftest_2(): i32 {
    // add rax, 6 -> 48 81 C0 06 00 00 00
    let j: i32[] = x86_add_r64_imm32([], x86_rax(), 6);
    if (j.len() != 7 || j[0] != 72 || j[1] != 129 || j[2] != 192 || j[3] != 6 || j[4] != 0) { return 11; }
    // sub rcx, 1 -> 48 81 E9 01 00 00 00
    let k: i32[] = x86_sub_r64_imm32([], x86_rcx(), 1);
    if (k.len() != 7 || k[0] != 72 || k[1] != 129 || k[2] != 233 || k[3] != 1) { return 12; }
    // cmp rcx, 0 -> 48 81 F9 00 00 00 00
    let l: i32[] = x86_cmp_r64_imm32([], x86_rcx(), 0);
    if (l.len() != 7 || l[0] != 72 || l[1] != 129 || l[2] != 249 || l[3] != 0) { return 13; }
    // cmp rax, rcx -> 48 39 C8 (0x39 /r, reg=rcx rm=rax)
    let m: i32[] = x86_cmp_r64_r64([], x86_rax(), x86_rcx());
    if (m.len() != 3 || m[0] != 72 || m[1] != 57 || m[2] != 200) { return 14; }
    // jne rel=-27 -> 0F 85 E5 FF FF FF
    let n: i32[] = x86_jne_rel32([], 0 - 27);
    if (n.len() != 6 || n[0] != 15 || n[1] != 133 || n[2] != 229 || n[3] != 255 || n[4] != 255 || n[5] != 255) { return 15; }
    // je rel=0 -> 0F 84 00 00 00 00
    let o: i32[] = x86_je_rel32([], 0);
    if (o.len() != 6 || o[0] != 15 || o[1] != 132 || o[2] != 0) { return 16; }
    // jmp rel=0 -> E9 00 00 00 00
    let pp: i32[] = x86_jmp_rel32([], 0);
    if (pp.len() != 5 || pp[0] != 233 || pp[1] != 0) { return 17; }
    // rel math: branch at 31, len 6, target 10 -> -27.
    if (x86_branch_rel(10, 31, 6) != (0 - 27)) { return 18; }
    // call rel=10 -> E8 0A 00 00 00
    let q: i32[] = x86_call_rel32([], 10);
    if (q.len() != 5 || q[0] != 232 || q[1] != 10 || q[2] != 0 || q[3] != 0 || q[4] != 0) { return 19; }
    // forward-ref rel: target 22, rel32 field at 15 -> 3.
    if (x86_rel_to(22, 15) != 3) { return 20; }
    return 0;
}

function x86enc_selftest_3(): i32 {
    // patch a placeholder: jne rel=0 then patch its rel32 (offset 2) to 3.
    let r: i32[] = x86_jne_rel32([], 0);
    r = x86_patch_rel32(r, 2, 3);
    if (r.len() != 6 || r[0] != 15 || r[1] != 133 || r[2] != 3 || r[3] != 0 || r[4] != 0 || r[5] != 0) { return 21; }
    // patch a negative rel (-27) -> E5 FF FF FF.
    let s: i32[] = x86_jmp_rel32([], 0);
    s = x86_patch_rel32(s, 1, 0 - 27);
    if (s.len() != 5 || s[0] != 233 || s[1] != 229 || s[2] != 255 || s[3] != 255 || s[4] != 255) { return 22; }
    // mov rax, [rbp-8] -> 48 8B 45 F8 (mod=01 disp8, rm=rbp)
    let t: i32[] = x86_mov_load_r64([], x86_rax(), x86_rbp(), 0 - 8);
    if (t.len() != 4 || t[0] != 72 || t[1] != 139 || t[2] != 69 || t[3] != 248) { return 23; }
    // mov [rbp-8], rax -> 48 89 45 F8
    let u: i32[] = x86_mov_store_r64([], x86_rbp(), 0 - 8, x86_rax());
    if (u.len() != 4 || u[0] != 72 || u[1] != 137 || u[2] != 69 || u[3] != 248) { return 24; }
    // mov rax, [rsp+16] -> 48 8B 44 24 10 (SIB escape for rsp)
    let v2: i32[] = x86_mov_load_r64([], x86_rax(), x86_rsp(), 16);
    if (v2.len() != 5 || v2[0] != 72 || v2[1] != 139 || v2[2] != 68 || v2[3] != 36 || v2[4] != 16) { return 25; }
    // mov rax, [rcx] -> 48 8B 01 (mod=00, no disp)
    let w: i32[] = x86_mov_load_r64([], x86_rax(), x86_rcx(), 0);
    if (w.len() != 3 || w[0] != 72 || w[1] != 139 || w[2] != 1) { return 26; }
    // mov rax, [rbp] -> 48 8B 45 00 (rbp forces mod=01 disp8=0)
    let x: i32[] = x86_mov_load_r64([], x86_rax(), x86_rbp(), 0);
    if (x.len() != 4 || x[0] != 72 || x[1] != 139 || x[2] != 69 || x[3] != 0) { return 27; }
    // mov rax, [rcx+512] -> 48 8B 81 00 02 00 00 (mod=10 disp32)
    let y: i32[] = x86_mov_load_r64([], x86_rax(), x86_rcx(), 512);
    if (y.len() != 7 || y[0] != 72 || y[1] != 139 || y[2] != 129 || y[3] != 0 || y[4] != 2 || y[5] != 0 || y[6] != 0) { return 28; }
    // sib byte for [rsp]: scale=0,index=4,base=4 -> 0x24 (36)
    if (x86_sib(0, 4, 4) != 36) { return 29; }
    // slice 2h integer ops (verified vs as/objdump):
    let aa: i32[] = x86_unary_r([], 64, 254, 0, x86_rax());   // incq %rax -> 48 FF C0
    if (aa.len() != 3 || aa[0] != 72 || aa[1] != 255 || aa[2] != 192) { return 30; }
    return 0;
}

function x86enc_selftest_4(): i32 {
    let bb: i32[] = x86_unary_r([], 64, 254, 1, x86_rcx());   // decq %rcx -> 48 FF C9
    if (bb[2] != 201) { return 31; }
    let cc2: i32[] = x86_unary_r([], 64, 246, 3, x86_rax());  // negq %rax -> 48 F7 D8
    if (cc2[1] != 247 || cc2[2] != 216) { return 32; }
    let dd: i32[] = x86_op_rr([], 64, 132, x86_rax(), x86_rax()); // testq -> 48 85 C0
    if (dd[1] != 133 || dd[2] != 192) { return 33; }
    let ee: i32[] = x86_op_rr([], 64, 32, x86_rcx(), x86_rax());  // andq %rcx,%rax -> 48 21 C8
    if (ee[1] != 33 || ee[2] != 200) { return 34; }
    let ff: i32[] = x86_op_rr([], 64, 8, x86_rcx(), x86_rax());   // orq %rcx,%rax -> 48 09 C8
    if (ff[1] != 9 || ff[2] != 200) { return 35; }
    let gg: i32[] = x86_op_rr([], 64, 48, x86_rcx(), x86_rax());  // xorq %rcx,%rax -> 48 31 C8
    if (gg[1] != 49 || gg[2] != 200) { return 36; }
    let hh: i32[] = x86_op0f_rr([], 64, 175, x86_rax(), x86_rcx()); // imulq %rcx,%rax -> 48 0F AF C1
    if (hh.len() != 4 || hh[1] != 15 || hh[2] != 175 || hh[3] != 193) { return 37; }
    let ii: i32[] = x86_unary_r([], 64, 246, 7, x86_rcx());  // idivq %rcx -> 48 F7 F9
    if (ii[1] != 247 || ii[2] != 249) { return 38; }
    let jj: i32[] = x86_unary_r([], 64, 246, 6, x86_rcx());   // divq %rcx -> 48 F7 F1
    if (jj[2] != 241) { return 39; }
    let kk: i32[] = x86_cqo([]);                  // 48 99
    if (kk.len() != 2 || kk[0] != 72 || kk[1] != 153) { return 40; }
    return 0;
}

function x86enc_selftest_5(): i32 {
    let ll: i32[] = x86_shift_r_imm([], 64, 4, x86_rax(), 3); // shlq $3,%rax -> 48 C1 E0 03
    if (ll.len() != 4 || ll[1] != 193 || ll[2] != 224 || ll[3] != 3) { return 41; }
    // slice 2i extended registers r8..r15 (verified vs as/objdump):
    if (x86_rex(1, 0, 0, 0) != 72 || x86_rex(1, 12, 0, 13) != 77 || x86_rex(0, 0, 0, 12) != 65) { return 42; }
    let ra: i32[] = x86_mov_r64_r64([], 13, 12); // mov r13,r12 -> 4D 89 E5
    if (ra.len() != 3 || ra[0] != 77 || ra[1] != 137 || ra[2] != 229) { return 43; }
    let rb: i32[] = x86_add_r64_r64([], x86_rax(), 12); // add rax,r12 -> 4C 01 E0
    if (rb[0] != 76 || rb[1] != 1 || rb[2] != 224) { return 44; }
    let rc: i32[] = x86_mov_r64_r64([], 8, x86_rax()); // mov r8,rax -> 49 89 C0
    if (rc[0] != 73 || rc[1] != 137 || rc[2] != 192) { return 45; }
    let rd: i32[] = x86_push_r64([], 12); // push r12 -> 41 54
    if (rd.len() != 2 || rd[0] != 65 || rd[1] != 84) { return 46; }
    let re: i32[] = x86_pop_r64([], 13); // pop r13 -> 41 5D
    if (re.len() != 2 || re[0] != 65 || re[1] != 93) { return 47; }
    let rf: i32[] = x86_unary_r([], 64, 254, 0, 12); // inc r12 -> 49 FF C4
    if (rf[0] != 73 || rf[1] != 255 || rf[2] != 196) { return 48; }
    let rg: i32[] = x86_mov_load_r64([], x86_rax(), 13, 0); // mov rax,[r13] -> 49 8B 45 00
    if (rg.len() != 4 || rg[0] != 73 || rg[1] != 139 || rg[2] != 69 || rg[3] != 0) { return 49; }
    let rh: i32[] = x86_mov_store_r64([], 12, 0, x86_rax()); // mov [r12],rax -> 49 89 04 24
    if (rh.len() != 4 || rh[0] != 73 || rh[1] != 137 || rh[2] != 4 || rh[3] != 36) { return 50; }
    return 0;
}

function x86enc_selftest_6(): i32 {
    let rk: i32[] = x86_op0f_rr([], 64, 175, 8, 9); // imul r8,r9 -> 4D 0F AF C1
    if (rk.len() != 4 || rk[0] != 77 || rk[1] != 15 || rk[2] != 175 || rk[3] != 193) { return 51; }
    // slice 2j SIB-index addressing (verified vs as/objdump):
    let sa: i32[] = x86_mov_load_r64_idx([], x86_rax(), x86_rax(), x86_rcx(), 1, 0); // 48 8B 04 08
    if (sa.len() != 4 || sa[0] != 72 || sa[1] != 139 || sa[2] != 4 || sa[3] != 8) { return 52; }
    let sb: i32[] = x86_mov_load_r64_idx([], x86_rax(), x86_rax(), x86_rcx(), 8, 0); // 48 8B 04 C8
    if (sb[3] != 200) { return 53; }
    let sc3: i32[] = x86_mov_load_r64_idx([], x86_rax(), 12, 15, 1, 0); // 4B 8B 04 3C
    if (sc3[0] != 75 || sc3[1] != 139 || sc3[2] != 4 || sc3[3] != 60) { return 54; }
    let sd: i32[] = x86_mov_store_r64_idx([], 13, x86_rcx(), 1, 0, x86_rax()); // 49 89 44 0D 00
    if (sd.len() != 5 || sd[0] != 73 || sd[1] != 137 || sd[2] != 68 || sd[3] != 13 || sd[4] != 0) { return 55; }
    if (x86_scale_bits(1) != 0 || x86_scale_bits(2) != 1 || x86_scale_bits(4) != 2 || x86_scale_bits(8) != 3) { return 56; }
    // slice 2k byte ops (verified vs as/objdump):
    let ba: i32[] = x86_movb_imm_mem([], 6, false, 0, 1, 0, 48); // movb $48,(%rsi) -> C6 06 30
    if (ba.len() != 3 || ba[0] != 198 || ba[1] != 6 || ba[2] != 48) { return 57; }
    let bc: i32[] = x86_movb_imm_mem([], 13, false, 0, 1, 2, 105); // movb $105,2(%r13) -> 41 C6 45 02 69
    if (bc.len() != 5 || bc[0] != 65 || bc[1] != 198 || bc[2] != 69 || bc[3] != 2 || bc[4] != 105) { return 58; }
    let bd: i32[] = x86_movb_imm_mem([], 12, true, 3, 1, 0, 102); // movb $102,(%r12,%rbx,1) -> 41 C6 04 1C 66
    if (bd.len() != 5 || bd[0] != 65 || bd[1] != 198 || bd[2] != 4 || bd[3] != 28 || bd[4] != 102) { return 59; }
    let be: i32[] = x86_movb_reg_mem([], 0, 6, false, 0, 1, 0); // movb %al,(%rsi) -> 88 06
    if (be.len() != 2 || be[0] != 136 || be[1] != 6) { return 60; }
    return 0;
}

function x86enc_selftest_7(): i32 {
    let bf: i32[] = x86_movb_reg_mem([], 0, 11, false, 0, 1, 0); // movb %al,(%r11) -> 41 88 03
    if (bf.len() != 3 || bf[0] != 65 || bf[1] != 136 || bf[2] != 3) { return 61; }
    let bg: i32[] = x86_movzb_mem([], 1, x86_rax(), x86_rax(), false, 0, 1, 0); // movzbq (%rax),%rax -> 48 0F B6 00
    if (bg.len() != 4 || bg[0] != 72 || bg[1] != 15 || bg[2] != 182 || bg[3] != 0) { return 62; }
    let bh: i32[] = x86_movzb_mem([], 1, x86_rdx(), 13, false, 0, 1, 2); // movzbq 2(%r13),%rdx -> 49 0F B6 55 02
    if (bh.len() != 5 || bh[0] != 73 || bh[1] != 15 || bh[2] != 182 || bh[3] != 85 || bh[4] != 2) { return 63; }
    let bj: i32[] = x86_movzb_reg([], 1, x86_rcx(), 0); // movzbq %al,%rcx -> 48 0F B6 C8
    if (bj.len() != 4 || bj[0] != 72 || bj[1] != 15 || bj[2] != 182 || bj[3] != 200) { return 64; }
    let bk: i32[] = x86_grp1_imm_r([], 8, 7, 1, 46); // cmpb $46,%cl -> 80 F9 2E
    if (bk.len() != 3 || bk[0] != 128 || bk[1] != 249 || bk[2] != 46) { return 65; }
    // setCC (slice 2m): setl %al -> 0F 9C C0; setge %al -> 0F 9D C0.
    let bl: i32[] = x86_setcc_reg8([], 156, 0);
    if (bl.len() != 3 || bl[0] != 15 || bl[1] != 156 || bl[2] != 192) { return 66; }
    let bm: i32[] = x86_setcc_reg8([], 157, 1); // setge %cl -> 0F 9D C1
    if (bm[1] != 157 || bm[2] != 193) { return 67; }
    // slice 2n SSE double (verified vs as/objdump):
    let sg: i32[] = x86_movq_gpr_to_xmm([], 1, 0); // movq %rax,%xmm1 -> 66 48 0F 6E C8
    if (sg.len() != 5 || sg[0] != 102 || sg[1] != 72 || sg[2] != 15 || sg[3] != 110 || sg[4] != 200) { return 68; }
    let sh: i32[] = x86_movq_xmm_to_gpr([], 0, 0); // movq %xmm0,%rax -> 66 48 0F 7E C0
    if (sh[3] != 126 || sh[4] != 192) { return 69; }
    let si2: i32[] = x86_sse_any_rr([], 242, 94, 0, 1); // divsd %xmm1,%xmm0 -> F2 0F 5E C1
    if (si2.len() != 4 || si2[0] != 242 || si2[1] != 15 || si2[2] != 94 || si2[3] != 193) { return 70; }
    return 0;
}

function x86enc_selftest_8(): i32 {
    let sj: i32[] = x86_cvttsd2si([], 0, 0); // F2 48 0F 2C C0
    if (sj.len() != 5 || sj[0] != 242 || sj[1] != 72 || sj[2] != 15 || sj[3] != 44 || sj[4] != 192) { return 71; }
    let sk2: i32[] = x86_cvtsi2sd([], 1, 0, 0); // F2 48 0F 2A C0
    if (sk2[3] != 42 || sk2[4] != 192) { return 72; }
    // cvtsi2sdl %ecx, %xmm0 -> F2 0F 2A C1 (no REX.W: the 32-bit source)
    let sk3: i32[] = x86_cvtsi2sd([], 0, 0, 1);
    if (sk3.len() != 4 || sk3[0] != 242 || sk3[1] != 15 || sk3[2] != 42 || sk3[3] != 193) { return 100; }
    let sl: i32[] = x86_sse_any_rr([], 102, 46, 0, 1); // ucomisd -> 66 0F 2E C1
    if (sl.len() != 4 || sl[0] != 102 || sl[1] != 15 || sl[2] != 46 || sl[3] != 193) { return 73; }
    // x86_le64_i64: 8 LE bytes of 0x00000000000000FF -> FF then 7 zeros.
    let sm2: i64 = 255;
    let sn: i32[] = x86_le64_i64([], sm2);
    if (sn.len() != 8 || sn[0] != 255 || sn[1] != 0 || sn[7] != 0) { return 74; }
    // push $imm (slice 2o): push $0 -> 68 00 00 00 00; push $42 -> 68 2A ...
    let so: i32[] = x86_push_imm32([], 0);
    if (so.len() != 5 || so[0] != 104 || so[1] != 0 || so[4] != 0) { return 75; }
    let sp2: i32[] = x86_push_imm32([], 42);
    if (sp2[0] != 104 || sp2[1] != 42) { return 76; }
    // movabsq (slice 2p): movabs %rax, 0x4000000000000000 (4611686018427387904)
    let mab: i64 = 4611686018427387904;
    let sq: i32[] = x86_movabsq([], x86_rax(), mab);
    // 48 B8 00 00 00 00 00 00 00 40
    if (sq.len() != 10 || sq[0] != 72 || sq[1] != 184 || sq[9] != 64) { return 77; }
    if (sq[2] != 0 || sq[8] != 0) { return 78; }
    // movabs %r8, 1 -> 49 B8 01 00.. (REX.W|B)
    let one64: i64 = 1;
    let sr: i32[] = x86_movabsq([], 8, one64);
    if (sr[0] != 73 || sr[1] != 184 || sr[2] != 1) { return 79; }
    // new jcc cc codes feed the same 0F 80+cc encoder: js rel=0 -> 0F 88 00*4.
    let sjs: i32[] = x86_jcc_rel32([], x86_cc_s(), 0);
    if (sjs.len() != 6 || sjs[0] != 15 || sjs[1] != 136) { return 80; }
    return 0;
}

function x86enc_selftest_9(): i32 {
    let sja: i32[] = x86_jcc_rel32([], x86_cc_a(), 0); // 0F 87
    if (sja[1] != 135) { return 81; }
    // slice 2q: 32-bit ALU / moves / extends / cmov / testb / rep / xorpd.
    let ta: i32[] = x86_grp1_imm_r([], 32, 0, x86_rax(), 128); // addl $128,%eax -> 05 80 00 00 00 (accumulator)
    if (ta.len() != 5 || ta[0] != 5 || ta[1] != 128 || ta[2] != 0) { return 82; }
    let tb: i32[] = x86_shift_r_imm([], 32, 5, x86_rax(), 8); // shrl $8,%eax -> C1 E8 08
    if (tb.len() != 3 || tb[0] != 193 || tb[1] != 232 || tb[2] != 8) { return 83; }
    let tc: i32[] = x86_mov_r32_r32([], x86_rax(), x86_rcx()); // movl %ecx,%eax -> 89 C8
    if (tc.len() != 2 || tc[0] != 137 || tc[1] != 200) { return 84; }
    let td: i32[] = x86_mov_r32_r32([], 15, x86_rax()); // movl %eax,%r15d -> 41 89 C7
    if (td.len() != 3 || td[0] != 65 || td[1] != 137 || td[2] != 199) { return 85; }
    let te: i32[] = x86_movl_load([], x86_rdi(), x86_rbp(), false, 0, 1, 0 - 76); // 8B 7D B4
    if (te.len() != 3 || te[0] != 139 || te[1] != 125 || te[2] != 180) { return 86; }
    let tf: i32[] = x86_test_imm_r([], 8, 0, 127); // testb $127,%al -> A8 7F (accumulator)
    if (tf.len() != 2 || tf[0] != 168 || tf[1] != 127) { return 87; }
    let tg: i32[] = x86_op_rr([], 8, 132, 0, 0); // testb %al,%al -> 84 C0
    if (tg.len() != 2 || tg[0] != 132 || tg[1] != 192) { return 88; }
    let th: i32[] = x86_cmovcc_rr([], 64, x86_cc_l(), x86_rax(), x86_rdx()); // cmovl %rdx,%rax -> 48 0F 4C C2
    if (th.len() != 4 || th[0] != 72 || th[1] != 15 || th[2] != 76 || th[3] != 194) { return 89; }
    let ti: i32[] = x86_movslq_load([], x86_rax(), x86_rax(), false, 0, 1, 0); // 48 63 00
    if (ti.len() != 3 || ti[0] != 72 || ti[1] != 99 || ti[2] != 0) { return 90; }
    return 0;
}

function x86enc_selftest_10(): i32 {
    let tj: i32[] = x86_movzwq_load([], x86_rax(), 13, true, 15, 1, 16); // movzwq 16(%r13,%r15,1),%rax -> 4B 0F B7 44 3D 10
    if (tj.len() != 6 || tj[0] != 75 || tj[1] != 15 || tj[2] != 183 || tj[3] != 68 || tj[4] != 61 || tj[5] != 16) { return 91; }
    let tk: i32[] = x86_sse_any_rr([], 102, 87, 0, 1); // xorpd %xmm1,%xmm0 -> 66 0F 57 C1
    if (tk.len() != 4 || tk[0] != 102 || tk[1] != 15 || tk[2] != 87 || tk[3] != 193) { return 92; }
    // rep stosb / cld now route through the string / fixed tables.
    let tl: X86Asm = x86_resolve(x86_gas_emit(x86_asm_new(), "rep", "stosb")); // F3 AA
    if (tl.text.len() != 2 || tl.text[0] as i32 != 243 || tl.text[1] as i32 != 170) { return 93; }
    let tm: X86Asm = x86_resolve(x86_gas_emit(x86_asm_new(), "cld", "")); // FC
    if (tm.text.len() != 1 || tm.text[0] as i32 != 252) { return 94; }
    // movsd reg-reg: movsd %xmm0,%xmm3 -> F2 0F 10 D8
    let tn: i32[] = x86_sse_any_rr([], 242, 16, 3, 0);
    if (tn.len() != 4 || tn[0] != 242 || tn[1] != 15 || tn[2] != 16 || tn[3] != 216) { return 95; }
    // movslq reg-reg: movslq %eax,%rax -> 48 63 C0; movslq %r9d,%r8 -> 4D 63 C1
    let to: i32[] = x86_movslq_rr([], x86_rax(), x86_rax());
    if (to.len() != 3 || to[0] != 72 || to[1] != 99 || to[2] != 192) { return 96; }
    let tp: i32[] = x86_movslq_rr([], 8, 9);
    if (tp[0] != 77 || tp[1] != 99 || tp[2] != 193) { return 97; }
    // sqrtsd %xmm0,%xmm0 -> F2 0F 51 C0; roundsd $1,%xmm0,%xmm0 -> 66 0F 3A 0B C0 01
    let tq: i32[] = x86_sse_any_rr([], 242, 81, 0, 0);
    if (tq.len() != 4 || tq[0] != 242 || tq[1] != 15 || tq[2] != 81 || tq[3] != 192) { return 98; }
    let tr: i32[] = x86_sse_3a_imm([], 0, 11, 0, 0, 1);
    if (tr.len() != 6 || tr[0] != 102 || tr[1] != 15 || tr[2] != 58 || tr[3] != 11 || tr[4] != 192 || tr[5] != 1) { return 99; }
    return 0;
}

function x86enc_selftest_11(): i32 {
    // Packed SSE2 — the encodings the __memchr / __ascii_run vector kernels
    // need (docs/ATLAS-PLATFORM-PLAN.md §3.3a: land the assembler's encodings
    // before emitting a kernel on a target). Every expectation below is GNU
    // as output, not a hand-derived table.
    //
    // Each instruction is checked once with low registers and once with an
    // x86-64 extended one, because the REX bit is the half a table gets wrong:
    // omit REX.R and pmovmskb writes %ecx where %r9d was meant, which reads
    // correct at the call site.
    // movdqu (%rax,%rdx), %xmm0 -> F3 0F 6F 04 10 (through the GAS layer:
    // the load direction of the movdq family)
    let va: X86Asm = x86_resolve(x86_gas_emit(x86_asm_new(), "movdqu", "(%rax,%rdx), %xmm0"));
    if (va.text.len() != 5 || va.text[0] as i32 != 243 || va.text[1] as i32 != 15 || va.text[2] as i32 != 111 || va.text[3] as i32 != 4 || va.text[4] as i32 != 16) { return 111; }
    // movdqu (%rax), %xmm0 -> F3 0F 6F 00
    let vb: X86Asm = x86_resolve(x86_gas_emit(x86_asm_new(), "movdqu", "(%rax), %xmm0"));
    if (vb.text.len() != 4 || vb.text[0] as i32 != 243 || vb.text[1] as i32 != 15 || vb.text[2] as i32 != 111 || vb.text[3] as i32 != 0) { return 112; }
    // movdqu (%r8,%r9), %xmm3 -> F3 43 0F 6F 1C 08 (REX.X and REX.B both set)
    let vc: X86Asm = x86_resolve(x86_gas_emit(x86_asm_new(), "movdqu", "(%r8,%r9), %xmm3"));
    if (vc.text.len() != 6 || vc.text[0] as i32 != 243 || vc.text[1] as i32 != 67 || vc.text[2] as i32 != 15 || vc.text[3] as i32 != 111 || vc.text[4] as i32 != 28 || vc.text[5] as i32 != 8) { return 113; }
    // movdqu (%rax,%rdx), %xmm9 -> F3 44 0F 6F 0C 10 (REX.R for the xmm)
    let vd: X86Asm = x86_resolve(x86_gas_emit(x86_asm_new(), "movdqu", "(%rax,%rdx), %xmm9"));
    if (vd.text.len() != 6 || vd.text[0] as i32 != 243 || vd.text[1] as i32 != 68 || vd.text[2] as i32 != 15 || vd.text[3] as i32 != 111 || vd.text[4] as i32 != 12 || vd.text[5] as i32 != 16) { return 114; }
    // pcmpeqb %xmm1, %xmm0 -> 66 0F 74 C1 ; %xmm9,%xmm10 -> 66 45 0F 74 D1
    let ve: i32[] = x86_sse_any_rr([], 102, 116, 0, 1);
    if (ve.len() != 4 || ve[0] != 102 || ve[1] != 15 || ve[2] != 116 || ve[3] != 193) { return 115; }
    let vf: i32[] = x86_sse_any_rr([], 102, 116, 10, 9);
    if (vf.len() != 5 || vf[0] != 102 || vf[1] != 69 || vf[2] != 15 || vf[3] != 116 || vf[4] != 209) { return 116; }
    // punpcklbw %xmm1,%xmm1 -> 66 0F 60 C9 ; punpcklwd -> 66 0F 61 C9
    let vg: i32[] = x86_sse_any_rr([], 102, 96, 1, 1);
    if (vg.len() != 4 || vg[0] != 102 || vg[1] != 15 || vg[2] != 96 || vg[3] != 201) { return 117; }
    let vh: i32[] = x86_sse_any_rr([], 102, 97, 1, 1);
    if (vh.len() != 4 || vh[0] != 102 || vh[1] != 15 || vh[2] != 97 || vh[3] != 201) { return 118; }
    let vi: i32[] = x86_sse_any_rr([], 102, 96, 10, 9);
    if (vi.len() != 5 || vi[0] != 102 || vi[1] != 69 || vi[2] != 15 || vi[3] != 96 || vi[4] != 209) { return 119; }
    // pmovmskb %xmm0,%eax -> 66 0F D7 C0 ; %xmm0,%r9d -> 66 44 0F D7 C8 ;
    // %xmm10,%ecx -> 66 41 0F D7 CA (REX.R is the GPR, REX.B the xmm)
    let vj: i32[] = x86_sse_any_rr([], 102, 215, x86_rax(), 0);
    if (vj.len() != 4 || vj[0] != 102 || vj[1] != 15 || vj[2] != 215 || vj[3] != 192) { return 120; }
    let vk: i32[] = x86_sse_any_rr([], 102, 215, 9, 0);
    if (vk.len() != 5 || vk[0] != 102 || vk[1] != 68 || vk[2] != 15 || vk[3] != 215 || vk[4] != 200) { return 121; }
    let vl: i32[] = x86_sse_any_rr([], 102, 215, x86_rcx(), 10);
    if (vl.len() != 5 || vl[0] != 102 || vl[1] != 65 || vl[2] != 15 || vl[3] != 215 || vl[4] != 202) { return 122; }
    // pshufd $0,%xmm1,%xmm1 -> 66 0F 70 C9 00 ; $27,%xmm9,%xmm10 -> 66 45 0F 70 D1 1B
    let vm: i32[] = x86_sse_any_rr([], 102, 112, 1, 1); vm = vm.append(0);
    if (vm.len() != 5 || vm[0] != 102 || vm[1] != 15 || vm[2] != 112 || vm[3] != 201 || vm[4] != 0) { return 123; }
    let vn: i32[] = x86_sse_any_rr([], 102, 112, 10, 9); vn = vn.append(27);
    if (vn.len() != 6 || vn[0] != 102 || vn[1] != 69 || vn[2] != 15 || vn[3] != 112 || vn[4] != 209 || vn[5] != 27) { return 124; }
    // bsfl %eax,%ecx -> 0F BC C8 (no REX) ; %r9d,%r9d -> 45 0F BC C9
    let vo: i32[] = x86_bsf_r32([], x86_rcx(), x86_rax());
    if (vo.len() != 3 || vo[0] != 15 || vo[1] != 188 || vo[2] != 200) { return 125; }
    let vp: i32[] = x86_bsf_r32([], 9, 9);
    if (vp.len() != 4 || vp[0] != 69 || vp[1] != 15 || vp[2] != 188 || vp[3] != 201) { return 126; }
    // xorpd uses the same 66 0F shape: %xmm1,%xmm0 -> 66 0F 57 C1.
    let vq: i32[] = x86_sse_any_rr([], 102, 87, 0, 1);
    if (vq.len() != 4 || vq[0] != 102 || vq[1] != 15 || vq[2] != 87 || vq[3] != 193) { return 127; }
    // bsr, the one instruction separating __rmemchr's vector body from
    // __memchr's: the HIGHEST set mask bit is the rightmost matching byte.
    // Same shape as bsf one opcode along, so both are checked together — a
    // transposed pair would otherwise read plausibly at either call site.
    // bsrl %eax,%ecx -> 0F BD C8 (no REX) ; %r9d,%r9d -> 45 0F BD C9
    let vr: i32[] = x86_bsr_r32([], x86_rcx(), x86_rax());
    if (vr.len() != 3 || vr[0] != 15 || vr[1] != 189 || vr[2] != 200) { return 128; }
    let vs: i32[] = x86_bsr_r32([], 9, 9);
    if (vs.len() != 4 || vs[0] != 69 || vs[1] != 15 || vs[2] != 189 || vs[3] != 201) { return 129; }
    // bsrl %ecx,%r10d -> 44 0F BD D1 (REX.R for the extended DESTINATION)
    let vt: i32[] = x86_bsr_r32([], 10, x86_rcx());
    if (vt.len() != 4 || vt[0] != 68 || vt[1] != 15 || vt[2] != 189 || vt[3] != 209) { return 130; }
    return 0;
}

function x86enc_selftest_12(): i32 {
    // Width-generic encoders (#7893). Every expectation is GNU as output.
    // addw %ax, %bx -> 66 01 C3 (66 prefix from size 16, byte-op | 1)
    let wa: i32[] = x86_op_rr([], 16, 0, 0, 3);
    if (wa.len() != 3 || wa[0] != 102 || wa[1] != 1 || wa[2] != 195) { return 131; }
    // addw %r8w, %ax -> 66 44 01 C0 (REX.R after the 66)
    let wb: i32[] = x86_op_rr([], 16, 0, 8, 0);
    if (wb.len() != 4 || wb[0] != 102 || wb[1] != 68 || wb[2] != 1 || wb[3] != 192) { return 132; }
    // addq %rcx, %r9 -> 49 01 C9 (REX.B for the rm)
    let wc: i32[] = x86_op_rr([], 64, 0, 1, 9);
    if (wc.len() != 3 || wc[0] != 73 || wc[1] != 1 || wc[2] != 201) { return 133; }
    // addq $6, %rax -> 48 83 C0 06 (the 83 short-immediate selection)
    let wd: i32[] = x86_grp1_imm_r([], 64, 0, x86_rax(), 6);
    if (wd.len() != 4 || wd[0] != 72 || wd[1] != 131 || wd[2] != 192 || wd[3] != 6) { return 134; }
    // subw $300, %bx -> 66 81 EB 2C 01 (iw, not id)
    let we: i32[] = x86_grp1_imm_r([], 16, 5, 3, 300);
    if (we.len() != 5 || we[0] != 102 || we[1] != 129 || we[2] != 235 || we[3] != 44 || we[4] != 1) { return 135; }
    // andb $15, %sil -> 40 80 E6 0F (forced empty REX for sil, byte opcode)
    let wf: i32[] = x86_grp1_imm_r([], 8, 4, 6, 15);
    if (wf.len() != 4 || wf[0] != 64 || wf[1] != 128 || wf[2] != 230 || wf[3] != 15) { return 136; }
    // notw %dx -> 66 F7 D2 ; mulb %cl -> F6 E1 ; incb %spl -> 40 FE C4
    let wg: i32[] = x86_unary_r([], 16, 246, 2, 2);
    if (wg.len() != 3 || wg[0] != 102 || wg[1] != 247 || wg[2] != 210) { return 137; }
    let wh: i32[] = x86_unary_r([], 8, 246, 4, 1);
    if (wh.len() != 2 || wh[0] != 246 || wh[1] != 225) { return 138; }
    let wi: i32[] = x86_unary_r([], 8, 254, 0, 4);
    if (wi.len() != 3 || wi[0] != 64 || wi[1] != 254 || wi[2] != 196) { return 139; }
    // shlq $1, %rax -> 48 D1 E0 (the D1 shift-by-one form gas selects)
    let wj: i32[] = x86_shift_r_imm([], 64, 4, x86_rax(), 1);
    if (wj.len() != 3 || wj[0] != 72 || wj[1] != 209 || wj[2] != 224) { return 140; }
    // shrb %cl, %bl -> D2 EB
    let wk: i32[] = x86_shift_r_cl([], 8, 5, 3);
    if (wk.len() != 2 || wk[0] != 210 || wk[1] != 235) { return 141; }
    // testw $0x1234, %cx -> 66 F7 C1 34 12
    let wl: i32[] = x86_test_imm_r([], 16, 1, 4660);
    if (wl.len() != 5 || wl[0] != 102 || wl[1] != 247 || wl[2] != 193 || wl[3] != 52 || wl[4] != 18) { return 142; }
    // cmovne %r8d, %ecx -> 41 0F 45 C8 (32-bit width, REX.B)
    let wm: i32[] = x86_cmovcc_rr([], 32, 5, 1, 8);
    if (wm.len() != 4 || wm[0] != 65 || wm[1] != 15 || wm[2] != 69 || wm[3] != 200) { return 143; }
    // pextrq $2, %xmm1, %rax -> 66 48 0F 3A 16 C8 02 (REX.W in the 3A form)
    let wn: i32[] = x86_sse_3a_imm([], 1, 22, 1, 0, 2);
    if (wn.len() != 7 || wn[0] != 102 || wn[1] != 72 || wn[2] != 15 || wn[3] != 58 || wn[4] != 22 || wn[5] != 200 || wn[6] != 2) { return 144; }
    return 0;
}

// Every register name at every width decodes to its number, only at its own
// width, with or without the %; near-misses are rejected.
function x86enc_selftest_13(): i32 {
    let q: string[] = ["rax", "rcx", "rdx", "rbx", "rsp", "rbp", "rsi", "rdi", "r8", "r9", "r10", "r11", "r12", "r13", "r14", "r15"];
    let d: string[] = ["eax", "ecx", "edx", "ebx", "esp", "ebp", "esi", "edi", "r8d", "r9d", "r10d", "r11d", "r12d", "r13d", "r14d", "r15d"];
    let w: string[] = ["ax", "cx", "dx", "bx", "sp", "bp", "si", "di", "r8w", "r9w", "r10w", "r11w", "r12w", "r13w", "r14w", "r15w"];
    let b: string[] = ["al", "cl", "dl", "bl", "spl", "bpl", "sil", "dil", "r8b", "r9b", "r10b", "r11b", "r12b", "r13b", "r14b", "r15b"];
    let i: i32 = 0;
    while (i < 16) {
        if (x86_gas_reg64(q[i]) != i || x86_gas_reg64("%" + q[i]) != i || x86_gas_reg(q[i]) != i) { return 145; }
        if (x86_gas_reg32(d[i]) != i || x86_gas_reg32("%" + d[i]) != i || x86_gas_reg(d[i]) != i) { return 146; }
        if (x86_gas_reg16(w[i]) != i || x86_gas_reg16("%" + w[i]) != i) { return 147; }
        if (x86_gas_reg8(b[i]) != i || x86_gas_reg8("%" + b[i]) != i) { return 148; }
        if (x86_gas_reg64(d[i]) != (0 - 1) || x86_gas_reg32(q[i]) != (0 - 1)) { return 149; }
        if (x86_gas_reg16(b[i]) != (0 - 1) || x86_gas_reg8(w[i]) != (0 - 1) || x86_gas_reg(w[i]) != (0 - 1)) { return 150; }
        if (x86_gas_reg_w(b[i], 8) != i || x86_gas_reg_w(w[i], 16) != i || x86_gas_reg_w(d[i], 32) != i || x86_gas_reg_w(q[i], 64) != i) { return 151; }
        i = i + 1;
    }
    let bad: string[] = ["", "%", "r", "e", "ah", "bh", "r7", "r1", "r16", "r08", "r8x", "r15dd", "rsl", "eal", "rip", "%%rax", "raxx", "xmm0"];
    for tok in bad {
        if (x86_gas_reg_decode(tok) != (0 - 1)) { return 152; }
    }
    return 0;
}

function main(): i32 {
    let r: i32 = 0;
    r = x86enc_selftest_1(); if (r != 0) { return r; }
    r = x86enc_selftest_2(); if (r != 0) { return r; }
    r = x86enc_selftest_3(); if (r != 0) { return r; }
    r = x86enc_selftest_4(); if (r != 0) { return r; }
    r = x86enc_selftest_5(); if (r != 0) { return r; }
    r = x86enc_selftest_6(); if (r != 0) { return r; }
    r = x86enc_selftest_7(); if (r != 0) { return r; }
    r = x86enc_selftest_8(); if (r != 0) { return r; }
    r = x86enc_selftest_9(); if (r != 0) { return r; }
    r = x86enc_selftest_10(); if (r != 0) { return r; }
    r = x86enc_selftest_11(); if (r != 0) { return r; }
    r = x86enc_selftest_12(); if (r != 0) { return r; }
    r = x86enc_selftest_13(); if (r != 0) { return r; }
    return 0;
}
`

// x86ElfExitDriverMain assembles exit(42) (mov edi,42 ; mov eax,60 ;
// syscall), wraps it in a static x86-64 ELF, and writes the raw binary to
// stdout for the Go test to run natively.
const x86ElfExitDriverMain = `
function main(): i32 {
    let code: i32[] = [];
    code = x86_mov_r32_imm32(code, x86_rdi(), 42); // exit code
    code = x86_mov_r32_imm32(code, x86_rax(), 60); // __NR_exit
    code = x86_syscall(code);
    let bin: i32[] = elf_static_executable_x86(code); // R+X, text-only
    write(string_from_bytes_unchecked(to_u8(bin)));
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
    let code: i32[] = [];
    code = x86_mov_r32_imm32(code, x86_rax(), 0); // acc = 0
    code = x86_mov_r32_imm32(code, x86_rcx(), 7); // counter = 7
    let loop_off: i32 = code.len();               // backward-branch target
    code = x86_add_r64_imm32(code, x86_rax(), 6);  // acc += 6
    code = x86_sub_r64_imm32(code, x86_rcx(), 1);  // counter -= 1
    code = x86_cmp_r64_imm32(code, x86_rcx(), 0);  // counter == 0 ?
    let jne_off: i32 = code.len();
    let rel: i32 = x86_branch_rel(loop_off, jne_off, 6);
    code = x86_jne_rel32(code, rel);               // loop while counter != 0
    code = x86_mov_r64_r64(code, x86_rdi(), x86_rax()); // exit code = acc
    code = x86_mov_r32_imm32(code, x86_rax(), 60);  // __NR_exit
    code = x86_syscall(code);
    let bin: i32[] = elf_static_executable_x86(code);
    write(string_from_bytes_unchecked(to_u8(bin)));
    return 0;
}
`

// x86ElfMaxDriverMain assembles max(42, 17) using a FORWARD conditional
// branch (jge skips the else-arm), resolved via a placeholder + patch:
//
//	eax = 42 ; ecx = 17 ; cmp eax, ecx ; jge done ; eax = ecx ; done:
//	exit(eax)
//
// The jge's rel32 is emitted as 0, its field offset recorded, then patched
// to reach `done` once that offset is known.
const x86ElfMaxDriverMain = `
function main(): i32 {
    let code: i32[] = [];
    code = x86_mov_r32_imm32(code, x86_rax(), 42); // a
    code = x86_mov_r32_imm32(code, x86_rcx(), 17); // b
    code = x86_cmp_r64_r64(code, x86_rax(), x86_rcx());
    let jge_off: i32 = code.len();                 // branch opcode offset
    code = x86_jcc_rel32(code, x86_cc_ge(), 0);    // placeholder rel32
    let patch_off: i32 = jge_off + 2;              // rel32 field (after 0F 8D)
    code = x86_mov_r64_r64(code, x86_rax(), x86_rcx()); // else: a = b
    let done_off: i32 = code.len();                // forward-branch target
    code = x86_patch_rel32(code, patch_off, x86_rel_to(done_off, patch_off));
    code = x86_mov_r64_r64(code, x86_rdi(), x86_rax()); // exit code = max
    code = x86_mov_r32_imm32(code, x86_rax(), 60);
    code = x86_syscall(code);
    let bin: i32[] = elf_static_executable_x86(code);
    write(string_from_bytes_unchecked(to_u8(bin)));
    return 0;
}
`

// x86ElfCallDriverMain assembles a forward call + ret:
//
//	call setval ; mov rdi, rax ; exit ; setval: mov eax, 42 ; ret
//
// The call targets a subroutine defined after the call site, so its rel32
// is a patched forward reference.
const x86ElfCallDriverMain = `
function main(): i32 {
    let code: i32[] = [];
    let call_off: i32 = code.len();
    code = x86_call_rel32(code, 0);               // placeholder -> setval
    let patch_off: i32 = call_off + 1;            // rel32 field (after E8)
    code = x86_mov_r64_r64(code, x86_rdi(), x86_rax()); // exit code = result
    code = x86_mov_r32_imm32(code, x86_rax(), 60);
    code = x86_syscall(code);
    let setval_off: i32 = code.len();             // subroutine entry
    code = x86_patch_rel32(code, patch_off, x86_rel_to(setval_off, patch_off));
    code = x86_mov_r32_imm32(code, x86_rax(), 42); // setval: result = 42
    code = x86_ret(code);
    let bin: i32[] = elf_static_executable_x86(code);
    write(string_from_bytes_unchecked(to_u8(bin)));
    return 0;
}
`

// x86LabelsSelfTestMain byte-checks the named-label assembler. Each
// `return N` is a distinct failing-check id (0 = all pass). 0x0F=15,
// 0x8D=141 (jge), 0x85=133 (jne), 0xE8=232 (call); rel32s: forward jge to
// done=22 with field at 15 -> 3; backward jne to loop=0 with field at 9 ->
// -13 (0xF3,FF,FF,FF = 243,255,255,255); forward call to sub=6 with field
// at 1 -> 1.
const x86LabelsSelfTestMain = `
function main(): i32 {
    // forward conditional: cmp then jge done (placeholder, resolved later).
    let a: X86Asm = x86_asm_new();
    x86_push_arr(a.code, x86_mov_r32_imm32([], x86_rax(), 42));
    x86_push_arr(a.code, x86_mov_r32_imm32([], x86_rcx(), 17));
    x86_push_arr(a.code, x86_cmp_r64_r64([], x86_rax(), x86_rcx()));
    a = x86_jcc_label(a, x86_cc_ge(), "done");
    x86_push_arr(a.code, x86_mov_r64_r64([], x86_rax(), x86_rcx()));
    a = x86_label(a, "done");
    a = x86_resolve(a);
    if (a.text.len() != 22 || a.text[13] as i32 != 15 || a.text[14] as i32 != 141) { return 1; }
    if (a.text[15] as i32 != 3 || a.text[16] as i32 != 0 || a.text[17] as i32 != 0 || a.text[18] as i32 != 0) { return 2; }

    // backward conditional: label loop, body, jne loop, resolve.
    let b: X86Asm = x86_asm_new();
    b = x86_label(b, "loop");
    x86_push_arr(b.code, x86_add_r64_imm32([], x86_rax(), 6));
    b = x86_jcc_label(b, x86_cc_ne(), "loop");
    b = x86_resolve(b);
    if (b.text.len() != 13 || b.text[7] as i32 != 15 || b.text[8] as i32 != 133) { return 3; }
    if (b.text[9] as i32 != 243 || b.text[10] as i32 != 255 || b.text[11] as i32 != 255 || b.text[12] as i32 != 255) { return 4; }

    // forward call: call sub, ret, label sub, resolve.
    let c: X86Asm = x86_asm_new();
    c = x86_call_label(c, "sub");
    x86_push_arr(c.code, x86_ret([]));
    c = x86_label(c, "sub");
    c = x86_resolve(c);
    if (c.text.len() != 6 || c.text[0] as i32 != 232 || c.text[1] as i32 != 1 || c.text[2] as i32 != 0) { return 5; }

    // label lookup: defined vs missing.
    if (x86_label_off(c, "sub") != 6) { return 6; }
    if (x86_label_off(c, "nope") != (0 - 1)) { return 7; }

    // rip-relative lea: lea rax, [rip+d] -> 48 8D 05 <d>, resolved against
    // a .rodata quad: lea(7)+mov(3)=10 text, padded 16, S0 at 16;
    // disp32 = 16 - (3+4) = 9.
    let d: X86Asm = x86_asm_new();
    d = x86_lea_rip_label(d, x86_rax(), "S0");
    x86_push_arr(d.code, x86_mov_load_r64([], x86_rax(), x86_rax(), 0));
    d = x86_rodata_label(d, "S0");
    d = x86_rodata_quad(d, 42i64);
    d = x86_resolve(d);
    if (d.text.len() != 10 || d.text[0] as i32 != 72 || d.text[1] as i32 != 141 || d.text[2] as i32 != 5 || d.text[3] as i32 != 9 || d.text[4] as i32 != 0 || d.text[5] as i32 != 0 || d.text[6] as i32 != 0) { return 8; }
    if (d.rodata.len() != 8 || d.rodata[0] != 42 || d.rodata[1] != 0 || d.rodata[7] != 0) { return 11; }
    // x86_align8 rounds up to the .text/.rodata boundary.
    if (x86_align8(10) != 16 || x86_align8(16) != 16 || x86_align8(0) != 0) { return 12; }
    // rip-relative movq load/store: movq G(%rip),%rax -> 48 8B 05 <d>;
    // movq %rcx,G(%rip) -> 48 89 0D <d>.
    let e2: X86Asm = x86_asm_new();
    e2 = x86_mov_load_rip_label(e2, x86_rax(), "G");
    e2 = x86_mov_store_rip_label(e2, x86_rcx(), "G");
    e2 = x86_resolve(e2);
    if (e2.text.len() != 14 || e2.text[0] as i32 != 72 || e2.text[1] as i32 != 139 || e2.text[2] as i32 != 5) { return 13; }
    if (e2.text[7] as i32 != 72 || e2.text[8] as i32 != 137 || e2.text[9] as i32 != 13) { return 14; }

    // A repeated label name resolves to its FIRST definition. The label table
    // is bucket-indexed, and a bucket that chained newest-first would answer
    // with the second one — so this pins the insertion-order walk, not a
    // property of the input.
    let f: X86Asm = x86_asm_new();
    x86_push_arr(f.code, x86_ret([]));
    f = x86_label(f, "dup");
    x86_push_arr(f.code, x86_ret([]));
    f = x86_label(f, "dup");
    if (x86_label_off(f, "dup") != 1) { return 15; }

    // 676 names spread across the buckets: every one must still be found, and
    // a name that was never placed must still miss. One byte of .text per
    // label, so "aa" sits at 1 and the k-th name at k+1.
    let g: X86Asm = x86_asm_new();
    let alpha: string = "abcdefghijklmnopqrstuvwxyz";
    let i: i32 = 0;
    while (i < 26) {
        let j: i32 = 0;
        while (j < 26) {
            buf_push_byte(g.code, 0);
            g = x86_label(g, slice_unchecked(alpha, i, i + 1) + slice_unchecked(alpha, j, j + 1));
            j = j + 1;
        }
        i = i + 1;
    }
    if (g.lab_names.len() != 676) { return 16; }
    if (x86_label_off(g, "aa") != 1) { return 17; }
    if (x86_label_off(g, "zz") != 676) { return 18; }
    if (x86_label_off(g, "mm") != 325) { return 19; }
    if (x86_label_off(g, "zzz") != (0 - 1)) { return 20; }
    buf_free(f.code);
    buf_free(g.code);
    return 0;
}
`

// x86ElfLabelDriverMain assembles, via the named-label API, a two-routine
// program: main calls compute (forward call), which loops acc += 6 seven
// times (backward jne to a label) and returns; main exits with the result
// (42). Both references resolve by name through x86_resolve.
const x86ElfLabelDriverMain = `
function main(): i32 {
    let a: X86Asm = x86_asm_new();
    a = x86_call_label(a, "compute");              // forward call
    x86_push_arr(a.code, x86_mov_r64_r64([], x86_rdi(), x86_rax())); // exit code = result
    x86_push_arr(a.code, x86_mov_r32_imm32([], x86_rax(), 60));
    x86_push_arr(a.code, x86_syscall([]));
    a = x86_label(a, "compute");
    x86_push_arr(a.code, x86_mov_r32_imm32([], x86_rax(), 0)); // acc = 0
    x86_push_arr(a.code, x86_mov_r32_imm32([], x86_rcx(), 7)); // counter = 7
    a = x86_label(a, "loop");
    x86_push_arr(a.code, x86_add_r64_imm32([], x86_rax(), 6)); // acc += 6
    x86_push_arr(a.code, x86_sub_r64_imm32([], x86_rcx(), 1)); // counter -= 1
    x86_push_arr(a.code, x86_cmp_r64_imm32([], x86_rcx(), 0));
    a = x86_jcc_label(a, x86_cc_ne(), "loop");     // backward branch
    x86_push_arr(a.code, x86_ret([]));
    a = x86_resolve(a);
    let bin: i32[] = elf_static_executable_x86(elf_cat_u8([], a.text));
    write(string_from_bytes_unchecked(to_u8(bin)));
    return 0;
}
`

// x86ElfFrameDriverMain assembles a stack-frame round-trip:
//
//	push rbp ; mov rbp, rsp ; sub rsp, 16
//	rax = 42 ; [rbp-8] = rax ; rax = 0 ; rax = [rbp-8]
//	mov rsp, rbp ; pop rbp ; exit(rax)
//
// The value survives only via the store/reload through [rbp-8], so a wrong
// memory encoding would not exit 42.
const x86ElfFrameDriverMain = `
function main(): i32 {
    let code: i32[] = [];
    code = x86_push_r64(code, x86_rbp());
    code = x86_mov_r64_r64(code, x86_rbp(), x86_rsp());          // mov rbp, rsp
    code = x86_sub_r64_imm32(code, x86_rsp(), 16);              // sub rsp, 16
    code = x86_mov_r32_imm32(code, x86_rax(), 42);
    code = x86_mov_store_r64(code, x86_rbp(), 0 - 8, x86_rax()); // [rbp-8] = rax
    code = x86_mov_r32_imm32(code, x86_rax(), 0);               // clobber
    code = x86_mov_load_r64(code, x86_rax(), x86_rbp(), 0 - 8);  // rax = [rbp-8]
    code = x86_mov_r64_r64(code, x86_rsp(), x86_rbp());          // mov rsp, rbp
    code = x86_pop_r64(code, x86_rbp());
    code = x86_mov_r64_r64(code, x86_rdi(), x86_rax());          // exit code = rax
    code = x86_mov_r32_imm32(code, x86_rax(), 60);
    code = x86_syscall(code);
    let bin: i32[] = elf_static_executable_x86(code);
    write(string_from_bytes_unchecked(to_u8(bin)));
    return 0;
}
`

// x86ElfRodataDriverMain assembles a program that reads a .rodata constant
// via rip-relative addressing:
//
//	lea rax, [rip+answer] ; rax = [rax] ; exit(rax) ; .rodata answer: .quad 42
//
// The rip displacement and the .rodata base (padded .text length) are
// resolved by x86_resolve, and the image is built with the R+W+X data ELF.
const x86ElfRodataDriverMain = `
function main(): i32 {
    let a: X86Asm = x86_asm_new();
    a = x86_lea_rip_label(a, x86_rax(), "answer");          // rax = &answer
    x86_push_arr(a.code, x86_mov_load_r64([], x86_rax(), x86_rax(), 0)); // rax = *answer
    x86_push_arr(a.code, x86_mov_r64_r64([], x86_rdi(), x86_rax()));  // exit code = answer
    x86_push_arr(a.code, x86_mov_r32_imm32([], x86_rax(), 60));
    x86_push_arr(a.code, x86_syscall([]));
    a = x86_rodata_label(a, "answer");
    a = x86_rodata_quad(a, 42i64);                          // .quad 42
    a = x86_resolve(a);
    let bin: i32[] = elf_static_executable_data_x86(a.text, a.rodata);
    write(string_from_bytes_unchecked(to_u8(bin)));
    return 0;
}
`
