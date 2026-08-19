package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// whileIRCases pin the standalone `while`-loop construct to the self-host IR path
// on x86-64 + wasm. The while lowering (irlower.fern's StmtWhile arm) emits a
// wasm-style block/loop/br_if and is IR-eligible for any i32-condition loop — it
// bails only on a 64-bit-width condition. while loops are exercised for exit
// codes throughout self_host_asm_run_test.go (while-sum, while-early-return, the
// print loops, …), but NONE of those assert the routing, so a regression that
// kicked while off the IR path would pass silently. These cases close that
// gap with the path-probe pin (assert path == "ir") + interp oracle, mirroring
// self_host_block_expr_ir_test.go.
//
// Every condition is i32 and every result is small (<= 126, wasmtime exit-code
// truncation, cf. #2908).
var whileIRCases = []struct {
	name string
	main string
}{
	// Accumulate a sum: 1+2+3+4+5 = 15.
	{"while-sum", `function main(): i32 { var i: i32 = 1; var s: i32 = 0; while (i <= 5) { s = s + i; i = i + 1; } return s; }`},
	// Early return out of the loop body: returns at i == 7.
	{"while-early-return", `function main(): i32 { var i: i32 = 0; while (i < 100) { if (i == 7) { return i; } i = i + 1; } return 0 - 1; }`},
	// Zero-iteration loop (false at entry): body never runs, s stays 7.
	{"while-zero-iter", `function main(): i32 { var s: i32 = 7; var i: i32 = 5; while (i < 5) { s = s + 1; } return s; }`},
	// Compound step (i += 2): s = 0+2+4 = 6.
	{"while-compound-step", `function main(): i32 { var s: i32 = 0; var i: i32 = 0; while (i < 6) { s = s + i; i = i + 2; } return s; }`},
	// Nested while loops: outer 4 × inner 3 = 12 increments.
	{"while-nested", `function main(): i32 { var c: i32 = 0; var i: i32 = 0; while (i < 4) { var j: i32 = 0; while (j < 3) { c = c + 1; j = j + 1; } i = i + 1; } return c; }`},
}

// TestSelfHostWhileIRX86_64 routes each while-loop case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostWhileIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range whileIRCases {
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

// TestSelfHostWhileIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostWhileIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host while wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range whileIRCases {
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
			watFile := filepath.Join(dir, "while_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("while wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
