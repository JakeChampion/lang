package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestSelfHostX86Encode exercises the self-hosted x86-64 machine-code
// encoding primitives (examples/self_host/x86_native.fern) — slice 2a of
// the native binary backend (the assembler half; the container half is
// elf.fern). It mirrors internal/native/x86_64/asm.go's byte emission.
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
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	nat, err := os.ReadFile("../../examples/self_host/x86_native.fern")
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
// forward branch (patched by x86_resolve), backward branch (patched
// immediately), forward call, and label lookup. Same wasm self-test shape
// as TestSelfHostX86Encode.
func TestSelfHostX86Labels(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host x86 labels e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	nat, err := os.ReadFile("../../examples/self_host/x86_native.fern")
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
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	nat, err := os.ReadFile("../../examples/self_host/x86_native.fern")
	if err != nil {
		t.Fatalf("read x86_native.fern: %v", err)
	}
	elf, err := os.ReadFile("../../examples/self_host/elf.fern")
	if err != nil {
		t.Fatalf("read elf.fern: %v", err)
	}
	source := string(nat) + "\n" + string(elf) + "\n" + driverMain

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
    return 0;
}

function x86enc_selftest_2(): i32 {
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
    // call rel=10 -> E8 0A 00 00 00
    var q: i32[] = x86_call_rel32([], 10);
    if (q.len() != 5 || q[0] != 232 || q[1] != 10 || q[2] != 0 || q[3] != 0 || q[4] != 0) { return 19; }
    // forward-ref rel: target 22, rel32 field at 15 -> 3.
    if (x86_rel_to(22, 15) != 3) { return 20; }
    return 0;
}

function x86enc_selftest_3(): i32 {
    // patch a placeholder: jne rel=0 then patch its rel32 (offset 2) to 3.
    var r: i32[] = x86_jne_rel32([], 0);
    r = x86_patch_rel32(r, 2, 3);
    if (r.len() != 6 || r[0] != 15 || r[1] != 133 || r[2] != 3 || r[3] != 0 || r[4] != 0 || r[5] != 0) { return 21; }
    // patch a negative rel (-27) -> E5 FF FF FF.
    var s: i32[] = x86_jmp_rel32([], 0);
    s = x86_patch_rel32(s, 1, 0 - 27);
    if (s.len() != 5 || s[0] != 233 || s[1] != 229 || s[2] != 255 || s[3] != 255 || s[4] != 255) { return 22; }
    // mov rax, [rbp-8] -> 48 8B 45 F8 (mod=01 disp8, rm=rbp)
    var t: i32[] = x86_mov_load_r64([], x86_rax(), x86_rbp(), 0 - 8);
    if (t.len() != 4 || t[0] != 72 || t[1] != 139 || t[2] != 69 || t[3] != 248) { return 23; }
    // mov [rbp-8], rax -> 48 89 45 F8
    var u: i32[] = x86_mov_store_r64([], x86_rbp(), 0 - 8, x86_rax());
    if (u.len() != 4 || u[0] != 72 || u[1] != 137 || u[2] != 69 || u[3] != 248) { return 24; }
    // mov rax, [rsp+16] -> 48 8B 44 24 10 (SIB escape for rsp)
    var v2: i32[] = x86_mov_load_r64([], x86_rax(), x86_rsp(), 16);
    if (v2.len() != 5 || v2[0] != 72 || v2[1] != 139 || v2[2] != 68 || v2[3] != 36 || v2[4] != 16) { return 25; }
    // mov rax, [rcx] -> 48 8B 01 (mod=00, no disp)
    var w: i32[] = x86_mov_load_r64([], x86_rax(), x86_rcx(), 0);
    if (w.len() != 3 || w[0] != 72 || w[1] != 139 || w[2] != 1) { return 26; }
    // mov rax, [rbp] -> 48 8B 45 00 (rbp forces mod=01 disp8=0)
    var x: i32[] = x86_mov_load_r64([], x86_rax(), x86_rbp(), 0);
    if (x.len() != 4 || x[0] != 72 || x[1] != 139 || x[2] != 69 || x[3] != 0) { return 27; }
    // mov rax, [rcx+512] -> 48 8B 81 00 02 00 00 (mod=10 disp32)
    var y: i32[] = x86_mov_load_r64([], x86_rax(), x86_rcx(), 512);
    if (y.len() != 7 || y[0] != 72 || y[1] != 139 || y[2] != 129 || y[3] != 0 || y[4] != 2 || y[5] != 0 || y[6] != 0) { return 28; }
    // sib byte for [rsp]: scale=0,index=4,base=4 -> 0x24 (36)
    if (x86_sib(0, 4, 4) != 36) { return 29; }
    // slice 2h integer ops (verified vs as/objdump):
    var aa: i32[] = x86_inc_r64([], x86_rax());   // 48 FF C0
    if (aa.len() != 3 || aa[0] != 72 || aa[1] != 255 || aa[2] != 192) { return 30; }
    return 0;
}

function x86enc_selftest_4(): i32 {
    var bb: i32[] = x86_dec_r64([], x86_rcx());   // 48 FF C9
    if (bb[2] != 201) { return 31; }
    var cc2: i32[] = x86_neg_r64([], x86_rax());  // 48 F7 D8
    if (cc2[1] != 247 || cc2[2] != 216) { return 32; }
    var dd: i32[] = x86_test_r64_r64([], x86_rax(), x86_rax()); // 48 85 C0
    if (dd[1] != 133 || dd[2] != 192) { return 33; }
    var ee: i32[] = x86_and_r64_r64([], x86_rax(), x86_rcx());  // 48 21 C8
    if (ee[1] != 33 || ee[2] != 200) { return 34; }
    var ff: i32[] = x86_or_r64_r64([], x86_rax(), x86_rcx());   // 48 09 C8
    if (ff[1] != 9 || ff[2] != 200) { return 35; }
    var gg: i32[] = x86_xor_r64_r64([], x86_rax(), x86_rcx());  // 48 31 C8
    if (gg[1] != 49 || gg[2] != 200) { return 36; }
    var hh: i32[] = x86_imul_r64_r64([], x86_rax(), x86_rcx()); // 48 0F AF C1
    if (hh.len() != 4 || hh[1] != 15 || hh[2] != 175 || hh[3] != 193) { return 37; }
    var ii: i32[] = x86_idiv_r64([], x86_rcx());  // 48 F7 F9
    if (ii[1] != 247 || ii[2] != 249) { return 38; }
    var jj: i32[] = x86_div_r64([], x86_rcx());   // 48 F7 F1
    if (jj[2] != 241) { return 39; }
    var kk: i32[] = x86_cqo([]);                  // 48 99
    if (kk.len() != 2 || kk[0] != 72 || kk[1] != 153) { return 40; }
    return 0;
}

function x86enc_selftest_5(): i32 {
    var ll: i32[] = x86_shl_r64_imm8([], x86_rax(), 3); // 48 C1 E0 03
    if (ll.len() != 4 || ll[1] != 193 || ll[2] != 224 || ll[3] != 3) { return 41; }
    // slice 2i extended registers r8..r15 (verified vs as/objdump):
    if (x86_rex(1, 0, 0, 0) != 72 || x86_rex(1, 12, 0, 13) != 77 || x86_rex(0, 0, 0, 12) != 65) { return 42; }
    var ra: i32[] = x86_mov_r64_r64([], 13, 12); // mov r13,r12 -> 4D 89 E5
    if (ra.len() != 3 || ra[0] != 77 || ra[1] != 137 || ra[2] != 229) { return 43; }
    var rb: i32[] = x86_add_r64_r64([], x86_rax(), 12); // add rax,r12 -> 4C 01 E0
    if (rb[0] != 76 || rb[1] != 1 || rb[2] != 224) { return 44; }
    var rc: i32[] = x86_mov_r64_r64([], 8, x86_rax()); // mov r8,rax -> 49 89 C0
    if (rc[0] != 73 || rc[1] != 137 || rc[2] != 192) { return 45; }
    var rd: i32[] = x86_push_r64([], 12); // push r12 -> 41 54
    if (rd.len() != 2 || rd[0] != 65 || rd[1] != 84) { return 46; }
    var re: i32[] = x86_pop_r64([], 13); // pop r13 -> 41 5D
    if (re.len() != 2 || re[0] != 65 || re[1] != 93) { return 47; }
    var rf: i32[] = x86_inc_r64([], 12); // inc r12 -> 49 FF C4
    if (rf[0] != 73 || rf[1] != 255 || rf[2] != 196) { return 48; }
    var rg: i32[] = x86_mov_load_r64([], x86_rax(), 13, 0); // mov rax,[r13] -> 49 8B 45 00
    if (rg.len() != 4 || rg[0] != 73 || rg[1] != 139 || rg[2] != 69 || rg[3] != 0) { return 49; }
    var rh: i32[] = x86_mov_store_r64([], 12, 0, x86_rax()); // mov [r12],rax -> 49 89 04 24
    if (rh.len() != 4 || rh[0] != 73 || rh[1] != 137 || rh[2] != 4 || rh[3] != 36) { return 50; }
    return 0;
}

function x86enc_selftest_6(): i32 {
    var rk: i32[] = x86_imul_r64_r64([], 8, 9); // imul r8,r9 -> 4D 0F AF C1
    if (rk.len() != 4 || rk[0] != 77 || rk[1] != 15 || rk[2] != 175 || rk[3] != 193) { return 51; }
    // slice 2j SIB-index addressing (verified vs as/objdump):
    var sa: i32[] = x86_mov_load_r64_idx([], x86_rax(), x86_rax(), x86_rcx(), 1, 0); // 48 8B 04 08
    if (sa.len() != 4 || sa[0] != 72 || sa[1] != 139 || sa[2] != 4 || sa[3] != 8) { return 52; }
    var sb: i32[] = x86_mov_load_r64_idx([], x86_rax(), x86_rax(), x86_rcx(), 8, 0); // 48 8B 04 C8
    if (sb[3] != 200) { return 53; }
    var sc3: i32[] = x86_mov_load_r64_idx([], x86_rax(), 12, 15, 1, 0); // 4B 8B 04 3C
    if (sc3[0] != 75 || sc3[1] != 139 || sc3[2] != 4 || sc3[3] != 60) { return 54; }
    var sd: i32[] = x86_mov_store_r64_idx([], 13, x86_rcx(), 1, 0, x86_rax()); // 49 89 44 0D 00
    if (sd.len() != 5 || sd[0] != 73 || sd[1] != 137 || sd[2] != 68 || sd[3] != 13 || sd[4] != 0) { return 55; }
    if (x86_scale_bits(1) != 0 || x86_scale_bits(2) != 1 || x86_scale_bits(4) != 2 || x86_scale_bits(8) != 3) { return 56; }
    // slice 2k byte ops (verified vs as/objdump):
    var ba: i32[] = x86_movb_imm_mem([], 6, false, 0, 1, 0, 48); // movb $48,(%rsi) -> C6 06 30
    if (ba.len() != 3 || ba[0] != 198 || ba[1] != 6 || ba[2] != 48) { return 57; }
    var bc: i32[] = x86_movb_imm_mem([], 13, false, 0, 1, 2, 105); // movb $105,2(%r13) -> 41 C6 45 02 69
    if (bc.len() != 5 || bc[0] != 65 || bc[1] != 198 || bc[2] != 69 || bc[3] != 2 || bc[4] != 105) { return 58; }
    var bd: i32[] = x86_movb_imm_mem([], 12, true, 3, 1, 0, 102); // movb $102,(%r12,%rbx,1) -> 41 C6 04 1C 66
    if (bd.len() != 5 || bd[0] != 65 || bd[1] != 198 || bd[2] != 4 || bd[3] != 28 || bd[4] != 102) { return 59; }
    var be: i32[] = x86_movb_reg_mem([], 0, 6, false, 0, 1, 0); // movb %al,(%rsi) -> 88 06
    if (be.len() != 2 || be[0] != 136 || be[1] != 6) { return 60; }
    return 0;
}

function x86enc_selftest_7(): i32 {
    var bf: i32[] = x86_movb_reg_mem([], 0, 11, false, 0, 1, 0); // movb %al,(%r11) -> 41 88 03
    if (bf.len() != 3 || bf[0] != 65 || bf[1] != 136 || bf[2] != 3) { return 61; }
    var bg: i32[] = x86_movzbq_mem([], x86_rax(), x86_rax(), false, 0, 1, 0); // movzbq (%rax),%rax -> 48 0F B6 00
    if (bg.len() != 4 || bg[0] != 72 || bg[1] != 15 || bg[2] != 182 || bg[3] != 0) { return 62; }
    var bh: i32[] = x86_movzbq_mem([], x86_rdx(), 13, false, 0, 1, 2); // movzbq 2(%r13),%rdx -> 49 0F B6 55 02
    if (bh.len() != 5 || bh[0] != 73 || bh[1] != 15 || bh[2] != 182 || bh[3] != 85 || bh[4] != 2) { return 63; }
    var bj: i32[] = x86_movzbq_reg([], x86_rcx(), 0); // movzbq %al,%rcx -> 48 0F B6 C8
    if (bj.len() != 4 || bj[0] != 72 || bj[1] != 15 || bj[2] != 182 || bj[3] != 200) { return 64; }
    var bk: i32[] = x86_cmpb_imm_reg8([], 1, 46); // cmpb $46,%cl -> 80 F9 2E
    if (bk.len() != 3 || bk[0] != 128 || bk[1] != 249 || bk[2] != 46) { return 65; }
    // setCC (slice 2m): setl %al -> 0F 9C C0; setge %al -> 0F 9D C0.
    var bl: i32[] = x86_setcc_reg8([], 156, 0);
    if (bl.len() != 3 || bl[0] != 15 || bl[1] != 156 || bl[2] != 192) { return 66; }
    var bm: i32[] = x86_setcc_reg8([], 157, 1); // setge %cl -> 0F 9D C1
    if (bm[1] != 157 || bm[2] != 193) { return 67; }
    // slice 2n SSE double (verified vs as/objdump):
    var sg: i32[] = x86_movq_gpr_to_xmm([], 1, 0); // movq %rax,%xmm1 -> 66 48 0F 6E C8
    if (sg.len() != 5 || sg[0] != 102 || sg[1] != 72 || sg[2] != 15 || sg[3] != 110 || sg[4] != 200) { return 68; }
    var sh: i32[] = x86_movq_xmm_to_gpr([], 0, 0); // movq %xmm0,%rax -> 66 48 0F 7E C0
    if (sh[3] != 126 || sh[4] != 192) { return 69; }
    var si2: i32[] = x86_sse_rr([], 94, 0, 1); // divsd %xmm1,%xmm0 -> F2 0F 5E C1
    if (si2.len() != 4 || si2[0] != 242 || si2[1] != 15 || si2[2] != 94 || si2[3] != 193) { return 70; }
    return 0;
}

function x86enc_selftest_8(): i32 {
    var sj: i32[] = x86_cvttsd2si([], 0, 0); // F2 48 0F 2C C0
    if (sj.len() != 5 || sj[0] != 242 || sj[1] != 72 || sj[2] != 15 || sj[3] != 44 || sj[4] != 192) { return 71; }
    var sk2: i32[] = x86_cvtsi2sd([], 0, 0); // F2 48 0F 2A C0
    if (sk2[3] != 42 || sk2[4] != 192) { return 72; }
    var sl: i32[] = x86_ucomisd([], 0, 1); // 66 0F 2E C1
    if (sl.len() != 4 || sl[0] != 102 || sl[1] != 15 || sl[2] != 46 || sl[3] != 193) { return 73; }
    // x86_le64_i64: 8 LE bytes of 0x00000000000000FF -> FF then 7 zeros.
    var sm2: i64 = 255;
    var sn: i32[] = x86_le64_i64([], sm2);
    if (sn.len() != 8 || sn[0] != 255 || sn[1] != 0 || sn[7] != 0) { return 74; }
    // push $imm (slice 2o): push $0 -> 68 00 00 00 00; push $42 -> 68 2A ...
    var so: i32[] = x86_push_imm32([], 0);
    if (so.len() != 5 || so[0] != 104 || so[1] != 0 || so[4] != 0) { return 75; }
    var sp2: i32[] = x86_push_imm32([], 42);
    if (sp2[0] != 104 || sp2[1] != 42) { return 76; }
    // movabsq (slice 2p): movabs %rax, 0x4000000000000000 (4611686018427387904)
    var mab: i64 = 4611686018427387904;
    var sq: i32[] = x86_movabsq([], x86_rax(), mab);
    // 48 B8 00 00 00 00 00 00 00 40
    if (sq.len() != 10 || sq[0] != 72 || sq[1] != 184 || sq[9] != 64) { return 77; }
    if (sq[2] != 0 || sq[8] != 0) { return 78; }
    // movabs %r8, 1 -> 49 B8 01 00.. (REX.W|B)
    var one64: i64 = 1;
    var sr: i32[] = x86_movabsq([], 8, one64);
    if (sr[0] != 73 || sr[1] != 184 || sr[2] != 1) { return 79; }
    // new jcc cc codes feed the same 0F 80+cc encoder: js rel=0 -> 0F 88 00*4.
    var sjs: i32[] = x86_jcc_rel32([], x86_cc_s(), 0);
    if (sjs.len() != 6 || sjs[0] != 15 || sjs[1] != 136) { return 80; }
    return 0;
}

function x86enc_selftest_9(): i32 {
    var sja: i32[] = x86_jcc_rel32([], x86_cc_a(), 0); // 0F 87
    if (sja[1] != 135) { return 81; }
    // slice 2q: 32-bit ALU / moves / extends / cmov / testb / rep / xorpd.
    var ta: i32[] = x86_alu_r32_imm32([], 0, x86_rax(), 128); // addl $128,%eax -> 81 C0 80 00 00 00
    if (ta.len() != 6 || ta[0] != 129 || ta[1] != 192 || ta[2] != 128 || ta[3] != 0) { return 82; }
    var tb: i32[] = x86_shr_r32_imm8([], x86_rax(), 8); // shrl $8,%eax -> C1 E8 08
    if (tb.len() != 3 || tb[0] != 193 || tb[1] != 232 || tb[2] != 8) { return 83; }
    var tc: i32[] = x86_mov_r32_r32([], x86_rax(), x86_rcx()); // movl %ecx,%eax -> 89 C8
    if (tc.len() != 2 || tc[0] != 137 || tc[1] != 200) { return 84; }
    var td: i32[] = x86_mov_r32_r32([], 15, x86_rax()); // movl %eax,%r15d -> 41 89 C7
    if (td.len() != 3 || td[0] != 65 || td[1] != 137 || td[2] != 199) { return 85; }
    var te: i32[] = x86_movl_load([], x86_rdi(), x86_rbp(), false, 0, 1, 0 - 76); // 8B 7D B4
    if (te.len() != 3 || te[0] != 139 || te[1] != 125 || te[2] != 180) { return 86; }
    var tf: i32[] = x86_testb_imm_reg8([], 0, 127); // testb $127,%al -> F6 C0 7F
    if (tf.len() != 3 || tf[0] != 246 || tf[1] != 192 || tf[2] != 127) { return 87; }
    var tg: i32[] = x86_testb_reg8_reg8([], 0, 0); // testb %al,%al -> 84 C0
    if (tg.len() != 2 || tg[0] != 132 || tg[1] != 192) { return 88; }
    var th: i32[] = x86_cmovcc_r64_r64([], x86_cc_l(), x86_rax(), x86_rdx()); // cmovl %rdx,%rax -> 48 0F 4C C2
    if (th.len() != 4 || th[0] != 72 || th[1] != 15 || th[2] != 76 || th[3] != 194) { return 89; }
    var ti: i32[] = x86_movslq_load([], x86_rax(), x86_rax(), false, 0, 1, 0); // 48 63 00
    if (ti.len() != 3 || ti[0] != 72 || ti[1] != 99 || ti[2] != 0) { return 90; }
    return 0;
}

function x86enc_selftest_10(): i32 {
    var tj: i32[] = x86_movzwq_load([], x86_rax(), 13, true, 15, 1, 16); // movzwq 16(%r13,%r15,1),%rax -> 4B 0F B7 44 3D 10
    if (tj.len() != 6 || tj[0] != 75 || tj[1] != 15 || tj[2] != 183 || tj[3] != 68 || tj[4] != 61 || tj[5] != 16) { return 91; }
    var tk: i32[] = x86_xorpd([], 0, 1); // xorpd %xmm1,%xmm0 -> 66 0F 57 C1
    if (tk.len() != 4 || tk[0] != 102 || tk[1] != 15 || tk[2] != 87 || tk[3] != 193) { return 92; }
    var tl: i32[] = x86_rep_stosb([]); // F3 AA
    if (tl.len() != 2 || tl[0] != 243 || tl[1] != 170) { return 93; }
    var tm: i32[] = x86_cld([]); // FC
    if (tm.len() != 1 || tm[0] != 252) { return 94; }
    // movsd reg-reg: movsd %xmm0,%xmm3 -> F2 0F 10 D8
    var tn: i32[] = x86_movsd_rr([], 3, 0);
    if (tn.len() != 4 || tn[0] != 242 || tn[1] != 15 || tn[2] != 16 || tn[3] != 216) { return 95; }
    // movslq reg-reg: movslq %eax,%rax -> 48 63 C0; movslq %r9d,%r8 -> 4D 63 C1
    var to: i32[] = x86_movslq_rr([], x86_rax(), x86_rax());
    if (to.len() != 3 || to[0] != 72 || to[1] != 99 || to[2] != 192) { return 96; }
    var tp: i32[] = x86_movslq_rr([], 8, 9);
    if (tp[0] != 77 || tp[1] != 99 || tp[2] != 193) { return 97; }
    // sqrtsd %xmm0,%xmm0 -> F2 0F 51 C0; roundsd $1,%xmm0,%xmm0 -> 66 0F 3A 0B C0 01
    var tq: i32[] = x86_sqrtsd([], 0, 0);
    if (tq.len() != 4 || tq[0] != 242 || tq[1] != 15 || tq[2] != 81 || tq[3] != 192) { return 98; }
    var tr: i32[] = x86_roundsd([], 0, 0, 1);
    if (tr.len() != 6 || tr[0] != 102 || tr[1] != 15 || tr[2] != 58 || tr[3] != 11 || tr[4] != 192 || tr[5] != 1) { return 99; }
    return 0;
}

function main(): i32 {
    var r: i32 = 0;
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
    write(string_from_bytes_unchecked(bin));
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
    write(string_from_bytes_unchecked(bin));
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
    var code: i32[] = [];
    code = x86_mov_r32_imm32(code, x86_rax(), 42); // a
    code = x86_mov_r32_imm32(code, x86_rcx(), 17); // b
    code = x86_cmp_r64_r64(code, x86_rax(), x86_rcx());
    var jge_off: i32 = code.len();                 // branch opcode offset
    code = x86_jcc_rel32(code, x86_cc_ge(), 0);    // placeholder rel32
    var patch_off: i32 = jge_off + 2;              // rel32 field (after 0F 8D)
    code = x86_mov_r64_r64(code, x86_rax(), x86_rcx()); // else: a = b
    var done_off: i32 = code.len();                // forward-branch target
    code = x86_patch_rel32(code, patch_off, x86_rel_to(done_off, patch_off));
    code = x86_mov_r64_r64(code, x86_rdi(), x86_rax()); // exit code = max
    code = x86_mov_r32_imm32(code, x86_rax(), 60);
    code = x86_syscall(code);
    var bin: i32[] = elf_static_executable_x86(code);
    write(string_from_bytes_unchecked(bin));
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
    var code: i32[] = [];
    var call_off: i32 = code.len();
    code = x86_call_rel32(code, 0);               // placeholder -> setval
    var patch_off: i32 = call_off + 1;            // rel32 field (after E8)
    code = x86_mov_r64_r64(code, x86_rdi(), x86_rax()); // exit code = result
    code = x86_mov_r32_imm32(code, x86_rax(), 60);
    code = x86_syscall(code);
    var setval_off: i32 = code.len();             // subroutine entry
    code = x86_patch_rel32(code, patch_off, x86_rel_to(setval_off, patch_off));
    code = x86_mov_r32_imm32(code, x86_rax(), 42); // setval: result = 42
    code = x86_ret(code);
    var bin: i32[] = elf_static_executable_x86(code);
    write(string_from_bytes_unchecked(bin));
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
    var a: X86Asm = x86_asm_new();
    a.code = x86_mov_r32_imm32(a.code, x86_rax(), 42);
    a.code = x86_mov_r32_imm32(a.code, x86_rcx(), 17);
    a.code = x86_cmp_r64_r64(a.code, x86_rax(), x86_rcx());
    a = x86_jcc_label(a, x86_cc_ge(), "done");
    a.code = x86_mov_r64_r64(a.code, x86_rax(), x86_rcx());
    a = x86_label(a, "done");
    a = x86_resolve(a);
    if (a.code.len() != 22 || a.code[13] != 15 || a.code[14] != 141) { return 1; }
    if (a.code[15] != 3 || a.code[16] != 0 || a.code[17] != 0 || a.code[18] != 0) { return 2; }

    // backward conditional: label loop, body, jne loop (patched immediately).
    var b: X86Asm = x86_asm_new();
    b = x86_label(b, "loop");
    b.code = x86_add_r64_imm32(b.code, x86_rax(), 6);
    b = x86_jcc_label(b, x86_cc_ne(), "loop");
    if (b.code.len() != 13 || b.code[7] != 15 || b.code[8] != 133) { return 3; }
    if (b.code[9] != 243 || b.code[10] != 255 || b.code[11] != 255 || b.code[12] != 255) { return 4; }

    // forward call: call sub, ret, label sub, resolve.
    var c: X86Asm = x86_asm_new();
    c = x86_call_label(c, "sub");
    c.code = x86_ret(c.code);
    c = x86_label(c, "sub");
    c = x86_resolve(c);
    if (c.code.len() != 6 || c.code[0] != 232 || c.code[1] != 1 || c.code[2] != 0) { return 5; }

    // label lookup: defined vs missing.
    if (x86_label_off(c, "sub") != 6) { return 6; }
    if (x86_label_off(c, "nope") != (0 - 1)) { return 7; }

    // rip-relative lea placeholder: lea rax, [rip+0] -> 48 8D 05 00*4.
    var d: X86Asm = x86_asm_new();
    d = x86_lea_rip_label(d, x86_rax(), "S0");
    if (d.code.len() != 7 || d.code[0] != 72 || d.code[1] != 141 || d.code[2] != 5) { return 8; }
    if (d.code[3] != 0 || d.code[4] != 0 || d.code[5] != 0 || d.code[6] != 0) { return 9; }
    // resolve a rip ref to a .rodata quad: lea(7)+mov(3)=10 text, padded 16,
    // S0 at 16; disp32 = 16 - (3+4) = 9.
    d.code = x86_mov_load_r64(d.code, x86_rax(), x86_rax(), 0);
    d = x86_rodata_label(d, "S0");
    d = x86_rodata_quad(d, 42i64);
    d = x86_resolve(d);
    if (d.code.len() != 10 || d.code[3] != 9 || d.code[4] != 0 || d.code[5] != 0 || d.code[6] != 0) { return 10; }
    if (d.rodata.len() != 8 || d.rodata[0] != 42 || d.rodata[1] != 0 || d.rodata[7] != 0) { return 11; }
    // x86_align8 rounds up to the .text/.rodata boundary.
    if (x86_align8(10) != 16 || x86_align8(16) != 16 || x86_align8(0) != 0) { return 12; }
    // rip-relative movq load/store: movq G(%rip),%rax -> 48 8B 05 <d>;
    // movq %rcx,G(%rip) -> 48 89 0D <d>.
    var e2: X86Asm = x86_asm_new();
    e2 = x86_mov_load_rip_label(e2, x86_rax(), "G");
    if (e2.code.len() != 7 || e2.code[0] != 72 || e2.code[1] != 139 || e2.code[2] != 5) { return 13; }
    e2 = x86_mov_store_rip_label(e2, x86_rcx(), "G");
    if (e2.code[7] != 72 || e2.code[8] != 137 || e2.code[9] != 13) { return 14; }
    return 0;
}
`

// x86ElfLabelDriverMain assembles, via the named-label API, a two-routine
// program: main calls compute (forward call), which loops acc += 6 seven
// times (backward jne to a label) and returns; main exits with the result
// (42). Both references resolve by name through x86_resolve.
const x86ElfLabelDriverMain = `
function main(): i32 {
    var a: X86Asm = x86_asm_new();
    a = x86_call_label(a, "compute");              // forward call
    a.code = x86_mov_r64_r64(a.code, x86_rdi(), x86_rax()); // exit code = result
    a.code = x86_mov_r32_imm32(a.code, x86_rax(), 60);
    a.code = x86_syscall(a.code);
    a = x86_label(a, "compute");
    a.code = x86_mov_r32_imm32(a.code, x86_rax(), 0); // acc = 0
    a.code = x86_mov_r32_imm32(a.code, x86_rcx(), 7); // counter = 7
    a = x86_label(a, "loop");
    a.code = x86_add_r64_imm32(a.code, x86_rax(), 6); // acc += 6
    a.code = x86_sub_r64_imm32(a.code, x86_rcx(), 1); // counter -= 1
    a.code = x86_cmp_r64_imm32(a.code, x86_rcx(), 0);
    a = x86_jcc_label(a, x86_cc_ne(), "loop");     // backward branch
    a.code = x86_ret(a.code);
    a = x86_resolve(a);
    var bin: i32[] = elf_static_executable_x86(a.code);
    write(string_from_bytes_unchecked(bin));
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
    var code: i32[] = [];
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
    var bin: i32[] = elf_static_executable_x86(code);
    write(string_from_bytes_unchecked(bin));
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
    var a: X86Asm = x86_asm_new();
    a = x86_lea_rip_label(a, x86_rax(), "answer");          // rax = &answer
    a.code = x86_mov_load_r64(a.code, x86_rax(), x86_rax(), 0); // rax = *answer
    a.code = x86_mov_r64_r64(a.code, x86_rdi(), x86_rax());  // exit code = answer
    a.code = x86_mov_r32_imm32(a.code, x86_rax(), 60);
    a.code = x86_syscall(a.code);
    a = x86_rodata_label(a, "answer");
    a = x86_rodata_quad(a, 42i64);                          // .quad 42
    a = x86_resolve(a);
    var bin: i32[] = elf_static_executable_data_x86(a.code, a.rodata);
    write(string_from_bytes_unchecked(bin));
    return 0;
}
`
