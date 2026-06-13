package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostU64UnsignedWasmIR is the wasm sibling of TestSelfHostU64UnsignedIR:
// it proves the wasm stack-IR backend (wasm_ir.fern) lowers u64 as UNSIGNED in
// the i64 domain (#2904) — i64.lt_u/le_u/gt_u/ge_u compares, i64.shr_u, and
// i64.div_u/rem_u. Expected values are the unsigned-correct answers (the Go
// reference interpreter agrees); each is bit-63-sensitive so it differs from the
// signed op. Exit codes are kept <= 125 (WASI proc_exit). Reuses
// u64UnsignedCases for the programs.
func TestSelfHostU64UnsignedWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host u64 unsigned wasm IR e2e")
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

	// The unsigned-correct exit code for each shared case (interp-verified).
	expected := map[string]int{
		"gt-u":        7,
		"lt-u":        7,
		"shr-logical": 2,
		"div-u":       9,
		"rem-u":       5,
		"param-ret":   2,
		"combined":    111,
	}
	for _, tc := range u64UnsignedCases {
		want, ok := expected[tc.name]
		if !ok {
			t.Fatalf("no expected value for case %q", tc.name)
		}
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			watFile := filepath.Join(dir, "ir_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("u64 unsigned wasm IR %q = %d, want %d", tc.name, got, want)
			}
		})
	}
}
