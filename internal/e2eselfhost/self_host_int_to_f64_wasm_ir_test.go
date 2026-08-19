package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostIntToF64WasmIR covers `<int> as f64` on the self-host WASM IR path
// (#5992). The IR op now carries the operand's WIDTH and SIGNEDNESS
// (ir.op_int_to_f64), which wasm needs to pick among four opcodes and the
// register backends can ignore — one 64-bit instruction (cvtsi2sd / scvtf)
// covers all four there, with a u32 arriving zero-extended. That asymmetry is
// why the missing information produced no visible failure on x86-64 or arm64
// and two different failures here:
//
//   - `u32 as f64` emitted f64.convert_i32_s, so a bit-31-set value converted
//     as NEGATIVE and the following i32.trunc_sat_f64_u saturated it to 0. A
//     wrong answer from a module that loads and runs.
//   - `u64 as f64` emitted the same i32 opcode on an i64 stack value, so the
//     module did not VALIDATE ("type mismatch: expected i32, found i64"). Note
//     both present as exit 1 under `wasmtime run` — a validation failure and a
//     program returning 1 are indistinguishable by exit code alone, which is
//     what hid the difference between them.
//
// Each case asserts the WAT reached the expected opcode (so a regression that
// merely changes the answer cannot pass by accident) and then runs it,
// cross-checked against the native interpreter.
func TestSelfHostIntToF64WasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host int-to-f64 wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	cases := []struct {
		name   string
		src    string
		opcode string
	}{
		// The two programs from #5992, which lived only in x86_64_test.go /
		// arm64_test.go and so had never been fed to a wasm driver.
		{"u32-roundtrips-through-f64", `function main(): i32 {
    var u: u32 = 3000000000 as u32;
    var f: f64 = u as f64;
    var back: u32 = f as u32;
    if (back == u) { return 0; }
    return 1;
}`, "f64.convert_i32_u"},
		{"u64-max-is-above-1e19", `function main(): i32 {
    var i: i64 = 0 - 1i64;
    var u: u64 = i as u64;
    var f: f64 = u as f64;
    var threshold: f64 = 10000000000000000000.0f64;
    if (f > threshold) { return 0; }
    return 1;
}`, "f64.convert_i64_u"},
		// The signed forms must keep their opcodes: a fix that made everything
		// unsigned would pass the two cases above and silently break these.
		{"negative-i32-stays-signed", `function main(): i32 {
    var n: i32 = 0 - 5;
    var f: f64 = n as f64;
    if (f < 0.0) { return 0; }
    return 1;
}`, "f64.convert_i32_s"},
		{"negative-i64-stays-signed", `function main(): i32 {
    var n: i64 = 0 - 5000000000;
    var f: f64 = n as f64;
    if (f < 0.0) { return 0; }
    return 1;
}`, "f64.convert_i64_s"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)

			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("wasm_ir_run -ir failed: %v (%d bytes)", err, len(wat))
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
