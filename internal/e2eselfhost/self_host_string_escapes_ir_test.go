package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stringEscapeIRCases exercise string-literal C-style escape sequences through
// the self-host IR path on x86-64 + wasm. Escapes are decoded in the lexer
// (scan_string in examples/self_host/lexer.fern: \t \n \r \0 \\ \" plus \xNN
// hex bytes), so a literal carrying any of them is an ordinary string box and
// lowers exactly like a plain literal — `.len()`, byte indexing (`s[i] as i32`),
// and `+` concat all stay on the IR path.
//
// This pins the foundational "String literals + escape sequences" audit row
// (docs/FEATURE-AUDIT.md) on the self-hosted compiler. Each escape is exactly
// one byte in a length-prefixed string (an embedded NUL via \0 / \x00 counts
// like any other byte — these are not C strings). Every case is oracle-checked
// against the interpreter, routing-pinned to "ir", and returns a value <= 126
// (wasmtime exit-code truncation, cf. #2908).
var stringEscapeIRCases = []struct {
	name string
	main string
}{
	// \t and \n each count as one byte: a TAB b LF c -> 5.
	{"newline-tab-len", `function main(): i32 { return "a\tb\nc".len(); }`},
	// Escaped backslash and double-quote: x \ y " z -> 5.
	{"backslash-quote", `function main(): i32 { return "x\\y\"z".len(); }`},
	// \xNN hex byte, read back by index: '\x41' == 'A' == 65.
	{"hex-escape-A", `function main(): i32 { return ("\x41")[0] as i32; }`},
	// Lowercase hex byte: '\x7a' == 'z' == 122.
	{"hex-escape-z", `function main(): i32 { return ("\x7a")[0] as i32; }`},
	// CR followed by an embedded NUL: both count -> 2.
	{"cr-null-len", `function main(): i32 { return "\r\0".len(); }`},
	// \xNN form of NUL is still one byte.
	{"null-hex-len", `function main(): i32 { return "\x00".len(); }`},
	// Byte index lands on the decoded LF: "a\nb"[1] == '\n' == 10.
	{"escape-byte-index", `function main(): i32 { var s: string = "a\nb"; return s[1] as i32; }`},
	// Concat of two single-escape literals: "\t" + "\n" -> 2.
	{"concat-escapes", `function main(): i32 { return ("\t" + "\n").len(); }`},
	// A mix of every escape in one literal: a TAB b \ c " d LF e -> 9.
	{"mixed-escapes-len", `function main(): i32 { return "a\tb\\c\"d\ne".len(); }`},
}

// TestSelfHostStringEscapesIRX86_64 routes each escape case through the
// self-hosted x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostStringEscapesIRX86_64(t *testing.T) {
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

	for _, tc := range stringEscapeIRCases {
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

// TestSelfHostStringEscapesIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostStringEscapesIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host string-escape wasm IR e2e")
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

	for _, tc := range stringEscapeIRCases {
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
			watFile := filepath.Join(dir, "strescape_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("string-escape wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
