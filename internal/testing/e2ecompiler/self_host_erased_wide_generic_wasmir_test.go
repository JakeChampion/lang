package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostErasedWideGenericWasm pins the wasm side of the erased-generic
// 64-bit widening (#5464): a module passing a 64-bit or f64 value through a
// bare-typevar (erased-generic) PASS-THROUGH fn (`ident[T](x: T): T`) LOWERS
// on the wasm IR path. The erased param/return/locals are typed i64 — the
// uniform 8-byte slot the register backends give every value — and the caller
// coerces its arg/result at the boundary (f64 <-> i64 reinterpret,
// i32/pointer <-> i64 extend/wrap), so the module validates and computes the
// right value under wasmtime.
func TestSelfHostErasedWideGenericWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping erased-wide generic wasm e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"erased-f64-roundtrip",
			`function ident[T](x: T): T { return x; } function main(): i32 { let d: f64 = ident[f64](2.5); if (d == 2.5) { return 42; } return 38; }`,
			42},
		{"erased-i64-roundtrip",
			`function ident[T](x: T): T { return x; } function main(): i32 { let big: i64 = ident[i64](4200000000 as i64); if (big == 4200000000 as i64) { return 42; } return 38; }`,
			42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			// The IR emitter never declares a `$__lit0` scratch local; finding one
			// means the module did not lower through the IR.
			if strings.Contains(string(wat), "$__lit0") {
				t.Errorf("%s did not lower through the IR (found $__lit0)", tc.name)
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
