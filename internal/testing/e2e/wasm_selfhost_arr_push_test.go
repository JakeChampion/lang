package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWasmSelfHostArrPush(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm append e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	cases := []struct {
		name   string
		source string
		exit   int
		stdout string
	}{
		{"append-len", "function main(): i32 { let a: i32[] = [1, 2, 3]; a = a.append(4); return a.len(); }", 4, ""},
		{"append-empty", "function main(): i32 { let a: i32[] = []; a = a.append(42); return a[0]; }", 42, ""},
		{"append-chain", "function main(): i32 { let a: i32[] = []; a = a.append(1); a = a.append(2); a = a.append(3); return a[0] + a[1] + a[2]; }", 6, ""},
		{"append-grow", "function main(): i32 { let a: i32[] = []; let i = 0; while (i < 10) { a = a.append(i); i = i + 1; } let s = 0; for x in a { s = s + x; } return s; }", 45, ""},
		{"append-string", "function main(): i32 { let xs: string[] = [\"a\"]; xs = xs.append(\"b\"); write(xs[1]); return xs.len(); }", 2, "b"},
		{"append-with-split", "function main(): i32 { let parts = \"a,b,c\".split(\",\"); let xs: i32[] = []; xs = xs.append(parts.len()); return xs[0]; }", 3, ""},
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
			out, _ := cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n--- WAT ---\n%s", tc.name, code, tc.exit, wat)
			}
			if tc.stdout != "" && string(out) != tc.stdout {
				t.Errorf("%s: wasm stdout = %q, want %q", tc.name, string(out), tc.stdout)
			}
		})
	}
}
