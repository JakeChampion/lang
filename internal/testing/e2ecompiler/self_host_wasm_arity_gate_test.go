package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostWasmArityGate(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "drivers/wasm_run.fern", "drivers/wasm_ir_run.fern")
	wasmRunDriver := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")
	irDriver := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "wasm_ir_run")

	run := func(t *testing.T, bin string, src string) ([]byte, []byte, int) {
		t.Helper()
		cmd := runX86_64Bin(runner, bin)
		cmd.Stdin = bytes.NewReader([]byte(src))
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, _ := cmd.Output()
		return out, stderr.Bytes(), cmd.ProcessState.ExitCode()
	}

	rejects := []struct {
		name string
		src  string
	}{
		{"too-few", "function f(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return f(1); }"},
		{"too-many", "function g(a: i32): i32 { return a; } function main(): i32 { return g(1, 2); }"},
		{"nested", "function h(a: i32, b: i32): i32 { return a * b; } function main(): i32 { let s = 0; while (s < 3) { s = s + h(1); } return s; }"},
	}
	accepts := []struct {
		name string
		src  string
		want int
	}{
		{"exact", "function f(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return f(40, 2); }", 42},
		{"default-arg", "function inc(n: i32, by: i32 = 1): i32 { return n + by; } function main(): i32 { return inc(41); }", 42},
		{"shadowed", "function dbl(n: i32): i32 { return n * 2; } function pick(f: (i32) => i32, n: i32): i32 { return f(n); } function main(): i32 { return pick(dbl, 21); }", 42},
	}

	drivers := []struct {
		name string
		bin  string
	}{
		{"wasm_run", wasmRunDriver},
		{"wasm_ir_run", irDriver},
	}
	for _, d := range drivers {
		for _, tc := range rejects {
			t.Run(d.name+"/reject-"+tc.name, func(t *testing.T) {
				out, errOut, code := run(t, d.bin, tc.src)
				if code != 1 {
					t.Errorf("driver exited %d, want 1 (reject)", code)
				}
				if !strings.Contains(string(errOut), "E004") {
					t.Errorf("stderr = %q, want an E004 diagnostic", errOut)
				}
				if len(out) != 0 {
					t.Errorf("driver emitted %d bytes for a mis-arity program, want 0", len(out))
				}
			})
		}
		for _, tc := range accepts {
			t.Run(d.name+"/accept-"+tc.name, func(t *testing.T) {
				out, errOut, code := run(t, d.bin, tc.src)
				if code != 0 {
					t.Fatalf("driver exited %d (stderr %q), want 0 (accept)", code, errOut)
				}
				if len(out) == 0 {
					t.Fatal("driver emitted 0 bytes for a valid program")
				}
				if _, err := exec.LookPath("wasmtime"); err != nil {
					t.Skip("wasmtime not on PATH; emitted WAT not executed")
				}
				watPath := filepath.Join(dir, "arity_"+tc.name+".wat")
				if err := os.WriteFile(watPath, out, 0o644); err != nil {
					t.Fatalf("write wat: %v", err)
				}
				wt := exec.Command("wasmtime", "run", watPath)
				_ = wt.Run()
				if wt.ProcessState == nil || !wt.ProcessState.Exited() {
					t.Fatalf("wasmtime did not exit normally")
				}
				if c := wt.ProcessState.ExitCode(); c != tc.want {
					t.Errorf("program exited %d, want %d", c, tc.want)
				}
			})
		}
	}
}
