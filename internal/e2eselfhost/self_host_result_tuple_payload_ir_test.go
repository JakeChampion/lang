package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// resultTuplePayloadIRCases close a Result payload gap: a `Result[T, E]` whose T
// or E is itself a tuple — e.g. `Result[(i32, i32), string]`, matched and the
// payload's elements read (`t.0` / `t.1`) — now lowers on the IR path. The bug was
// in `opt_payload_type`: its Result T-vs-E comma split counted only `[`/`]`, not
// `(`/`)`, so a tuple payload's inner comma was mistaken for the T-E separator
// (T parsed as `(i32` instead of `(i32, i32)`), failing payload recovery and
// bailing the module to AST. (Option works regardless — it returns the whole inner
// type without splitting.) The fix makes that split also count parens.
//
// Each case is oracle-checked against the interpreter, routing-pinned to "ir", and
// returns a value <= 126 (cf. the wasmtime exit-code gap #2908).
var resultTuplePayloadIRCases = []struct {
	name string
	main string
}{
	// Ok payload is a tuple; read both elements.
	{"ok-tuple", `function main(): i32 { var r: Result[(i32, i32), string] = Ok((3, 4)); match (r) { Ok(t) => { return t.0 + t.1; }, Err(e) => { return 0; } } }`},
	// Same type, Err arm taken (string payload).
	{"err-string", `function main(): i32 { var r: Result[(i32, i32), string] = Err("ab"); match (r) { Ok(t) => { return t.0 + t.1; }, Err(e) => { return e.len(); } } }`},
	// The Err type is the tuple (mirror): exercises the E side of the split.
	{"err-tuple", `function main(): i32 { var r: Result[i32, (i32, i32)] = Err((5, 6)); match (r) { Ok(n) => { return n; }, Err(t) => { return t.0 + t.1; } } }`},
	// Scalar-Result regression (must stay on the IR path).
	{"scalar-regress", `function main(): i32 { var r: Result[i32, string] = Ok(5); match (r) { Ok(n) => { return n; }, Err(e) => { return 0; } } }`},
}

// TestSelfHostResultTuplePayloadIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostResultTuplePayloadIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range resultTuplePayloadIRCases {
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

// TestSelfHostResultTuplePayloadIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostResultTuplePayloadIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host Result-tuple-payload wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range resultTuplePayloadIRCases {
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
			watFile := filepath.Join(dir, "result_tuple_payload_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("Result-tuple-payload wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
