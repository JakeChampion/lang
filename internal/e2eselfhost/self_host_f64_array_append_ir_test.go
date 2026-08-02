package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// f64ArrayAppendIRCases close the f64[] `.append(v)` gap on the IR path: an
// `a = a.append(x)` on an `f64[]` local now lowers (previously it was the lone
// 8-byte-element append that still bailed to the AST emitter — i64[]/u64[]
// already lowered via arr_push_i64). The f64 value rides the IR value stack as
// raw 8 bytes, so the register backends reuse __fern_arr_push exactly like the
// i64 path; wasm32 (typed operand stack) uses an f64-typed $__fern_arr_push_f64
// companion to $__fern_arr_push_i64. The element-read side (a[i] via f64.load,
// for-in, .len, .with) already lowered, so this closes the write side.
//
// Each case is oracle-checked against the interpreter, routing-pinned to "ir",
// and returns a value <= 126 (cf. the wasmtime exit-code gap #2908).
var f64ArrayAppendIRCases = []struct {
	name string
	main string
}{
	// Append to a non-empty f64[], then read the length.
	{"f64-append-len", `function main(): i32 { var a: f64[] = [1.0]; a = a.append(2.0); a = a.append(3.0); return a.len(); }`},
	// Append to a non-empty f64[], then index the appended element (value round-trip).
	{"f64-append-index", `function main(): i32 { var a: f64[] = [1.5]; a = a.append(2.5); return a[1] as i32; }`},
	// Append onto an EMPTY f64[] literal (the geometric-growth first-alloc path).
	{"f64-append-empty-sum", `function main(): i32 { var a: f64[] = []; a = a.append(1.5); a = a.append(2.5); var s = 0.0; for x in a { s = s + x; } return s as i32; }`},
	// Repeated appends past the initial capacity (forces the grow-and-copy path).
	{"f64-append-grow-many", `function main(): i32 { var a: f64[] = []; var i = 0; while (i < 10) { a = a.append((i as f64) + 0.5); i = i + 1; } return a[7] as i32; }`},
	// i64[] append regression — must stay on the IR path (shares __fern_arr_push).
	{"i64-append-regress", `function main(): i32 { var a: i64[] = [1]; a = a.append(2); return a[1] as i32; }`},
}

// TestSelfHostF64ArrayAppendIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostF64ArrayAppendIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range f64ArrayAppendIRCases {
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

// TestSelfHostF64ArrayAppendIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostF64ArrayAppendIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host f64[]-append wasm IR e2e")
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

	for _, tc := range f64ArrayAppendIRCases {
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
			watFile := filepath.Join(dir, "f64_append_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("f64[]-append wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
