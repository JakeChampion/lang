package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostRcRuntimeWasm — the Perceus RC runtime helpers via the
// self-hosted wasm backend (examples/self_host/wasm.fern). The wasm32
// mirror of TestSelfHostRcRuntimeX86_64 / ...Arm64: the rc word is an i32
// at [data-8], the helpers (__fern_rc_inc / __fern_rc_dec /
// __fern_rc_is_unique / __fern_rc_underflow_count) plus the raw-memory
// pokes (__alloc / __load_i32 / __store_i32) are emitted into the wasm
// module (gated on use), and a program hand-builds an rc-headered object
// via __alloc + __store_i32 to exercise them directly. This is the
// additive Phase-0c foundation for wasm RC — array layout migration +
// inc/dec call sites ride on it in later slices.
//
// Reuses the shared rcRuntimeCases (defined in self_host_rc_runtime_test.go):
// the `return <expr>;` result becomes the wasm proc_exit code, same as the
// asm backends, so the expected exit codes carry over unchanged.
func TestSelfHostRcRuntimeWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm RC e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "wasm.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	for _, tc := range rcRuntimeCases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", "--dir", dir, watPath)
			_, _ = cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n--- WAT ---\n%s", tc.name, code, tc.exit, wat)
			}
		})
	}
}
