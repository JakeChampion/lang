package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// strNeqIRCases pin string inequality (`a != b`) used as an `if` condition to the
// self-host IR path on x86-64 + wasm. String `==` is already pinned via the
// literal-match desugar (self_host_match_literal_ir_test.go — a `"a" => …` arm
// lowers to an `==` if-chain); `!=` is the negation arm of the same string-compare
// lowering, so this pins an existing-but-untested code path rather than asking for
// new compiler work. The eq-ne-mix case additionally guards that `==` and `!=`
// coexist in one module without bailing.
//
// Each case is routing-pinned via asm_pathprobe_run (assert path == "ir") and
// oracle-checked against the interpreter; every result is <= 126 (wasmtime
// exit-code truncation, cf. #2908). Mirrors self_host_nested_tuple_ir_test.go.
var strNeqIRCases = []struct {
	name string
	main string
}{
	// Inequality holds -> take the branch.
	{"ne-true", `function main(): i32 { var a: string = "foo"; if (a != "bar") { return 9; } return 0; }`},
	// Inequality is false (equal strings) -> fall through.
	{"ne-false", `function main(): i32 { var a: string = "foo"; if (a != "foo") { return 1; } return 5; }`},
	// `!=` between two string variables.
	{"ne-var", `function main(): i32 { var a: string = "abc"; var b: string = "abd"; if (a != b) { return 7; } return 0; }`},
	// `==` and `!=` coexisting in one module.
	{"eq-ne-mix", `function main(): i32 { var a: string = "hi"; var n = 0; if (a == "hi") { n = n + 3; } if (a != "bye") { n = n + 4; } return n; }`},
}

// TestSelfHostStrNeqIRX86_64 routes each case through the self-hosted x86-64 IR
// driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostStrNeqIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range strNeqIRCases {
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

// TestSelfHostStrNeqIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostStrNeqIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host str-neq wasm IR e2e")
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

	for _, tc := range strNeqIRCases {
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
			watFile := filepath.Join(dir, "str_neq_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("str-neq wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
