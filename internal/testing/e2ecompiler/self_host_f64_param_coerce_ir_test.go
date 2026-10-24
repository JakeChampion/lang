package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostF64ParamCoerceIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host f64-param-coerce IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	cases := []struct {
		name string
		src  string
		exit int
	}{
		{"int-literal-arg", `function addhalf(x: f64): f64 { return x + 0.5; } function main(): i32 { return addhalf(3) as i32; }`, 3},
		{"float-arg-unchanged", `function addhalf(x: f64): f64 { return x + 0.5; } function main(): i32 { return addhalf(3.25) as i32; }`, 3},
		{"f64-local-arg", `function addhalf(x: f64): f64 { return x + 0.5; } function main(): i32 { let v: f64 = 2.5; return addhalf(v) as i32; }`, 3},
		{"mixed-params", `function m(a: f64, b: i32, c: f64): f64 { return a + c + (b as f64); } function main(): i32 { return m(1, 2, 3) as i32; }`, 6},
		{"i64-param-unaffected", `function d(x: i64): i64 { return x * 2; } function main(): i32 { return (d(21) % 97) as i32; }`, 42},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed: %v", err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally (likely type-invalid WAT — the bug):\n%s", wat)
			}
			want := interpExit(t, interpBin, tc.src)
			if want != tc.exit {
				t.Fatalf("interp oracle returned %d, but the case expects %d — fix the case", want, tc.exit)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s: wasm exited %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}
