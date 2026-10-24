package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostU32ContainerWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host u32-container wasm IR e2e")
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
		{"struct-field-shr", `struct SU { n: u32 } function main(): i32 { let p: SU = SU { n: 2147483649 as u32 }; return (p.n >> 25) as i32; }`, 64},
		{"enum-payload-shr", `enum EU { U(u32), N } function main(): i32 { let e: EU = EU.U(2147483649); match (e) { EU.U(x) => { return (x >> 25) as i32; }, EU.N => { return 99; } } }`, 64},
		{"tuple-elem-shr", `function main(): i32 { let t: (u32, i32) = (2147483649, 1); return (t.0 >> 25) as i32; }`, 64},
		{"array-literal-shr", `function main(): i32 { return ([2147483649 as u32, 1 as u32][0] >> 25) as i32; }`, 64},
		{"array-slice-shr", `function main(): i32 { let a: u32[] = [2147483649 as u32, 1 as u32, 2 as u32]; return (a[0:2][0] >> 25) as i32; }`, 64},
		{"struct-field-div", `struct SU { n: u32 } function main(): i32 { let p: SU = SU { n: 4000000000 as u32 }; return ((p.n / (7 as u32)) % (100 as u32)) as i32; }`, 71},
		{"struct-field-cmp", `struct SU { n: u32 } function main(): i32 { let p: SU = SU { n: 4000000000 as u32 }; if (p.n > (2000000000 as u32)) { return 11; } return 0; }`, 11},
		{"tuple-elem-cmp", `function main(): i32 { let t: (u32, i32) = (4000000000, 1); if (t.0 > (2000000000 as u32)) { return 12; } return 0; }`, 12},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("u32-container wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
