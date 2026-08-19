package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// optArithNarrowBindIRCases pin an Option/Result match-EXPRESSION arm that does
// ARITHMETIC over a bound i64 payload and then narrows it with `as i32`
// (`match (o) { Some(x) => (x + 2) as i32, None => 0 }`) to the self-host IR path on
// x86-64 + wasm. The match-expression result is i32 (the cast narrows), but an arm
// computes a wide i64 intermediate over the payload first. The recognizer
// iife_arm_returns_narrowed_payload admitted only `name as i32` (the BARE payload);
// an arithmetic operand under the cast fell through, and because the result temp is
// i32 none of the wide-result admits fired either, so iife_payload_field_bindable
// returned false and the whole module bailed to the legacy AST emitter. #2691 adds
// iife_arm_returns_narrowed_payload_arith (the arith sibling), reusing the existing
// iife_payload_arith_kind classifier. i64 only — the f64 arith-then-narrow result
// temp is not yet width-lowered in the IIFE path, so it stays on the AST path (a
// separate follow-up). Each case is oracle-checked against the interpreter and
// returns <= 126. Mirrors self_host_opt_unused_wide_bind_ir_test.go.
var optArithNarrowBindIRCases = []struct {
	name string
	main string
}{
	// (payload + 2) as i32. 40 + 2 = 42.
	{"add-narrow", `function main(): i32 { var o: Option[i64] = Some(40); return match (o) { Some(x) => (x + 2) as i32, None => 0 }; }`},
	// (payload * 2) as i32. 40 * 2 = 80.
	{"mul-narrow", `function main(): i32 { var o: Option[i64] = Some(40); return match (o) { Some(x) => (x * 2) as i32, None => 0 }; }`},
	// None arm taken — the arith arm is not evaluated. 7.
	{"none-taken", `function main(): i32 { var o: Option[i64] = None; return match (o) { Some(x) => (x + 2) as i32, None => 7 }; }`},
	// Result[i64, i32], Ok arm arith-then-narrow. 40 - 5 = 35.
	{"result-sub", `function main(): i32 { var r: Result[i64, i32] = Ok(40); return match (r) { Ok(x) => (x - 5) as i32, Err(e) => 0 }; }`},
	// Regression: the bare-payload narrow (`x as i32`) was already on the IR path. 40.
	{"bare-narrow", `function main(): i32 { var o: Option[i64] = Some(40); return match (o) { Some(x) => x as i32, None => 0 }; }`},
}

// TestSelfHostOptArithNarrowBindIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostOptArithNarrowBindIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range optArithNarrowBindIRCases {
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

// TestSelfHostOptArithNarrowBindIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostOptArithNarrowBindIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host opt-arith-narrow-bind wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range optArithNarrowBindIRCases {
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
			watFile := filepath.Join(dir, "opt_arith_narrow_bind_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("opt-arith-narrow-bind wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
