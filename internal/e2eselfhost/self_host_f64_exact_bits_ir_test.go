package e2eselfhost

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostF64ExactBitsIR pins that the self-host IR emitters lay down the
// EXACT IEEE-754 bits of an f64 literal (round-to-nearest, ties-to-even),
// independent of the downstream assembler — issue #5434, the emit-side sibling
// of SH-004.
//
// Before this, const_f64 emitted `.double <decimal>` and deferred the decimal->
// binary conversion to the assembler. GNU `as` rounds `.double` ties half-UP,
// so a literal on a ULP tie got the wrong bits: 9007199254740993.0 (2^53+1,
// not representable) must round to 2^53, but GNU as encoded 2^53+2, so
// `x == 9007199254740992.0` came out FALSE where the language (interp, native
// codegen, glibc strtod) says TRUE. The emit now computes the bits with
// util.parse_f64_bits and writes them as two little-endian `.long` halves, so
// the value is correct no matter which assembler consumes the .s.
//
// Each case is oracle-checked against the interpreter and routing-pinned to the
// IR path (.Lir_ labels present).
func TestSelfHostF64ExactBitsIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range f64ExactBitsCases() {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			want := interpExit(t, interpBin, string(src))
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			if !strings.Contains(string(asm), ".Lir_") {
				t.Fatalf("%s fell back to the AST path (no .Lir_ labels)", tc.name)
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

// TestSelfHostF64ExactBitsIRArm64 — CI-gated arm64 counterpart: the same
// cases through the self-host arm64 IR path's `.long` halves, assembled by
// aarch64 GNU gcc and run under qemu against the interp oracle.
func TestSelfHostF64ExactBitsIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range f64ExactBitsCases() {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			want := interpExit(t, interpBin, string(src))
			asm := runCapture(t, x86gcc, x86runner, driverBin, src, "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, "exactbits_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
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
		// The tie: 2^53+1 rounds to 2^53 (even), so it equals 2^53. GNU as's
		// half-up rounding of `.double` got this wrong; exact bits get it right.
		{"tie-2p53",
			`function main(): i32 { var x: f64 = 9007199254740993.0; var y: f64 = 9007199254740992.0; if (x == y) { return 42; } return 1; }`},
		// A neighbouring tie one binade up: 2^54+2 rounds to 2^54 (even).
		{"tie-2p54",
			`function main(): i32 { var x: f64 = 18014398509481986.0; var y: f64 = 18014398509481984.0; if (x == y) { return 42; } return 1; }`},
		// 17-significant-digit round-trip spelling of 0.1 must equal 0.1.
		{"rt-0p1",
			`function main(): i32 { var x: f64 = 0.10000000000000001; var y: f64 = 0.1; if (x == y) { return 42; } return 1; }`},
		// Sanity: ordinary literals still compute correctly through the new path.
		{"ordinary",
			`function main(): i32 { var a: f64 = 1.5; var b: f64 = 2.5; return (a + b) as i32; }`},
		// A constant whose LOW 32-bit half is exactly 0x80000000 (1 + 2^-21 =
		// bits 0x3FF0000080000000). util.i32_to_string's generic sign flip
		// wraps on INT32_MIN and rendered the half as a bare "-", so the emit
		// produced `.long -, 1072693248` and gcc rejected the .s outright.
		{"int32min-low-half",
			`function main(): i32 { var x: f64 = 1.000000476837158203125; if (x > 1.0) { return 42; } return 1; }`},
	}
}
