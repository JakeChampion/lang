package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestSelfHostX86Capstone is the milestone of the native-binary track: it
// takes the AT&T assembly that the self-hosted compiler (asm.fern) emits
// for a real Fern program, feeds that text through the self-hosted GAS
// front-end (x86_gas.fern) + ELF writer (elf.fern), and runs the resulting
// binary NATIVELY on x86-64 — with no external `as` or `ld` anywhere.
//
// Stage A: build asm_run.fern (source -> AT&T asm) via the Go toolchain
// and capture the asm for each program.
// Stage B: build a Fern driver that embeds that asm, calls
// x86_gas_assemble, sets the ELF entry to the `_start` label's offset
// (asm.fern emits `__fn_main` before `_start`), and writes the ELF to
// stdout. Compile it through the self-host wasm pipeline and run under
// wasmtime to obtain the ELF bytes.
// Stage C: execute that ELF natively and assert the exit code.
//
// The table covers arithmetic, while loops, if/else, comparisons (setCC),
// function calls, and recursion — all returning 42.
func TestSelfHostX86Capstone(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("native x86-64 run requires an amd64 host")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host x86 capstone")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "asm.fern", "asm_run.fern", "wasm.fern", "wasm_run.fern"} {
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

	cases := []struct {
		name string
		prog string
		want int
	}{
		{"arith", "function main(): i32 { var x: i32 = 40; var y: i32 = 2; return x + y; }\n", 42},
		{"while", "function main(): i32 { var s: i32 = 0; var i: i32 = 0; while (i < 7) { s = s + 6; i = i + 1; } return s; }\n", 42},
		{"ifelse", "function main(): i32 { var x: i32 = 10; if (x > 5) { return 42; } return 0; }\n", 42},
		{"call", "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(40, 2); }\n", 42},
		{"recur", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); }\nfunction main(): i32 { return fib(9) + 8; }\n", 42},
		{"float", "function main(): i32 { var x: f64 = 84.0; var y: f64 = 2.0; var z: f64 = x / y; return z as i32; }\n", 42},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Stage A.
			asmText := runCapture(t, gcc, runner, asmRun, []byte(tc.prog))
			if len(asmText) == 0 {
				t.Fatal("asm.fern produced no assembly")
			}
			// Stage B: embed the asm as a Fern string literal (escape \ and "
			// then turn newlines into \n).
			lit := strings.ReplaceAll(string(asmText), "\\", "\\\\")
			lit = strings.ReplaceAll(lit, "\"", "\\\"")
			lit = strings.ReplaceAll(lit, "\n", "\\n")
			driver := "\nfunction main(): i32 {\n" +
				"    var src: string = \"" + lit + "\";\n" +
				"    var a: X86Asm = x86_gas_assemble(src);\n" +
				"    var entry: i32 = x86_label_off(a, \"_start\");\n" +
				"    write(string_from_bytes(elf_static_executable_data_x86_at(a.code, a.rodata, entry)));\n" +
				"    return 0;\n}\n"

			wat := runCapture(t, gcc, runner, wasmRun, []byte(prelude+driver))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes for the capstone driver")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
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
			// Stage C: run the self-assembled binary natively.
			binPath := filepath.Join(dir, tc.name)
			if err := os.WriteFile(binPath, bin, 0o755); err != nil {
				t.Fatalf("write binary: %v", err)
			}
			got := 0
			if err := exec.Command(binPath).Run(); err != nil {
				ee, ok := err.(*exec.ExitError)
				if !ok {
					t.Fatalf("run failed (not an exit code): %v\n--- asm ---\n%s", err, asmText)
				}
				got = ee.ExitCode()
			}
			if got != tc.want {
				t.Fatalf("exit code = %d, want %d\n--- asm ---\n%s", got, tc.want, asmText)
			}
		})
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}
