package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// constStringIRCases pin a direct STRING operation (`.len()`, concat) on a bare
// const-string reference to the self-host IR path on x86-64 + wasm. A
// `const NAME: string = "hi"` desugars to a zero-arg string-returning function;
// a bare `NAME` in value position lowers to a call of that const fn. The const
// ident has no LOCAL slot, so `expr_is_str`'s ExprIdent arm (which only checked
// local string slots) returned false for it — and the `.len()` dispatch site
// then emitted op_arr_len (an array-header read) on a STRING box instead of
// op_str_len. str_len and arr_len read different length fields, so `NAME.len()`
// silently miscompiled to 0 on the x86 IR backend (wasm + interp were correct).
// #2691 widens expr_is_str: a no-local-slot ident that is a const fn with a
// `string` return type now reads as a string, so the dispatch picks op_str_len.
// Each case is oracle-checked against the interpreter and returns <= 126.
var constStringIRCases = []struct {
	name string
	main string
}{
	// The regressing case: `.len()` on a const string. "hi" -> 2.
	{"const-str-len", `const NAME: string = "hi"; function main(): i32 { return NAME.len(); }`},
	// A longer const string. "abc" -> 3.
	{"const-str-len3", `const NAME: string = "abc"; function main(): i32 { return NAME.len(); }`},
	// Concat of a const string with a literal, then length. "hi"+"xx" -> 4.
	{"const-str-concat", `const NAME: string = "hi"; function main(): i32 { return (NAME + "xx").len(); }`},
	// Regression: a const-i32 reference (no string involvement) still resolves. 8.
	{"const-i32-ref", `const NAME: i32 = 8; function main(): i32 { return NAME; }`},
	// Regression: `.len()` on a LOCAL string slot was always correct. 2.
	{"local-str-len", `function main(): i32 { var s: string = "hi"; return s.len(); }`},
	// A literal carrying escaped bytes exercises asmcore.escape_for_ascii's
	// multi-byte-escape branches (\n and \") when the const is emitted as a
	// `.ascii` directive (#4379 rewrote that escaper to a u8[] buffer). The
	// decoded string is a\nb"c -> 5 bytes; oracle-checked against the interp.
	{"const-str-escapes", `const NAME: string = "a\nb\"c"; function main(): i32 { return NAME.len(); }`},
}

// TestSelfHostConstStringIRX86_64 routes each case through the self-hosted x86-64
// IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostConstStringIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range constStringIRCases {
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

// TestSelfHostConstStringIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostConstStringIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host const-string wasm IR e2e")
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

	for _, tc := range constStringIRCases {
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
			watFile := filepath.Join(dir, "const_string_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("const-string wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
