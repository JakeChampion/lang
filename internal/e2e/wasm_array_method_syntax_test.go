package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestWasmArrayMethodSyntax guards the wasm backend's array method-syntax
// dispatch: `a.<field>(args)` whose `__method_Array_<field>` helper is in scope
// lowers to a call of that helper (the wasm mirror of the asm-backend fix).
// Before it, a non-intercepted array method fell through to `(i32.const 0)`.
// The helpers are defined inline so the single-file wasm driver exercises the
// dispatch without an import/bundle step (the dispatch keys off the helper
// being in scope, however it got there).
//
// TestWasm* so the test-e2e-wasm workflow (which installs wasmtime) runs it.
func TestWasmArrayMethodSyntax(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm array-method-syntax e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	const sumSq = "function __method_Array_sum_squared(arr: i32[]): i32 { var s: i32 = 0; var i: i32 = 0; while (i < arr.len()) { s = s + arr[i] * arr[i]; i = i + 1; } return s; }\n"
	const reversed = "function __method_Array_reversed(arr: i32[]): i32[] { var out: i32[] = []; var i: i32 = arr.len() - 1; while (i >= 0) { out = out.append(arr[i]); i = i - 1; } return out; }\n"

	cases := []struct {
		name   string
		source string
		exit   int
	}{
		// scalar-returning via method syntax.
		{"sum_squared", sumSq + "function main(): i32 { var a: i32[] = [5, 3, 8, 1]; return a.sum_squared(); }", 99},
		// array-returning via method syntax: result must be a live array
		// (indexable) and rc-swept, not garbage.
		{"reversed-index", reversed + "function main(): i32 { var a: i32[] = [5, 3, 8, 1]; var b: i32[] = a.reversed(); return b[0]; }", 1},
		{"reversed-sum", reversed + "function main(): i32 { var a: i32[] = [5, 3, 8, 1]; var b: i32[] = a.reversed(); var s: i32 = 0; for x in b { s = s + x; } return s; }", 17},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.source))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(t.TempDir(), "prog.wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", watPath)
			_, _ = cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n--- WAT ---\n%s", tc.name, code, tc.exit, wat)
			}
		})
	}
}
