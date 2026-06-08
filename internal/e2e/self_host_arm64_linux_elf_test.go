package e2e

import (
	"bytes"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostArm64NativeLinuxElfRuns is the arm64-LINUX counterpart of the
// arm64-darwin Mach-O path: it assembles the self-host asm_arm64 emitter's
// *actual* Linux output (emit_module(false) — no darwinize) through
// arm64_native into a static ELF via elf.fern, then RUNS the binary under
// qemu-aarch64 and checks the exit code. Unlike the darwin path (whose
// execution can only be checked on a macOS runner), the Linux path runs on
// the Linux CI box directly, so this is a true end-to-end proof that
// arm64_native produces *executable* machine code — it caught the
// literal-pool (`ldr =N` / `.ltorg`) and negative-offset (stur/ldur) gaps
// the darwin tests silently skipped past.
//
// Both arm64_native and elf.fern are import-free, so the driver concatenates
// them with a small main() (no `pub` needed) and is compiled by the Go x86
// backend (the CLI's backend). Covers the common surface: literal loads,
// negative-offset frame stores, branches/calls + ELF entry wiring (exit42 /
// arith / fib); rodata strings + `:lo12:` addressing (print); the heap
// (`.bss`/`.skip`, register-offset array indexing, post-index byte copy for
// string concat) via array / concat / strout.
func TestSelfHostArm64NativeLinuxElfRuns(t *testing.T) {
	if _, err := exec.LookPath("qemu-aarch64"); err != nil {
		t.Skip("qemu-aarch64 not on PATH; skipping arm64-linux ELF run e2e")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("native x86-64 run required to build the self-host emitter + driver")
	}

	// Build the Linux arm64 asm emitter (asm_arm64_run.fern, emit_module(false)).
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asmcore.fern", "flatten.fern", "asm_arm64.fern", "asm_arm64_run.fern"} {
		b, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	emitBin := buildSelfHostBin(t, gcc, dir, "asm_arm64_run.fern", "lxemit")

	// Driver: arm64_native + elf.fern + main(), concatenated (no imports).
	// Reads the asm from "in.s" in its CWD, assembles + ELF-wraps it, writes
	// the binary to stdout. Compiled by the Go x86 backend.
	native := string(mustRead(t, "../../examples/self_host/arm64_native.fern"))
	elfsrc := string(mustRead(t, "../../examples/self_host/elf.fern"))
	const driverMain = `
function to_u8(b: i32[]): u8[] { var o: u8[] = []; var i: i32 = 0; while (i < b.len()) { o = o.append(b[i] as u8); i = i + 1; } return o; }
function roundup8x(n: i32): i32 { return (n + 7) & (0 - 8); }
function main(): i32 {
    var asm: string = ""; var ok: boolean = false;
    match (read_file("in.s")) { Ok(s) => { asm = s; ok = true; }, Err(e) => { ok = false; } }
    if (!ok) { write("ERR"); return 1; }
    var p: Arm64GasProg = arm64_gas_program(asm);
    if (p.unknown.len() > 0) {
        var msg: string = "UNKNOWN:"; var i: i32 = 0;
        while (i < p.unknown.len()) { msg = msg + p.unknown[i] + ","; i = i + 1; }
        write(msg); return 0;
    }
    var pa: Arm64Asm = p.asm;
    var tv: i64 = (elf_text_vaddr()) as i64;
    var dv: i64 = tv + (roundup8x(pa.code.len()) as i64);
    p = arm64_gas_link(p, tv, dv);
    var pa2: Arm64Asm = p.asm;
    var entry_off: i32 = arm64_asm_label_off(pa2, "_start");
    if (entry_off < 0) { entry_off = 0; }
    var body: i32[] = elf_pad_to_8(pa2.code);
    body = elf_cat(body, p.data);
    var bin: i32[] = elf_image_entry_bss(body, 7, elf_em_aarch64(), entry_off, p.bss_size);
    write(string_from_bytes(to_u8(bin)));
    return 0;
}
`
	edir := t.TempDir()
	if err := os.WriteFile(filepath.Join(edir, "drv.fern"), []byte(native+"\n"+elfsrc+"\n"+driverMain), 0o644); err != nil {
		t.Fatalf("write driver: %v", err)
	}
	drvBin := buildSelfHostBin(t, gcc, edir, "drv.fern", "drv")

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"exit42", `function main(): i32 { return 42; }`, 42},
		{"arith", `function main(): i32 { var x = 6; var y = 7; return x * y; }`, 42},
		{"fib", `function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n-1)+fib(n-2); } function main(): i32 { return fib(10); }`, 55},
		{"print", `function main(): i32 { print("hi"); return 0; }`, 0},
		{"concat", `function main(): i32 { var s: string = "hello, " + "world!"; return s.len(); }`, 13},
		{"array", `function main(): i32 { var a = [1,2,3,4,5]; var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }`, 15},
		{"strbuild", `function main(): i32 { var s: string = ""; var i: i32 = 0; while (i < 3) { s = s + "ab"; i = i + 1; } return s.len(); }`, 6},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rdir := t.TempDir()
			asm := runCapture(t, gcc, runner, emitBin, []byte(c.src+"\n"))
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
			binPath := filepath.Join(rdir, c.name)
			if err := os.WriteFile(binPath, out, 0o755); err != nil {
				t.Fatalf("write bin: %v", err)
			}
			run := exec.Command("qemu-aarch64", binPath)
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != c.want {
				t.Errorf("%s: qemu exit = %d, want %d", c.name, code, c.want)
			}
		})
	}
}
