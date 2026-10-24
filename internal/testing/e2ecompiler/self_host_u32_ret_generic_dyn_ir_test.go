package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostU32RetGenericDynWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host u32-return generic/dyn wasm IR e2e")
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
		{"generic-shr", `function idg[T](x: T): T { return x; } function main(): i32 { let a: u32 = 2147483649 as u32; return (idg(a) >> 25) as i32; }`, 64},
		{"generic-twocall-div", `function idg[T](x: T): T { return x; } function main(): i32 { let a: u32 = 4000000000 as u32; let b: u32 = 7 as u32; return ((idg(a) / idg(b)) % (100 as u32)) as i32; }`, 71},
		{"iife-shr", `function main(): i32 { let a: u32 = 2147483649 as u32; let r: u32 = (if (a > (0 as u32)) { a } else { 0 as u32 }) >> 25; return r as i32; }`, 64},
		{"dyn-shr", `trait Val { function v(self: Self): u32; } struct B { n: u32 } impl Val for B { function v(self: Self): u32 { return self.n; } } function main(): i32 { let b: B = B { n: 2147483649 as u32 }; let d: dyn Val = b; return (d.v() >> 25) as i32; }`, 64},
		{"dyn-twocall-div", `trait Val { function v(self: Self): u32; function d7(self: Self): u32; } struct B { n: u32, d: u32 } impl Val for B { function v(self: Self): u32 { return self.n; } function d7(self: Self): u32 { return self.d; } } function main(): i32 { let b: B = B { n: 4000000000 as u32, d: 7 as u32 }; let d: dyn Val = b; return ((d.v() / d.d7()) % (100 as u32)) as i32; }`, 71},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("u32-return generic/dyn wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
