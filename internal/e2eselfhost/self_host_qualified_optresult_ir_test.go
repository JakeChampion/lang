package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// qualifiedOptResultIRCases widen the self-host IR subset: the qualified built-in
// Option/Result construction spellings — `Option.Some(x)`, `Option.None`,
// `Result.Ok(x)`, `Result.Err(x)` — now lower on the IR path. Previously only the
// bare forms (`Some(x)` / `Ok(x)` / `None`) lowered; a qualified construction made
// the whole module IR-ineligible and fell back to the AST emitter (which mis-lowers
// it as `# unresolved ident: <Enum>`). The qualified forms produce the identical
// value as the bare ones (the same op_opt_make / op_opt_none box), so they share a
// `lower_opt_make_payload` helper.
//
// Each case is oracle-checked against the interpreter, routing-pinned to "ir", and
// returns a value <= 126 (cf. the wasmtime exit-code gap #2908).
var qualifiedOptResultIRCases = []struct {
	name string
	main string
}{
	// Option.Some payload round-trips through a match.
	{"option-some", "function f(): Option[i32] { return Option.Some(42); }\nfunction main(): i32 { var o = f(); match (o) { Some(n) => { return n; }, None => { return 0; } } }"},
	// Option.None takes the None arm.
	{"option-none", "function f(): Option[i32] { return Option.None; }\nfunction main(): i32 { var o = f(); match (o) { Some(n) => { return n; }, None => { return 7; } } }"},
	// Result.Ok payload round-trips.
	{"result-ok", "function f(): Result[i32, string] { return Result.Ok(13); }\nfunction main(): i32 { var r = f(); match (r) { Ok(n) => { return n; }, Err(e) => { return 0; } } }"},
	// Result.Err payload (a string) reaches the Err arm.
	{"result-err", "function f(): Result[i32, string] { return Result.Err(\"bad\"); }\nfunction main(): i32 { var r = f(); match (r) { Ok(n) => { return n; }, Err(e) => { return e.len(); } } }"},
	// Qualified construction composes with the try-operator.
	{"qual-with-try", "function g(): Result[i32, string] { return Result.Ok(20); }\nfunction f(): Result[i32, string] { var n = g()?; return Result.Ok(n + 5); }\nfunction main(): i32 { var r = f(); match (r) { Ok(n) => { return n; }, Err(e) => { return 0; } } }"},
}

// TestSelfHostQualifiedOptResultIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostQualifiedOptResultIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range qualifiedOptResultIRCases {
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

// TestSelfHostQualifiedOptResultIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostQualifiedOptResultIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host qualified Option/Result wasm IR e2e")
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

	for _, tc := range qualifiedOptResultIRCases {
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
			watFile := filepath.Join(dir, "qual_optresult_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("qualified Option/Result wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
