package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// floatBitsIRCases pin the float<->int bit-reinterpret builtins — `f64_bits`,
// `f64_from_bits`, `f32_bits`, `f32_from_bits` — on the IR path. Before #3513
// these bailed the whole module to the legacy AST emitter (asm_pathprobe_run
// reported "ast"); they now lower through irlower's lower_expr / lower_i64.
// On the register backends f64_bits/f64_from_bits are no-op reinterprets (the
// 8-byte slot already holds the IEEE-754 bits) while the f32 pair narrows /
// widens between f64 and f32; wasm emits the typed reinterpret instructions
// (i64.reinterpret_f64 / f64.reinterpret_i64 / f32.demote_f64+i32.reinterpret_f32
// / f32.reinterpret_i32+f64.promote_f32). Each case is routing-pinned to "ir"
// (asm_pathprobe_run) and oracle-checked against the interpreter; every result
// stays <= 120 (cf. the wasmtime exit-code gap #2908).
//
// The f32_from_bits cases write `(f32_from_bits(x) as f64)` so the program
// type-checks under BOTH compilers despite the known result-type split (#3513's
// checker observation): the native checker types f32_from_bits as f32, the
// self-host as f64 — the explicit `as f64` is a promote on one and a no-op on
// the other, valid either way.
var floatBitsIRCases = []struct {
	name string
	main string
}{
	// f64_bits: extract the biased exponent of 2.0 (1024) and subtract 1000.
	{"f64-bits-exp", `function main(): i32 { var b: i64 = f64_bits(2.0); return ((b >> 52i64) as i32) - 1000; }`},
	// f64_bits then f64_from_bits round-trips 3.5 back to 3.5.
	{"f64-roundtrip", `function main(): i32 { var b: i64 = f64_bits(3.5); var y: f64 = f64_from_bits(b); return (y * 10.0) as i32; }`},
	// f64_from_bits over a literal i64 bit pattern (0x4045000000000000 == 42.0).
	{"f64-from-bits-lit", `function main(): i32 { var n: i64 = 4631107791820423168i64; var y: f64 = f64_from_bits(n); return y as i32; }`},
	// f32_bits: the i32 bit pattern of 1.5 is 0x3FC00000; >>23 == 0x7F (127).
	{"f32-bits", `function main(): i32 { var fb: i32 = f32_bits(1.5); return (fb >> 23) - 100; }`},
	// f32_bits then f32_from_bits round-trips 2.5 (×4 == 10).
	{"f32-roundtrip", `function main(): i32 { var fb: i32 = f32_bits(2.5); return ((f32_from_bits(fb) as f64) * 4.0) as i32; }`},
}

// TestSelfHostFloatBitsIRX86_64 routes each case through the self-hosted x86-64
// IR driver (asm_run), pins the routing to "ir" (asm_pathprobe_run), and
// oracle-checks the exit code against the interpreter.
func TestSelfHostFloatBitsIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
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

	for _, tc := range floatBitsIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
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
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestNativeFloatBitsX86_64 is the native-backend half: the same programs
// compiled through the Go compiler's x86-64 emitter must produce the same exit
// codes (the OpReinterpret* path the self-host lowering mirrors).
func TestNativeFloatBitsX86_64(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	for _, tc := range floatBitsIRCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.main+"\n")
			_, code := compileAndRunX86_64(t, tc.main+"\n")
			if code != want {
				t.Errorf("%s native exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostFloatBitsIRWasm runs the same cases through the wasm IR backend.
// wasm is the only backend where these are NOT no-ops: its typed stack needs
// the real reinterpret / demote / promote instructions, so a per-backend
// regression in wasm_ir's lowering would surface here even if the register
// backends stayed correct. Mirrors the dual-backend shape of
// self_host_binary_bitwise_ir_test.go.
func TestSelfHostFloatBitsIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host float-bits wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range floatBitsIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
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
			watFile := filepath.Join(dir, "float_bits_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("float-bits wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
