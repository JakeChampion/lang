package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// f64BitsIRPrelude provides a tiny f64->i64 helper so the "via-param" case can
// exercise f64_bits across a call boundary (an f64 parameter reinterpreted to
// i64 in the callee's return position).
const f64BitsIRPrelude = `function g(v: f64): i64 { return f64_bits(v); }
`

// f64BitsIRCases pin the `f64_bits` builtin — reinterpret an f64's 8 bytes as an
// i64 (the raw IEEE-754 bit pattern, no numeric conversion) — on the self-host
// IR path (x86-64 + wasm). Previously the whole float bit-reinterpret family
// bailed the module to the legacy AST emitter (#3513). f64_bits now lowers:
// op_f64_bits is a no-op on the register backends (the value already rides the
// runtime stack as its bits) and an `i64.reinterpret_f64` on wasm's typed stack.
// irlower recognises it as a 64-bit value (infer_expr_width / lower_i64) and
// emits the reinterpret in lower_expr.
//
// (The inverse f64_from_bits deliberately stays on the AST path for now — it
// surfaced a wasm self-host-driver crash on one input shape; tracked on #3513.)
//
// Each case is routing-pinned to "ir" and oracle-checked against the
// interpreter; every result stays <= 120 (the wasm exit-code clamp, #2908).
var f64BitsIRCases = []struct {
	name string
	main string
	want int
}{
	// 2.0 = 0x4000000000000000; high byte 0x40 = 64.
	{"high-byte-2", `var x = 2.0; var b: i64 = f64_bits(x); return (b >> 56i64) as i32;`, 64},
	// 1.5 = 0x3FF8000000000000; high byte 0x3F = 63.
	{"high-byte-1p5", `var x = 1.5; var b: i64 = f64_bits(x); return (b >> 56i64) as i32;`, 63},
	// +0.0 has an all-zero bit pattern.
	{"zero-bits", `var x = 0.0; var b: i64 = f64_bits(x); return b as i32;`, 0},
	// distinct floats produce distinct bit patterns (the reinterpret is faithful).
	{"distinct-patterns", `var a: i64 = f64_bits(1.0); var b: i64 = f64_bits(2.0); if (a != b) { return 5; } return 1;`, 5},
	// the operand can be a computed f64 expression (4.0 -> high byte 0x40).
	{"from-expr", `var b: i64 = f64_bits(3.0 + 1.0); return (b >> 56i64) as i32;`, 64},
	// reinterpret across a call boundary (uses the prelude's g).
	{"via-param", `var b: i64 = g(2.0); return (b >> 56i64) as i32;`, 64},
	// the full 11-bit exponent field of 2.0 is 1024 (0x400); biased subtract -> 0.
	{"exp-field-2", `var x = 2.0; var b: i64 = f64_bits(x); return ((b >> 52i64) as i32) - 1024;`, 0},
}

func f64BitsIRSrc(mainBody string) string {
	return f64BitsIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostF64BitsIRX86_64 routes each case through the self-hosted x86-64 IR
// driver, with the routing pinned to the "ir" path (the register backend lowers
// op_f64_bits to a no-op — the bits already ride the rt-stack slot).
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

// TestSelfHostF64BitsIRWasm runs the same cases through the wasm IR backend,
// where op_f64_bits lowers to a real `i64.reinterpret_f64` (wasm's typed stack
// distinguishes f64 from i64, unlike the bit-slot register backends).
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

// TestNativeF64BitsX86_64 is the native-backend cross-check: the same programs
// compiled through the Go x86-64 emitter must produce the same exit codes (the
// native compiler lowers f64_bits via OpReinterpretI64F64).
func TestNativeF64BitsX86_64(t *testing.T) {
	for _, tc := range f64BitsIRCases {
		t.Run(tc.name, func(t *testing.T) {
			_, code := compileAndRunX86_64(t, f64BitsIRSrc(tc.main))
			if code != tc.want {
				t.Errorf("%s native exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
