package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// floatMathIRCases exercise std/float's f64 math builtins — __sqrt_f64 /
// __floor_f64 / __ceil_f64 / __trunc_f64 / __abs_f64 / __round_f64 — through the
// self-host IR path on x86-64 + wasm. Most map to one hardware instruction on
// every backend (sqrtsd / roundsd / sign-mask on x86, fsqrt / frint* / fabs on
// arm64, f64.sqrt / floor / ceil / trunc / abs on wasm), so the IR path now
// lowers them (op_funary) instead of bailing — the
// FEATURE-AUDIT "float math builtins" row was self-host-blank.
//
// __round_f64 is round-half-away-from-zero (Go's math.Round): one instruction on
// arm64 (frinta), and on x86 (roundsd has no ties-away mode) and wasm
// (f64.nearest is ties-to-EVEN) built from trunc plus an exact fractional-part
// test. The half-integer cases below (2.5 -> 3, 99.5 -> 100) are exactly where
// ties-to-even would diverge; the three #7880 cases pin the classes where the
// shorter trunc(x+copysign(0.5,x)) identity diverged instead.
//
// These cases pin routing to the "ir" path via the pathprobe driver. (The older
// combined test used `-ir`, which silently skips the gated fast path when the
// module is not all_eligible, so it never verified the IR path.) `pow` lowers as
// an fpow fbin rather than an op_funary, so it is covered elsewhere.
//
// Each case casts its f64 result to i32 and returns a non-negative value kept
// <= 126 (the wasmtime exit-code truncation gap, cf. #2908), oracle-checked
// against the interpreter. FEATURE-AUDIT std/float row.
var floatMathIRCases = []struct {
	name string
	main string
}{
	{"sqrt", `return __sqrt_f64(16.0) as i32;`},
	{"sqrt-large", `return __sqrt_f64(10000.0) as i32;`},
	{"floor", `return __floor_f64(7.8) as i32;`},
	{"ceil", `return __ceil_f64(7.2) as i32;`},
	{"trunc", `return __trunc_f64(7.9) as i32;`},
	// abs of a negative input: exercises the sign-bit clear with a positive result.
	{"abs", `return __abs_f64(0.0 - 5.5) as i32;`},
	// round half-away: 2.5 -> 3 (ties-to-even would give 2), 99.5 -> 100.
	{"round-half", `return __round_f64(2.5) as i32;`},
	{"round-half-large", `return __round_f64(99.5) as i32;`},
	// round below / above the half: 2.4 -> 2, 2.6 -> 3.
	{"round-down", `return __round_f64(2.4) as i32;`},
	{"round-up", `return __round_f64(2.6) as i32;`},
	// Nested intrinsics: sqrt(abs(-16)) = 4.
	{"nested", `return __sqrt_f64(__abs_f64(0.0 - 16.0)) as i32;`},
	// Intrinsic result feeding f64 arithmetic: sqrt(2) * 10 = 14.14 -> 14.
	{"in-expr", `var x: f64 = 2.0; return (__sqrt_f64(x) * 10.0) as i32;`},
	// f64 local round-trip: floor of a stored value.
	{"via-local", `var y: f64 = 9.99; return __floor_f64(y) as i32;`},
	// round of an f64 local.
	{"round-via-local", `var z: f64 = 4.5; return __round_f64(z) as i32;`},
	// #7880, class 1: just below the tie. 0.49999999999999994 is 0.5 - 2^-54,
	// and x + 0.5 is the exact sum 1 - 2^-54 — precisely halfway between
	// 1 - 2^-53 and 1.0, which round-to-nearest-EVEN lifts to 1.0 before any
	// truncation sees it. The answer is 0 (so 10), not 1.
	{"round-just-below-tie", `return 10 + (__round_f64(0.49999999999999994) as i32);`},
	// #7880, class 2: the negative twin, -0.0 rather than -1 (so 10, not 9).
	{"round-just-below-tie-neg", `return 10 + (__round_f64(0.0 - 0.49999999999999994) as i32);`},
	// #7880, class 3 — the broad one: every already-integral double at or above
	// 2^52 must come back UNCHANGED. Past that the spacing is >= 1, so x + 0.5
	// rounds to a different integer and round(x) - x came out 1 (so 21, not 20).
	{"round-large-integral", `var x: f64 = 4503599627370497.0; return 20 + ((__round_f64(x) - x) as i32);`},
	{"round-large-integral-neg", `var x: f64 = 0.0 - 4503599627370497.0; return 20 + ((x - __round_f64(x)) as i32);`},
	// The infinities and NaN must come back unchanged: they reach the
	// |x - t| >= 0.5 test with a NaN difference, which the emulation has to
	// read as "no bump" (x86 ucomisd leaves CF set on the unordered compare;
	// wasm's f64.ge answers 0).
	{"round-inf", `var z: f64 = 0.0; var inf: f64 = 1.0 / z; if (__round_f64(inf) == inf) { return 7; } return 8;`},
	{"round-neg-inf", `var z: f64 = 0.0; var ninf: f64 = (0.0 - 1.0) / z; if (__round_f64(ninf) == ninf) { return 7; } return 8;`},
	{"round-nan", `var z: f64 = 0.0; var n: f64 = z / z; var r: f64 = __round_f64(n); if (r != r) { return 7; } return 8;`},
}

func floatMathIRSrc(mainBody string) string {
	return "function main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostFloatMathIRX86_64 routes each case through the self-hosted x86-64
// IR driver, oracle-checked, with routing pinned to the "ir" path.
func TestSelfHostFloatMathIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

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

// TestSelfHostFloatMathIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostFloatMathIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host float-math wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range floatMathIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(floatMathIRSrc(tc.main))
			want := interpExit(t, interpBin, string(src))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
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

// TestSelfHostFloatMathIRArm64 runs the same cases through the self-hosted
// arm64 IR emitter, assembled with the aarch64 cross-gcc and executed under
// qemu, oracle-checked against the interpreter.
//
// arm64 rounds with frinta (ties-away in hardware) while x86 and wasm emulate,
// so this is the target that makes the three self-host backends agree rather
// than merely each agree with itself — the disagreement #7880 describes was
// invisible while only two of the three ran these cases.
func TestSelfHostFloatMathIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

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
