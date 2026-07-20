package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostErasedWideGenericWasm pins the wasm side of the erased-generic
// 64-bit widening: a module passing a 64-bit or f64 value through a
// bare-typevar (erased-generic) param is DEFERRED off the wasm IR path
// (LowerResult.erased_wide → wasm_ir_deferrals_ok) because wasm types erased
// param locals i32. Before the deferral, the f64 shape emitted type-INVALID
// WAT (f64.const flowing into an i32 param) that the wasmtime loader
// rejects — so the real assertion here is that the emitted module LOADS and
// computes the right value on the (AST) path it defers to.
func TestSelfHostErasedWideGenericWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping erased-wide generic wasm e2e")
	}
	gcc, runner := x86_64Tooling(t)
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

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"erased-f64-roundtrip",
			`function ident[T](x: T): T { return x; } function main(): i32 { var d: f64 = ident[f64](2.5); if (d == 2.5) { return 42; } return 38; }`,
			42},
		{"erased-i64-roundtrip",
			`function ident[T](x: T): T { return x; } function main(): i32 { var big: i64 = ident[i64](4200000000 as i64); if (big == 4200000000 as i64) { return 42; } return 38; }`,
			42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s (an invalid module fails to load)", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s = %d, want %d (38 = width truncated)", tc.name, got, tc.want)
			}
		})
	}
}
