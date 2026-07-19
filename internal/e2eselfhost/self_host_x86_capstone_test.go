package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestSelfHostX86Capstone is the milestone of the native-binary track: it
// takes the AT&T assembly the self-hosted compiler (asm.fern) emits for a
// real Fern program, feeds that text through the self-hosted GAS front-end
// (x86_gas.fern) + ELF writer (elf.fern), and runs the resulting binary
// NATIVELY on x86-64 — with no external `as` or `ld` anywhere.
//
//   - Stage A: build asm_run.fern (source -> AT&T asm) via the Go
//     toolchain; capture each program's asm.
//   - Stage B: a small constant driver (x86CapstoneDriver) reads the asm
//     from a fixed file "in.s" (so the driver compiles once and the
//     embedded-asm size never bloats it — letting heap programs, whose asm
//     is the whole alloc/memcpy runtime, assemble too), runs it through
//     x86_gas_assemble + elf, and writes the ELF to stdout. Compiled once
//     via the self-host wasm pipeline; run per-case under `wasmtime --dir`.
//   - Stage C: run that ELF natively; assert exit code (and stdout).
//
// The table spans arithmetic, loops, if/else, comparisons (setCC), calls,
// recursion, floats, strings, and heap types (struct / array) — the last
// exercising asm.fern's full alloc/memcpy runtime through the assembler.
func TestSelfHostX86Capstone(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("native x86-64 run requires an amd64 host")
	}
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host x86 capstone")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{"astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_run.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	asmRun := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "asm_run")
	wasmRun := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	enc := mustRead(t, "../../examples/self_host/x86_encode.fern")
	gas := mustRead(t, "../../examples/self_host/x86_gas.fern")
	elf := mustRead(t, "../../examples/self_host/elf.fern")
	prelude := string(enc) + "\n" + string(gas) + "\n" + string(elf) + "\n"

	// Build the (constant) driver once.
	driverWat := runCapture(t, gcc, runner, wasmRun, []byte(prelude+x86CapstoneDriver))
	if len(driverWat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the capstone driver")
	}
	driverPath := filepath.Join(dir, "capstone_driver.wat")
	if err := os.WriteFile(driverPath, driverWat, 0o644); err != nil {
		t.Fatalf("write driver wat: %v", err)
	}

	cases := []struct {
		name    string
		prog    string
		want    int
		wantOut string
	}{
		{"arith", "function main(): i32 { var x: i32 = 40; var y: i32 = 2; return x + y; }\n", 42, ""},
		{"while", "function main(): i32 { var s: i32 = 0; var i: i32 = 0; while (i < 7) { s = s + 6; i = i + 1; } return s; }\n", 42, ""},
		{"ifelse", "function main(): i32 { var x: i32 = 10; if (x > 5) { return 42; } return 0; }\n", 42, ""},
		{"call", "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(40, 2); }\n", 42, ""},
		{"recur", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); }\nfunction main(): i32 { return fib(9) + 8; }\n", 42, ""},
		{"float", "function main(): i32 { var x: f64 = 84.0; var y: f64 = 2.0; var z: f64 = x / y; return z as i32; }\n", 42, ""},
		{"string", "function main(): i32 { write(\"hi!\"); return 0; }\n", 0, "hi!"},
		// Heap programs: their asm is the whole alloc/RC/memcpy runtime — the
		// mmap'd bump heap whose pointers live in `.bss` `.quad`s accessed via
		// rip-relative movq, plus the `.skip` freelist — the full instruction
		// + data-section surface.
		{"struct", "struct P { x: i32, y: i32 }\nfunction main(): i32 { var p = P { x: 40, y: 2 }; return p.x + p.y; }\n", 42, ""},
		{"array", "function main(): i32 { var a = [10, 20, 12]; var s = 0; var i = 0; while (i < 3) { s = s + a[i]; i = i + 1; } return s; }\n", 42, ""},
		// String length + indexing exercise movslq reg-reg (the .len() widen)
		// and the byte-load char access.
		{"strlen", "function main(): i32 { var s = \"hello world\"; return s.len() as i32 + 31; }\n", 42, ""},
		{"strchar", "function main(): i32 { var s = \"*abc\"; return s[0] as i32; }\n", 42, ""},
		// Maps exercise the full FNV-hash / open-addressing runtime, both
		// i32-keyed and string-keyed.
		{"mapi32", "function main(): i32 { var m = Map { 1: 40, 2: 2 }; return m.get_or(1, 0) + m.get_or(2, 0); }\n", 42, ""},
		{"mapstr", "function main(): i32 { var m = Map { \"a\": 40, \"b\": 2 }; return m.get_or(\"a\", 0) + m.get_or(\"b\", 0); }\n", 42, ""},
		// NOTE: f64 `.sqrt()`/`.floor()`/`.ceil()`/`.trunc()` are an asm.fern
		// gap — it emits `call __fn_f64__sqrt` etc. without emitting those
		// method bodies (an undefined reference even for gcc), so they aren't
		// capstone cases. Their SSE encoders (sqrtsd / roundsd) are
		// byte-verified in TestSelfHostX86Encode for when asm.fern emits them.
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Stage A.
			asmText := runCapture(t, gcc, runner, asmRun, []byte(tc.prog))
			if len(asmText) == 0 {
				t.Fatal("asm.fern produced no assembly")
			}
			// The driver reads a fixed path "in.s" (avoiding args(), which
			// has a layout-dependent alignment bug in the self-host wasm
			// runtime); subtests run sequentially, so overwriting is safe.
			if err := os.WriteFile(filepath.Join(dir, "in.s"), asmText, 0o644); err != nil {
				t.Fatalf("write asm: %v", err)
			}
			// Stage B: the driver reads in.s, assembles it, writes the ELF.
			bin, err := exec.Command(wasmtime, "run", "--dir", dir+"::/", driverPath).Output()
			if err != nil {
				t.Fatalf("wasmtime run (driver): %v", err)
			}
			if len(bin) < 4 || bin[0] != 0x7f || bin[1] != 'E' || bin[2] != 'L' || bin[3] != 'F' {
				t.Fatalf("output is not an ELF (bad magic): % x\n--- asm ---\n%s", bin[:min(4, len(bin))], asmText)
			}
			// Stage C: run the self-assembled binary natively.
			binPath := filepath.Join(dir, tc.name+".bin")
			if err := os.WriteFile(binPath, bin, 0o755); err != nil {
				t.Fatalf("write binary: %v", err)
			}
			stdout, runErr := exec.Command(binPath).Output()
			got := 0
			if runErr != nil {
				ee, ok := runErr.(*exec.ExitError)
				if !ok {
					t.Fatalf("run failed (not an exit code): %v\n--- asm ---\n%s", runErr, asmText)
				}
				got = ee.ExitCode()
			}
			if got != tc.want {
				t.Fatalf("exit code = %d, want %d\n--- asm ---\n%s", got, tc.want, asmText)
			}
			if tc.wantOut != "" && string(stdout) != tc.wantOut {
				t.Fatalf("stdout = %q, want %q\n--- asm ---\n%s", string(stdout), tc.wantOut, asmText)
			}
		})
	}
}

// x86CapstoneDriver reads the AT&T asm from the fixed path "in.s",
// assembles it with the self-hosted GAS front-end + ELF writer, and writes
// the runnable ELF to stdout. Reading the asm at runtime (rather than
// embedding it in the source) keeps the driver small + constant, so it
// compiles once and scales to large programs. A single-arm `Ok` match is
// enough since the file always exists.
const x86CapstoneDriver = `
function main(): i32 {
    match (read_file("in.s")) {
        Ok(asmtext) => {
            var a: X86Asm = x86_gas_assemble(asmtext);
            var entry: i32 = x86_label_off(a, "_start");
            write(string_from_bytes(elf_static_executable_bss_x86_at(a.code, a.rodata, a.bss_size, entry)));
            return 0;
        }
    }
    return 1;
}
`

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}
