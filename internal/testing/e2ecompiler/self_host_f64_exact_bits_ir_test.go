package e2ecompiler

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func TestSelfHostF64ExactBitsIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range f64ExactBitsCases() {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			want := interpExit(t, interpBin, string(src))
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			if !strings.Contains(string(asm), ".Lssa_") {
				t.Fatalf("%s did not lower through the IR (no .Lssa_ labels)", tc.name)
			}
			if strings.Contains(string(asm), ".double ") {
				t.Errorf("%s still emits a `.double` directive — should emit exact `.long` bits", tc.name)
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var run *exec.Cmd
			if len(runner) == 0 {
				run = exec.Command(bin)
			} else {
				run = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			run.Stdin = bytes.NewReader(nil)
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

func TestSelfHostF64ExactBitsIRArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range f64ExactBitsCases() {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			want := interpExit(t, interpBin, src)
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("%s on %s exited %d, want %d (interp oracle)\n%s", tc.name, target, code, want, stderr)
				}
			}
		})
	}
}

func f64ExactBitsCases() []struct {
	name string
	src  string
} {
	return []struct {
		name string
		src  string
	}{
		{"tie-2p53",
			`function main(): i32 { let x: f64 = 9007199254740993.0; let y: f64 = 9007199254740992.0; if (x == y) { return 42; } return 1; }`},
		{"tie-2p54",
			`function main(): i32 { let x: f64 = 18014398509481986.0; let y: f64 = 18014398509481984.0; if (x == y) { return 42; } return 1; }`},
		{"rt-0p1",
			`function main(): i32 { let x: f64 = 0.10000000000000001; let y: f64 = 0.1; if (x == y) { return 42; } return 1; }`},
		{"ordinary",
			`function main(): i32 { let a: f64 = 1.5; let b: f64 = 2.5; return (a + b) as i32; }`},
		{"int32min-low-half",
			`function main(): i32 { let x: f64 = 1.000000476837158203125; if (x > 1.0) { return 42; } return 1; }`},
	}
}
