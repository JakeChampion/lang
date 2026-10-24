package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWasmSelfHostClosureArray(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm closure-array e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	cases := []struct {
		name   string
		source string
		exit   int
	}{
		{"index-call", "function mk(s: i32): () => i32 { return (): i32 => { return s; }; } function main(): i32 { let fns: (() => i32)[] = [mk(42), mk(9)]; let c = fns[0]; return c(); }", 42},
		{"index-arg", "function adder(n: i32): (i32) => i32 { return (x: i32): i32 => { return x + n; }; } function main(): i32 { let fs: ((i32) => i32)[] = [adder(10), adder(20)]; let g = fs[1]; return g(22); }", 42},
		{"for-each-call", "function mk(s: i32): () => i32 { return (): i32 => { return s; }; } function main(): i32 { let fns: (() => i32)[] = [mk(10), mk(20), mk(12)]; let s: i32 = 0; for f in fns { s = s + f(); } return s; }", 42},
		{"lambda-elems", "function main(): i32 { let n: i32 = 5; let fns: (() => i32)[] = [(): i32 => { return n + 1; }, (): i32 => { return n * 2; }]; let a = fns[0]; let b = fns[1]; return a() + b(); }", 16},
		{"inline-cap-direct-index", "function main(): i32 { let n: i32 = 35; let fs = [(x: i32): i32 => { return x + n; }]; return fs[0](7); }", 42},
		{"inline-cap-multi-forin", "function main(): i32 { let a: i32 = 1; let b: i32 = 10; let fs = [(x: i32): i32 => { return x + a; }, (x: i32): i32 => { return x * b; }]; let s = 0; for f in fs { s = s + f(10); } return s; }", 111},
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
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n--- WAT ---\n%s", tc.name, code, tc.exit, wat)
			}
		})
	}
}
