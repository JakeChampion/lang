package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostArm64FloatRuntimeGate pins the "float_transcendentals" need that
// gates asm_arm64_ir.fern's emit_rt_float_transcendentals.
//
// arm64 has no transcendental instruction, so sin/cos/exp/log/pow are calls into
// a ~400-instruction fdlibm bundle plus its constant table. That bundle used to
// be emitted UNCONDITIONALLY — every arm64 program carried it, including pure
// integer ones, where it was 406 of int_loop's 468 instructions (87%). Native
// arm64 has always gated the same bundle on usesFloatTranscendentals, so this
// was a backend-parity gap, not a deliberate size trade.
//
// Both directions are asserted, and they are not symmetric in risk: dropping the
// bundle when it IS needed is a link failure or a wrong answer, so the positive
// cases also RUN and check the computed value. A test that only asserted the
// absence would be satisfied by a backend that never emitted the bundle at all.
func TestSelfHostArm64FloatRuntimeGate(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	emit := func(t *testing.T, src string) string {
		t.Helper()
		var cmd *exec.Cmd
		if len(x86runner) == 0 {
			cmd = exec.Command(driverBin, "-target", "arm64-linux")
		} else {
			args := append(append([]string{}, x86runner[1:]...), driverBin, "-target", "arm64-linux")
			cmd = exec.Command(x86runner[0], args...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		out, err := cmd.Output()
		if err != nil || len(out) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		return string(out)
	}

	run := func(t *testing.T, name, asm string) int {
		t.Helper()
		asmPath := filepath.Join(dir, name+".s")
		binPath := filepath.Join(dir, name)
		if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
			t.Fatalf("write asm: %v", err)
		}
		if out, err := exec.Command(arm64gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
			t.Fatalf("gcc: %v\n%s", err, out)
		}
		inner := runArm64Bin(qemu, binPath)
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("%s did not exit normally", name)
		}
		return inner.ProcessState.ExitCode()
	}

	// bundleMarkers are definition-site strings that appear ONLY inside
	// emit_rt_float_transcendentals — a label definition and a rodata constant,
	// so a mere `bl` call site in user code cannot make them match.
	bundleMarkers := []string{"__fern_ksin:", ".Lfc_one:", "__fern_pow_f64:"}

	hasBundle := func(asm string) bool {
		for _, m := range bundleMarkers {
			if strings.Contains(asm, m) {
				return true
			}
		}
		return false
	}

	// Float-free programs must carry none of it. These use the same shapes as
	// the peephole test so a divergence shows up as one test passing, not both.
	t.Run("absent", func(t *testing.T) {
		cases := []struct {
			name string
			src  string
			want int
		}{
			{"arith", "function main(): i32 { return 2 + 3 * 4 - 1; }", 13},
			{"loop", "function main(): i32 { var s = 0; var i = 0; while (i < 10) { s = s + i * 2; i = i + 1; } return s; }", 90},
			{"call", "function add3(a: i32, b: i32, c: i32): i32 { return a + b + c; } function main(): i32 { return add3(20, 15, 7); }", 42},
			// f64 arithmetic and comparison lower to native FP instructions and
			// must NOT drag in the transcendental bundle.
			{"f64_arith", "function main(): i32 { var a: f64 = 6.5; var b: f64 = 2.0; var c: f64 = a * b - 1.0; if (c > 11.5) { return 7; } return 0; }", 7},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				asm := emit(t, tc.src)
				for _, m := range bundleMarkers {
					if strings.Contains(asm, m) {
						t.Errorf("float transcendental bundle emitted for a program that uses none (marker %q present)", m)
					}
				}
				if got := run(t, "absent_"+tc.name, asm); got != tc.want {
					t.Errorf("exit = %d, want %d", got, tc.want)
				}
			})
		}
	})

	// Each transcendental must mark the need on its own — one unmarked lowering
	// site is a dangling `bl` at link time. __*_f64 are builtins, so no import
	// is needed and the stdin driver can compile these directly.
	t.Run("present", func(t *testing.T) {
		cases := []struct {
			name string
			src  string
			want int
		}{
			// sin(2.0) ≈ 0.909 > 0
			{"sin", "function main(): i32 { var x: f64 = 2.0; if (__sin_f64(x) > 0.9) { return 1; } return 0; }", 1},
			// cos(0.0) == 1
			{"cos", "function main(): i32 { var x: f64 = 0.0; if (__cos_f64(x) > 0.99) { return 2; } return 0; }", 2},
			// exp(1.0) ≈ 2.718
			{"exp", "function main(): i32 { var x: f64 = 1.0; if (__exp_f64(x) > 2.7) { return 3; } return 0; }", 3},
			// log(e) ≈ 1
			{"log", "function main(): i32 { var x: f64 = 2.718281828459045; if (__log_f64(x) > 0.99) { return 4; } return 0; }", 4},
			// pow(2,10) == 1024
			{"pow", "function main(): i32 { var b: f64 = 2.0; var e: f64 = 10.0; if (__pow_f64(b, e) > 1023.5) { return 5; } return 0; }", 5},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				asm := emit(t, tc.src)
				if !hasBundle(asm) {
					t.Fatalf("%s: float transcendental bundle MISSING — the lowering marks no need, so the `bl` would dangle at link", tc.name)
				}
				if got := run(t, "present_"+tc.name, asm); got != tc.want {
					t.Errorf("exit = %d, want %d — the bundle is emitted but computes the wrong value", got, tc.want)
				}
			})
		}
	})
}
