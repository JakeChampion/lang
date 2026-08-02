package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// floatIntrinsicCases exercise the cheap f64 math intrinsics emitted
// by the self-hosted compiler: __abs_f64 / __sqrt_f64 / __floor_f64 /
// __ceil_f64 / __trunc_f64 / __round_f64. The -nostdlib native
// backends can't link libm, so these lower to single SSE / FP
// instructions (sqrtsd, roundsd, andpd-style sign-mask; fsqrt, frintm,
// frintp, frintz, frinta, fabs). Each program returns an observable
// i32 (the intrinsic result cast to i32, or a tolerance comparison).
// Values cross-checked against Go's math package.
var floatIntrinsicCases = []struct {
	name string
	src  string
	exit int
}{
	{"abs-neg", "function main(): i32 { return __abs_f64(0.0 - 5.5) as i32; }", 5},
	{"abs-pos", "function main(): i32 { return __abs_f64(3.0) as i32; }", 3},
	{"sqrt-exact", "function main(): i32 { return __sqrt_f64(16.0) as i32; }", 4},
	{"sqrt-tol", "function main(): i32 { var r: f64 = __sqrt_f64(2.0); if (r > 1.41 && r < 1.42) { return 7; } return 0; }", 7},
	{"floor", "function main(): i32 { return __floor_f64(7.9) as i32; }", 7},
	{"ceil", "function main(): i32 { return __ceil_f64(7.1) as i32; }", 8},
	{"trunc", "function main(): i32 { return __trunc_f64(7.9) as i32; }", 7},
	{"round-up", "function main(): i32 { return __round_f64(2.5) as i32; }", 3},
	{"round-down", "function main(): i32 { return __round_f64(2.4) as i32; }", 2},
}

// floatTranscendentalCases exercise the self-hosted compiler's f64
// transcendentals: __sin_f64 / __cos_f64 / __exp_f64 / __log_f64 /
// __pow_f64. x86-64 uses the x87 FPU (fsin/fcos/fyl2x/f2xm1); arm64
// uses polynomial approximations (the ISA has no transcendentals).
// Results are checked to a tolerance (the i32 cast truncates). Values
// cross-checked against Go's math package.
var floatTranscendentalCases = []struct {
	name string
	src  string
	exit int
}{
	{"exp-0", "function main(): i32 { return __exp_f64(0.0) as i32; }", 1},
	{"exp-2", "function main(): i32 { return __exp_f64(2.0) as i32; }", 7},
	{"exp-e-tol", "function main(): i32 { var r: f64 = __exp_f64(1.0); if (r > 2.71 && r < 2.72) { return 7; } return 0; }", 7},
	{"log-tol", "function main(): i32 { var r: f64 = __log_f64(2.0); if (r > 0.69 && r < 0.70) { return 7; } return 0; }", 7},
	{"log-10", "function main(): i32 { return __log_f64(10.0) as i32; }", 2},
	{"exp-log-roundtrip", "function main(): i32 { var r: f64 = __log_f64(__exp_f64(3.0)); if (r > 2.999 && r < 3.001) { return 7; } return 0; }", 7},
	{"sin-0", "function main(): i32 { return __sin_f64(0.0) as i32; }", 0},
	{"sin-halfpi-tol", "function main(): i32 { var r: f64 = __sin_f64(1.5707963); if (r > 0.999 && r < 1.001) { return 7; } return 0; }", 7},
	{"cos-0", "function main(): i32 { return __cos_f64(0.0) as i32; }", 1},
	{"cos-pi-tol", "function main(): i32 { var r: f64 = __cos_f64(3.1415926); if (r > 0.0 - 1.001 && r < 0.0 - 0.999) { return 7; } return 0; }", 7},
	{"pow-int", "function main(): i32 { return __pow_f64(2.0, 5.0) as i32; }", 32},
	{"pow-sqrt-tol", "function main(): i32 { var r: f64 = __pow_f64(2.0, 0.5); if (r > 1.41 && r < 1.42) { return 7; } return 0; }", 7},
	{"pow-3-2", "function main(): i32 { return __pow_f64(3.0, 2.0) as i32; }", 9},
}

// TestSelfHostFloatTranscendentalsIRX86_64 proves the transcendentals lower
// through the IR path (not just the legacy AST emitter): irlower maps
// __sin_f64/__cos_f64/__exp_f64/__log_f64 to op_funary fsin/fcos/fexp/flog and
// __pow_f64 to the fpow fbin, which asm_ir emits as the same x87 FPU sequences
// the AST backend uses. For each case the path prober must report "ir" (the
// module is IR-eligible) and the IR-built binary must hit the same exit the AST
// path does — same fixed oracle values as TestSelfHostFloatTranscendentalsX86_64.
func TestSelfHostFloatTranscendentalsIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range floatTranscendentalCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\" (transcendentals should now be IR-eligible)", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "tr_ir_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s (IR path) exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostFloatTranscendentalsX86_64 — x87 FPU transcendentals.
func TestSelfHostFloatTranscendentalsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range floatTranscendentalCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
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
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostFloatIntrinsicsX86_64 — the self-hosted x86-64 emitter's
// cheap libm intrinsics (plan item 4, cheap subset).
func TestSelfHostFloatIntrinsicsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range floatIntrinsicCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
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
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostFloatTranscendentalsArm64 — CI-gated arm64 counterpart of the
// transcendental IR test. asm_arm64.emit_module routes IR-eligible modules
// through emit_function_via_ir, so once irlower makes the transcendentals
// eligible, asm_ir_run (-target arm64) emits them via asm_arm64_ir's fsin/fcos/fexp/flog/fpow
// branches — `bl __fern_<op>_f64` into the polynomial-approx runtime helpers that
// emit_runtime always defines. Same fixed oracle exits as the x86 test.
func TestSelfHostFloatTranscendentalsArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range floatTranscendentalCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, "tr_arm64_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostFloatIntrinsicsArm64 — CI-gated arm64 counterpart.
func TestSelfHostFloatIntrinsicsArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range floatIntrinsicCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
