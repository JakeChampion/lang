package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// nestedOptResultIRCases close the last seam in the Option/Result nesting story: a
// fully-matched `Option[Result[T, E]]` (the outer Some bound, then the inner Result
// matched and its payload read) now lowers on the IR path. The bug was in
// `some_opt_type`: for `var o: Option[Result[..]] = Some(Ok(x))` it inferred o's
// type from the construction, and `elem_type_tag(Ok(x))` defaults an Ok/Err payload
// to "i32" — so o was mis-recorded as `Option[i32]` and that wrong inference
// preempted the authoritative annotation. The inner `match (r)` then found no
// Result type on the bound slot and bailed the module to AST. Fix: `some_opt_type`
// returns "" when the Some payload is itself an Ok/Err construction, so the
// binding's annotation (`Option[Result[T, E]]`) wins. (`Option[Option[T]]` was
// already fine — a Some payload types cleanly.)
//
// Each case is oracle-checked against the interpreter, routing-pinned to "ir", and
// returns a value <= 126 (cf. the wasmtime exit-code gap #2908).
var nestedOptResultIRCases = []struct {
	name string
	main string
}{
	// Some(Ok(x)) — inner Ok payload read.
	{"some-ok", `function main(): i32 { var o: Option[Result[i32, string]] = Some(Ok(5)); match (o) { Some(r) => { match (r) { Ok(n) => { return n; }, Err(e) => { return 0; } } }, None => { return 0; } } }`},
	// Some(Err(s)) — inner Err payload (string) read.
	{"some-err", `function main(): i32 { var o: Option[Result[i32, string]] = Some(Err("ab")); match (o) { Some(r) => { match (r) { Ok(n) => { return n; }, Err(e) => { return e.len(); } } }, None => { return 7; } } }`},
	// Option[Option[T]] regression (Some payload types cleanly).
	{"opt-opt-regress", `function main(): i32 { var o: Option[Option[i32]] = Some(Some(5)); match (o) { Some(r) => { match (r) { Some(n) => { return n; }, None => { return 0; } } }, None => { return 0; } } }`},
	// Unannotated Some(scalar) regression (some_opt_type still infers it).
	{"unannot-some-regress", `function main(): i32 { var o = Some(7); match (o) { Some(n) => { return n; }, None => { return 0; } } }`},
}

// TestSelfHostNestedOptResultIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostNestedOptResultIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range nestedOptResultIRCases {
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

// TestSelfHostNestedOptResultIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostNestedOptResultIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host nested Option/Result wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range nestedOptResultIRCases {
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
			watFile := filepath.Join(dir, "nested_opt_result_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("nested Option/Result wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
