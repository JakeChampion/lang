package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostU32RetCallWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host u32-return-call wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	runIR := func(t *testing.T, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		wat, err := cmd.Output()
		if err != nil || len(wat) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		watFile := filepath.Join(dir, "ir_prog.wat")
		if err := os.WriteFile(watFile, wat, 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		run := exec.Command("wasmtime", "run", watFile)
		_ = run.Run()
		if run.ProcessState == nil || !run.ProcessState.Exited() {
			t.Fatalf("wasmtime did not exit normally for %q:\n%s", src, wat)
		}
		return run.ProcessState.ExitCode()
	}

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		{"free-fn-shr", `function id32(x: u32): u32 { return x; } function main(): i32 { let a: u32 = 2147483649 as u32; return (id32(a) >> 25) as i32; }`, 64},
		{"method-shr", `struct P { n: u32 } function (self: P) get(): u32 { return self.n; } function main(): i32 { let p: P = P { n: 2147483649 as u32 }; return (p.get() >> 25) as i32; }`, 64},
		{"free-fn-div", `function id32(x: u32): u32 { return x; } function main(): i32 { let a: u32 = 4000000000 as u32; return ((id32(a) / 7) % 100) as i32; }`, 71},
		{"method-cmp", `struct P { n: u32 } function (self: P) get(): u32 { return self.n; } function main(): i32 { let p: P = P { n: 4000000000 as u32 }; if (p.get() > 2000000000) { return 12; } return 0; }`, 12},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("u32-return-call wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
