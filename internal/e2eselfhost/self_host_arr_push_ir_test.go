package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// arrPushIRCases exercise `arr.append(v)` (op_arr_push) through the self-host IR
// path on x86-64 + wasm.
//
// Like string_from_bytes_unchecked, the wasm IR backend emitted `op_arr_push` as a `call
// $__fern_arr_push` but `wasm_ir_run` had no gate to emit that helper — so any
// IR-path program using `.append` produced a wasm module with a dangling call that
// failed to link. x86-64 / arm64 already emitted it. The fix gates the standalone
// `wasm.arr_push_helper()` (push-only, so it doesn't double-define the separately
// gated slice helpers) on `module_emits_op(mod, "arr_push")`.
//
// Each case is oracle-checked against the interpreter, routing-pinned to "ir", and
// returns a non-negative value <= 126 (cf. #2908).
var arrPushIRCases = []struct {
	name string
	main string
}{
	// Append three, read length.
	{"len", `function main(): i32 { var a: i32[] = []; a = a.append(1); a = a.append(2); a = a.append(3); return a.len(); }`},
	// Append to a non-empty array, index the new element.
	{"index", `function main(): i32 { var a: i32[] = [10]; a = a.append(20); return a[1]; }`},
	// Append in a loop (exercises geometric growth / realloc), index midway.
	{"loop-grow", `function main(): i32 { var a: i32[] = []; var i: i32 = 0; while (i < 10) { a = a.append(i * i); i = i + 1; } return a[7]; }`},
	// Sum a loop-built array.
	{"loop-sum", `function main(): i32 { var a: i32[] = []; var i: i32 = 0; while (i < 5) { a = a.append(i + 1); i = i + 1; } var s: i32 = 0; for x in a { s = s + x; } return s; }`},
}

// TestSelfHostArrPushIRX86_64 routes each case through the self-hosted x86-64 IR
// driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostArrPushIRX86_64(t *testing.T) {
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

	for _, tc := range arrPushIRCases {
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

// TestSelfHostArrPushIRWasm runs the same cases through the wasm IR backend — the
// path the missing-helper bug affected.
func TestSelfHostArrPushIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arr_push wasm IR e2e")
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

	for _, tc := range arrPushIRCases {
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
			watFile := filepath.Join(dir, "arrpush_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("arr_push wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
