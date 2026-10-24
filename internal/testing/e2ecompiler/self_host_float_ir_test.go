package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var floatMathIRCases = []struct {
	name string
	main string
}{
	{"sqrt", `return __sqrt_f64(16.0) as i32;`},
	{"sqrt-large", `return __sqrt_f64(10000.0) as i32;`},
	{"floor", `return __floor_f64(7.8) as i32;`},
	{"ceil", `return __ceil_f64(7.2) as i32;`},
	{"trunc", `return __trunc_f64(7.9) as i32;`},
	{"abs", `return __abs_f64(0.0 - 5.5) as i32;`},
	{"round-half", `return __round_f64(2.5) as i32;`},
	{"round-half-large", `return __round_f64(99.5) as i32;`},
	{"round-down", `return __round_f64(2.4) as i32;`},
	{"round-up", `return __round_f64(2.6) as i32;`},
	{"nested", `return __sqrt_f64(__abs_f64(0.0 - 16.0)) as i32;`},
	{"in-expr", `let x: f64 = 2.0; return (__sqrt_f64(x) * 10.0) as i32;`},
	{"via-local", `let y: f64 = 9.99; return __floor_f64(y) as i32;`},
	{"round-via-local", `let z: f64 = 4.5; return __round_f64(z) as i32;`},
	{"round-just-below-tie", `return 10 + (__round_f64(0.49999999999999994) as i32);`},
	{"round-just-below-tie-neg", `return 10 + (__round_f64(0.0 - 0.49999999999999994) as i32);`},
	{"round-large-integral", `let x: f64 = 4503599627370497.0; return 20 + ((__round_f64(x) - x) as i32);`},
	{"round-large-integral-neg", `let x: f64 = 0.0 - 4503599627370497.0; return 20 + ((x - __round_f64(x)) as i32);`},
	{"round-inf", `let z: f64 = 0.0; let inf: f64 = 1.0 / z; if (__round_f64(inf) == inf) { return 7; } return 8;`},
	{"round-neg-inf", `let z: f64 = 0.0; let ninf: f64 = (0.0 - 1.0) / z; if (__round_f64(ninf) == ninf) { return 7; } return 8;`},
	{"round-nan", `let z: f64 = 0.0; let n: f64 = z / z; let r: f64 = __round_f64(n); if (r != r) { return 7; } return 8;`},
}

func floatMathIRSrc(mainBody string) string {
	return "function main(): i32 { " + mainBody + " }\n"
}

func TestSelfHostFloatMathIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern", "drivers/asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range floatMathIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(floatMathIRSrc(tc.main))
			want := interpExit(t, interpBin, string(src))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

func TestSelfHostFloatMathIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host float-math wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range floatMathIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(floatMathIRSrc(tc.main))
			want := interpExit(t, interpBin, string(src))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "float_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("float-math wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

func TestSelfHostFloatMathIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range floatMathIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := floatMathIRSrc(tc.main)
			want := interpExit(t, interpBin, src)

			var cmd *exec.Cmd
			if len(x86runner) == 0 {
				cmd = exec.Command(driverBin, "-target", "arm64-linux")
			} else {
				args := append(append([]string{}, x86runner[1:]...), driverBin, "-target", "arm64-linux")
				cmd = exec.Command(x86runner[0], args...)
			}
			cmd.Stdin = bytes.NewReader([]byte(src))
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}

			asmPath := filepath.Join(dir, tc.name+".s")
			binPath := filepath.Join(dir, tc.name)
			if err := os.WriteFile(asmPath, asm, 0o644); err != nil {
				t.Fatalf("write asm: %v", err)
			}
			if out, err := exec.Command(arm64gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
				t.Fatalf("gcc: %v\n%s", err, out)
			}
			run := runArm64Bin(qemu, binPath)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("%s did not exit normally", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("float-math arm64 IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
