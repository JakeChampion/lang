package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// numericMethodsIRCases are self-contained programs exercising the i64 / u32 /
// u64 numeric-method LOGIC (abs / min / max / clamp + unsigned compare) that
// std/i64, std/u32, std/u64 wrap, verified through the self-hosted compiler's
// IR path on BOTH the x86-64 (register) and wasm (stack) backends. The
// single-program self-host driver resolves no imports, so the method bodies are
// inlined — this verifies the language constructs the stdlib methods compile to
// (64-bit and unsigned arithmetic / compare / branch across function calls)
// lower correctly on the IR path.
//
// The u32 / u64 cases deliberately use values above 2^31 / 2^63 so a signed
// comparison would give the wrong answer — confirming the IR path selects the
// unsigned compare. (This is what surfaced #2917: the register backends compare
// a zero-extended u32 in a 64-bit slot, where signed happens to agree, but wasm
// compares natively at 32 bits and needs the unsigned op.) The u64 big value is
// built with shifts rather than a >2^63 literal to avoid an unrelated wasm
// large-i64-literal gap (#2928).
//
// Each program's exit code is oracle-checked against the reference interpreter
// rather than hardcoded, so a wrong-but-stable result can't slip through (cf.
// the hardcoded-expectation gap in #2908). FEATURE-AUDIT std/i64 · u32 · u64.
var numericMethodsIRCases = []struct {
	name string
	src  string
}{
	// i64 abs / min / max / clamp, composed across helper calls.
	// 7 + 5 + 9 = 21, then clamp(12,3,9)==9 adds 100 → 121.
	{"i64-abs-min-max-clamp", `function i64_abs(n: i64): i64 { if (n < (0 as i64)) { return (0 as i64) - n; } return n; }
function i64_min(a: i64, b: i64): i64 { if (a < b) { return a; } return b; }
function i64_max(a: i64, b: i64): i64 { if (a > b) { return a; } return b; }
function main(): i32 {
    var r: i64 = i64_abs(0 as i64 - 7 as i64) + i64_min(5 as i64, 9 as i64) + i64_max(5 as i64, 9 as i64);
    if (i64_max(i64_min(12 as i64, 9 as i64), 3 as i64) == 9 as i64) { r = r + 100 as i64; }
    return r as i32;
}`},
	// u32 min / max with a value above 2^31 (signed-negative as i32): unsigned
	// max(4e9, 1) == 4e9 and min == 1. A signed 32-bit compare would invert both.
	{"u32-unsigned-min-max", `function u32_min(a: u32, b: u32): u32 { if (a < b) { return a; } return b; }
function u32_max(a: u32, b: u32): u32 { if (a > b) { return a; } return b; }
function main(): i32 {
    var big: u32 = 4000000000 as u32; var one: u32 = 1 as u32;
    if (u32_max(big, one) == big && u32_min(big, one) == one) { return 42; }
    return 0;
}`},
	// u64 min with a value above 2^63 (2^63 + 2^62, built by shifts): unsigned
	// min(big, 1) == 1. A signed 64-bit compare would pick big (negative as i64).
	{"u64-unsigned-min", `function u64_min(a: u64, b: u64): u64 { if (a < b) { return a; } return b; }
function main(): i32 {
    var big: u64 = (1 as u64 << 63 as u64) + (1 as u64 << 62 as u64);
    var one: u64 = 1 as u64;
    if (u64_min(big, one) == one) { return 42; }
    return 0;
}`},
}

// interpExit runs `fern -interp` on src (written to a temp file) and returns the
// reference exit code — the oracle for the self-host comparisons below.
func interpExit(t *testing.T, interpBin, src string) int {
	t.Helper()
	f := filepath.Join(t.TempDir(), "oracle.fern")
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatalf("write oracle src: %v", err)
	}
	cmd := exec.Command(interpBin, "-interp", f)
	_ = cmd.Run()
	return cmd.ProcessState.ExitCode()
}

// TestSelfHostNumericMethodsIRX86_64 routes each case through the self-hosted
// x86-64 driver (IR on), asserts the exit code matches the interpreter oracle,
// AND probes the routing (asm_pathprobe_run) to pin each case to the "ir" path.
func TestSelfHostNumericMethodsIRX86_64(t *testing.T) {
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

	for _, tc := range numericMethodsIRCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, []byte(tc.src))))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
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

// TestSelfHostNumericMethodsIRWasm runs the same cases through the wasm IR
// backend (wasm_ir_run -ir), oracle-checked against the interpreter. This is the
// leg that pins the unsigned-compare fix (#2917): the u32 / u64 cases fail here
// with a signed compare.
func TestSelfHostNumericMethodsIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host numeric-methods wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
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

	for _, tc := range numericMethodsIRCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "numeric_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("numeric-methods wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
