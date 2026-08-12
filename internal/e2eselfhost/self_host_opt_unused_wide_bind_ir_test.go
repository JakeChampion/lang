package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// optUnusedWideBindIRCases pin an Option/Result match-EXPRESSION whose arm binds an
// i64/f64 payload to a name the arm body never references (`Some(x) => 1`) to the
// self-host IR path on x86-64 + wasm. The StmtMatch binder already produces the
// correct width-typed payload slot, and an i32 (or wildcard `Some(_)`) binding
// already lowered; only the eligibility gate `iife_payload_field_bindable` rejected
// an i64/f64 payload bound to an unread name (its result temp was conservatively
// assumed to need the payload width), bailing the whole module to the legacy AST
// emitter. #2691 admits a DEAD wide binding (the arm never mentions the name, so the
// payload width is irrelevant to the result temp). Each case is oracle-checked
// against the interpreter and returns <= 126. Mirrors self_host_iife_i64_annot_ir_test.go.
var optUnusedWideBindIRCases = []struct {
	name string
	main string
}{
	// Option[f64], Some arm binds an unused x, returns a constant. 1.
	{"opt-f64-unused", `function main(): i32 { var o: Option[f64] = Some(3.5); return match (o) { Some(x) => 1, None => 0 }; }`},
	// Option[f64] = None — the None arm taken (distinct exit). 7.
	{"opt-f64-none-taken", `function main(): i32 { var o: Option[f64] = None; return match (o) { Some(x) => 1, None => 7 }; }`},
	// Result[f64, i32], Ok arm binds an unused x. 1.
	{"result-f64-unused", `function main(): i32 { var r: Result[f64, i32] = Ok(3.5); return match (r) { Ok(x) => 1, Err(e) => 0 }; }`},
	// Option[i64] (8-byte payload) bound to an unread name. 4.
	{"opt-i64-unused", `function main(): i32 { var o: Option[i64] = Some(9000000000); return match (o) { Some(x) => 4, None => 0 }; }`},
	// The match-expression result bound into a local, unused f64 payload. 1.
	{"opt-f64-var-bind", `function main(): i32 { var o: Option[f64] = Some(3.5); var r: i32 = match (o) { Some(x) => 1, None => 0 }; return r; }`},
	// Regression: a USED f64 binding (`Some(x) => x as i32`) was already on the IR
	// path via the wide-read classifier — it must stay there. 3.5 as i32 = 3.
	{"opt-f64-used", `function main(): i32 { var o: Option[f64] = Some(3.5); return match (o) { Some(x) => x as i32, None => 0 }; }`},
}

// TestSelfHostOptUnusedWideBindIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostOptUnusedWideBindIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range optUnusedWideBindIRCases {
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

// TestSelfHostOptUnusedWideBindIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostOptUnusedWideBindIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host opt-unused-wide-bind wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range optUnusedWideBindIRCases {
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
			watFile := filepath.Join(dir, "opt_unused_wide_bind_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("opt-unused-wide-bind wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
