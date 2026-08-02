package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// largeIntLiteralIRCases exercise i64 / u64 LITERALS above the i32 range through
// the self-host IR path. `n as i64` / `n as u64` widens its operand; a numeric
// literal operand is already a 64-bit value, so it must lower to an `i64.const`,
// not an `i32.const` (the latter truncates and, for a value above the i32 range,
// is invalid WAT — `i32.const 9000000000000000000` is rejected by wasm). #2928.
//
// The literals have nonzero low bits so a 32-bit truncation would give a
// different (wrong) answer rather than coincidentally matching. Each exit code
// is oracle-checked against the reference interpreter. (Return values are kept
// <= 126 so the comparison is clean: wasmtime maps a larger WASI exit value to
// 1, whereas the native exit path truncates mod 256 — a difference unrelated to
// the lowering under test.)
var largeIntLiteralIRCases = []struct {
	name string
	src  string
}{
	// u64 literal ~9e18 (> i32 and > 2^31, < 2^63): big % 1000 = 123.
	{"u64-literal-mod", `function main(): i32 {
    var big: u64 = 9000000000000000123 as u64;
    return (big % 1000 as u64) as i32;
}`},
	// i64 literal ~5e18 round-trips through an i64.const: a 32-bit truncation
	// would not compare equal. (`% 1000` would be 457 > 126, so a round-trip is
	// used instead to keep the exit code in range.)
	{"i64-literal-roundtrip", `function main(): i32 {
    var n: i64 = 5000000000000000457 as i64;
    if (n == 5000000000000000457 as i64) { return 77; }
    return 0;
}`},
	// Large u64 literal feeding an unsigned compare (literal + #2917 path):
	// 9e18 > 1 is true → 7.
	{"u64-literal-compare", `function main(): i32 {
    var big: u64 = 9000000000000000000 as u64;
    if (big > 1 as u64) { return 7; }
    return 0;
}`},
}

// TestSelfHostLargeIntLiteralIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with the routing pinned to the "ir" path.
func TestSelfHostLargeIntLiteralIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range largeIntLiteralIRCases {
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

// TestSelfHostLargeIntLiteralIRWasm runs the same cases through the wasm IR
// backend — the leg where the i32.const truncation bug (#2928) reproduces.
func TestSelfHostLargeIntLiteralIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host large-int-literal wasm IR e2e")
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

	for _, tc := range largeIntLiteralIRCases {
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
			watFile := filepath.Join(dir, "largelit_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("large-int-literal wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
