package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// resultTupleIRCases close the comma-containing-tuple-element story (after nested
// tuples): a `Result[T, E]` element of a tuple — `(1, Ok(5))`, accessed via
// `match (t.1)` — now lowers on the IR path. `Result[T, E]` has an internal comma
// but is bracketed, so the now-depth-aware tag decoders keep it whole; the only
// remaining blockers were the explicit "Option-only" exclusions. The full
// `Result[T, E]` tag (a bare `Ok(x)` cannot name E) comes from the binding's
// tuple TYPE annotation — and the checker rejects an un-annotated Result, so the
// annotation is always present. `opt_payload_type` already recovers both arms.
//
// Each case is oracle-checked against the interpreter, routing-pinned to "ir", and
// returns a value <= 126 (cf. the wasmtime exit-code gap #2908).
var resultTupleIRCases = []struct {
	name string
	main string
}{
	// Ok payload of a Result tuple element round-trips through a match.
	{"result-ok-elem", `function main(): i32 { var t: (i32, Result[i32, string]) = (1, Ok(5)); match (t.1) { Ok(n) => { return n; }, Err(e) => { return 0; } } }`},
	// Err payload (a string) reaches the Err arm.
	{"result-err-elem", `function main(): i32 { var t: (i32, Result[i32, string]) = (1, Err("ab")); match (t.1) { Ok(n) => { return n; }, Err(e) => { return e.len(); } } }`},
	// Result as the FIRST element, with a scalar sibling.
	{"result-first-elem", `function main(): i32 { var t: (Result[i32, string], i32) = (Ok(7), 3); match (t.0) { Ok(n) => { return n + t.1; }, Err(e) => { return 0; } } }`},
	// Returned from a function (the tag comes from the return-type annotation).
	{"result-ret-tuple", "function f(): (i32, Result[i32, string]) { return (9, Ok(5)); }\nfunction main(): i32 { var t = f(); match (t.1) { Ok(n) => { return t.0 + n; }, Err(e) => { return 0; } } }"},
	// Option-in-tuple regression (must stay on the IR path).
	{"option-elem-regress", `function main(): i32 { var t: (i32, Option[i32]) = (1, Some(5)); match (t.1) { Some(n) => { return n; }, None => { return 0; } } }`},
}

// TestSelfHostResultTupleIRX86_64 routes each case through the self-hosted x86-64
// IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostResultTupleIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range resultTupleIRCases {
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

// TestSelfHostResultTupleIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostResultTupleIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host Result-in-tuple wasm IR e2e")
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

	for _, tc := range resultTupleIRCases {
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
			watFile := filepath.Join(dir, "result_tuple_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("Result-in-tuple wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
