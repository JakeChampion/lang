package e2ecompiler

import (
	"bytes"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostArm64NativeLinuxElfRuns(t *testing.T) {
	if _, err := exec.LookPath("qemu-aarch64"); err != nil {
		t.Skip("qemu-aarch64 not on PATH; skipping arm64-linux ELF run e2e")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("native x86-64 run required to build the self-host emitter + driver")
	}

	dir := writeSelfHostAsmProject(t)
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "flatten.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	emitBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "lxemit")

	native := string(mustRead(t, "../../../compiler/arm64_native.fern"))
	elfsrc := string(mustRead(t, "../../../compiler/elf.fern"))
	const driverMain = `
function main(): i32 {
    let asm: string = ""; let ok: boolean = false;
    match (read_file("in.s")) { Ok(s) => { asm = s; ok = true; }, Err(e) => { ok = false; } }
    if (!ok) { write("ERR"); return 1; }
    let p: Arm64GasProg = arm64_gas_program(asm);
    if (p.unknown.len() > 0) {
        let msg: string = "UNKNOWN:"; let i: i32 = 0;
        while (i < p.unknown.len()) { msg = msg + p.unknown[i] + ","; i = i + 1; }
        write(msg); return 0;
    }
    let pa: Arm64Asm = p.asm;
    // W^X two-segment layout (matches fern.fern's arm64_elf_binary): .text
    // R+X, data R+W on the next page boundary.
    let tv: i64 = (elf_text_vaddr_wx(false)) as i64;
    let dv: i64 = (elf_data_vaddr_wx(pa.text.len())) as i64;
    p = arm64_gas_link(p, tv, dv);
    let pa2: Arm64Asm = p.asm;
    let entry_off: i32 = arm64_asm_label_off(pa2, "_start");
    if (entry_off < 0) { entry_off = 0; }
    write(string_from_bytes_unchecked(elf_image_wx(pa2.text, p.data, elf_em_aarch64(), entry_off, p.bss_size)));
    return 0;
}
`
	edir := t.TempDir()
	if err := os.WriteFile(filepath.Join(edir, "drv.fern"), []byte(native+"\n"+elfsrc+"\n"+driverMain), 0o644); err != nil {
		t.Fatalf("write driver: %v", err)
	}
	drvBin := buildSelfHostBin(t, gcc, edir, "drv.fern", "drv")

	const pieDriverMain = `
function main(): i32 {
    let asm: string = ""; let ok: boolean = false;
    match (read_file("in.s")) { Ok(s) => { asm = s; ok = true; }, Err(e) => { ok = false; } }
    if (!ok) { write("ERR"); return 1; }
    let p: Arm64GasProg = arm64_gas_program(asm);
    if (p.unknown.len() > 0) { write("UNKNOWN:"); return 0; }
    let pa: Arm64Asm = p.asm;
    let tv: i64 = (elf_text_vaddr_pie()) as i64;
    let dv: i64 = (elf_data_vaddr_pie(pa.text.len())) as i64;
    p = arm64_gas_link(p, tv, dv);
    let pa2: Arm64Asm = p.asm;
    let entry_off: i32 = arm64_asm_label_off(pa2, "_start");
    if (entry_off < 0) { entry_off = 0; }
    write(string_from_bytes_unchecked(elf_image_pie(pa2.text, p.data, elf_em_aarch64(), entry_off, p.bss_size)));
    return 0;
}
`
	pdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pdir, "drv.fern"), []byte(native+"\n"+elfsrc+"\n"+pieDriverMain), 0o644); err != nil {
		t.Fatalf("write pie driver: %v", err)
	}
	drvPieBin := buildSelfHostBin(t, gcc, pdir, "drv.fern", "drvpie")

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"exit42", `function main(): i32 { return 42; }`, 42},
		{"arith", `function main(): i32 { let x = 6; let y = 7; return x * y; }`, 42},
		{"fib", `function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n-1)+fib(n-2); } function main(): i32 { return fib(10); }`, 55},
		{"print", `function main(): i32 { print("hi"); return 0; }`, 0},
		{"concat", `function main(): i32 { let s: string = "hello, " + "world!"; return s.len(); }`, 13},
		{"array", `function main(): i32 { let a = [1,2,3,4,5]; let i = 0; let s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }`, 15},
		{"strbuild", `function main(): i32 { let s: string = ""; let i: i32 = 0; while (i < 3) { s = s + "ab"; i = i + 1; } return s.len(); }`, 6},
		{"floats", `function main(): i32 { let x: f64 = 3.5; let y: f64 = 2.0; let z: f64 = x*y + x/y - x; if (z > 5.0) { return 7; } return 1; }`, 7},
		{"bitcount", `function main(): i32 {
    if (__ctz32(0 as u32) != 32) { return 1; }
    if (__ctz64(0 as u64) != 64) { return 2; }
    if (__popcount32(0 as u32) != 0) { return 3; }
    if (__popcount64(0 as u64) != 0) { return 4; }
    if (__ctz32(16 as u32) != 4) { return 5; }
    if (__ctz64(1048576 as u64) != 20) { return 6; }
    if (__popcount32(4294967295 as u32) != 32) { return 7; }
    if (__popcount64(1023 as u64) != 10) { return 8; }
    if (__clz32(1 as u32) != 31) { return 9; }
    if (__clz64(1 as u64) != 63) { return 10; }
    return 42;
}`, 42},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rdir := t.TempDir()
			asm := runCapture(t, gcc, runner, emitBin, []byte(c.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("emitter produced 0 bytes")
			}
			if err := os.WriteFile(filepath.Join(rdir, "in.s"), asm, 0o644); err != nil {
				t.Fatalf("write in.s: %v", err)
			}
			cmd := exec.Command(drvBin)
			cmd.Dir = rdir
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("assemble driver failed: %v", err)
			}
			if bytes.HasPrefix(out, []byte("UNKNOWN:")) {
				t.Fatalf("arm64_native reported unknown instructions: %s", out)
			}
			f, err := elf.NewFile(bytes.NewReader(out))
			if err != nil {
				t.Fatalf("output is not a parseable ELF: %v (len=%d)", err, len(out))
			}
			if f.Machine != elf.EM_AARCH64 || f.Type != elf.ET_EXEC {
				t.Fatalf("got machine=%v type=%v, want AARCH64/EXEC", f.Machine, f.Type)
			}
			loads := 0
			for _, prog := range f.Progs {
				if prog.Type != elf.PT_LOAD {
					continue
				}
				loads++
				if prog.Flags&elf.PF_W != 0 && prog.Flags&elf.PF_X != 0 {
					t.Errorf("%s: PT_LOAD is W+X (%v) — not W^X", c.name, prog.Flags)
				}
			}
			if loads != 2 {
				t.Errorf("%s: got %d PT_LOAD segments, want 2 (W^X code + data)", c.name, loads)
			}
			binPath := filepath.Join(rdir, c.name)
			if err := os.WriteFile(binPath, out, 0o755); err != nil {
				t.Fatalf("write bin: %v", err)
			}
			run := exec.Command("qemu-aarch64", binPath)
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != c.want {
				t.Errorf("%s: qemu exit = %d, want %d", c.name, code, c.want)
			}

			pcmd := exec.Command(drvPieBin)
			pcmd.Dir = rdir
			pout, perr := pcmd.Output()
			if perr != nil {
				t.Fatalf("pie assemble driver failed: %v", perr)
			}
			if bytes.HasPrefix(pout, []byte("UNKNOWN:")) {
				t.Fatalf("pie: arm64_native reported unknown instructions")
			}
			pf, err := elf.NewFile(bytes.NewReader(pout))
			if err != nil {
				t.Fatalf("pie output is not a parseable ELF: %v (len=%d)", err, len(pout))
			}
			if pf.Machine != elf.EM_AARCH64 || pf.Type != elf.ET_DYN {
				t.Fatalf("pie: got machine=%v type=%v, want AARCH64/DYN", pf.Machine, pf.Type)
			}
			ploads := 0
			for _, prog := range pf.Progs {
				if prog.Type != elf.PT_LOAD {
					continue
				}
				ploads++
				if prog.Flags&elf.PF_W != 0 && prog.Flags&elf.PF_X != 0 {
					t.Errorf("%s pie: PT_LOAD is W+X (%v)", c.name, prog.Flags)
				}
			}
			if ploads != 2 {
				t.Errorf("%s pie: got %d PT_LOAD segments, want 2", c.name, ploads)
			}
			pbinPath := filepath.Join(rdir, c.name+"-pie")
			if err := os.WriteFile(pbinPath, pout, 0o755); err != nil {
				t.Fatalf("write pie bin: %v", err)
			}
			prun := exec.Command("qemu-aarch64", pbinPath)
			_ = prun.Run()
			if code := prun.ProcessState.ExitCode(); code != c.want {
				t.Errorf("%s pie: qemu exit = %d, want %d", c.name, code, c.want)
			}
		})
	}
}
