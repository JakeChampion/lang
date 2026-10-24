package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostIntToF64WasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host int-to-f64 wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	cases := []struct {
		name   string
		src    string
		opcode string
	}{
		{"u32-roundtrips-through-f64", `function main(): i32 {
    let u: u32 = 3000000000 as u32;
    let f: f64 = u as f64;
    let back: u32 = f as u32;
    if (back == u) { return 0; }
    return 1;
}`, "f64.convert_i32_u"},
		{"u64-max-is-above-1e19", `function main(): i32 {
    let i: i64 = 0 - 1i64;
    let u: u64 = i as u64;
    let f: f64 = u as f64;
    let threshold: f64 = 10000000000000000000.0f64;
    if (f > threshold) { return 0; }
    return 1;
}`, "f64.convert_i64_u"},
		{"negative-i32-stays-signed", `function main(): i32 {
    let n: i32 = 0 - 5;
    let f: f64 = n as f64;
    if (f < 0.0) { return 0; }
    return 1;
}`, "f64.convert_i32_s"},
		{"negative-i64-stays-signed", `function main(): i32 {
    let n: i64 = 0 - 5000000000;
    let f: f64 = n as f64;
    if (f < 0.0) { return 0; }
    return 1;
}`, "f64.convert_i64_s"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)

			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("wasm_ir_run failed: %v (%d bytes)", err, len(wat))
			}
			if !strings.Contains(string(wat), tc.opcode) {
				t.Errorf("emitted WAT does not contain %s — the width/signedness is not reaching the opcode selection", tc.opcode)
			}

			watPath := filepath.Join(t.TempDir(), "prog.wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watPath)
			out, runErr := run.CombinedOutput()
			code := run.ProcessState.ExitCode()
			if code != want {
				t.Errorf("wasm exit = %d, want %d (interp oracle); err=%v\n%s", code, want, runErr, out)
			}
		})
	}
}
