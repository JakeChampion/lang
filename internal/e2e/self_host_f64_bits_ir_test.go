package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// f64BitsIRCases exercise the `f64_bits` builtin — reinterpreting an f64's raw
// bits as an i64 (a value-preserving bit copy, IEEE-754 layout) — through the
// self-host IR path on x86-64 + wasm. Previously `f64_bits` had no self-host IR
// lowering and bailed to the AST path; this adds the `reinterpret_f64_i64` IR op
// (a no-op on the register backends, where the value already rides the operand
// stack as raw bits; `i64.reinterpret_f64` on wasm's typed stack) plus the
// irlower recognition + i64 width tracking. The cases extract bit-fields of the
// IEEE-754 double (top nibble / biased exponent) and narrow back to i32 so the
// exit code is observable; each is pinned to the `"ir"` path and oracle-checked
// against the native interpreter. (The reverse `f64_from_bits` and the f32 pair
// are a later increment — they need f64-result width tracking.) These are pure
// language builtins, so each case is an import-free bare `main`. FEATURE-AUDIT
// `f32_bits/f32_from_bits/f64_bits/f64_from_bits` row (f64_bits direction).
var f64BitsIRCases = []struct {
	name string
	main string
	want int
}{
	// 2.0 = 0x4000_0000_0000_0000; top nibble (>>60) = 0x4 = 4.
	{"two-hi", `return (f64_bits(2.0) >> 60) as i32;`, 4},
	// 1.0 = 0x3FF0_0000_0000_0000; top nibble = 0x3 = 3.
	{"one-hi", `return (f64_bits(1.0) >> 60) as i32;`, 3},
	// 0.5 = 0x3FE0_0000_0000_0000; top nibble = 0x3 = 3.
	{"half-hi", `return (f64_bits(0.5) >> 60) as i32;`, 3},
	// +0.0 has an all-zero bit pattern.
	{"zero", `return f64_bits(0.0) as i32;`, 0},
	// biased exponent of 8.0 is 1026; 1026 - 1024 = 2.
	{"exp8", `return ((f64_bits(8.0) >> 52) as i32) - 1024;`, 2},
	// via an explicit i64 local: 3.0 = 0x4008...; top nibble = 0x4 = 4.
	{"local", `var b: i64 = f64_bits(3.0); return (b >> 60) as i32;`, 4},
}

func f64BitsIRSrc(mainBody string) string {
	return "function main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostF64BitsIRX86_64 routes each case through the self-hosted x86-64 IR
// driver, with the routing pinned to the "ir" path.
func TestSelfHostF64BitsIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range f64BitsIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(f64BitsIRSrc(tc.main))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostF64BitsIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostF64BitsIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host f64_bits wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range f64BitsIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(f64BitsIRSrc(tc.main))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "f64_bits_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("f64_bits wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
