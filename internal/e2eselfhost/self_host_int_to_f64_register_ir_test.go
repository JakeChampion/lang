package e2eselfhost

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostIntToF64X86_64IR is the REGISTER-backend sibling of
// TestSelfHostIntToF64WasmIR: `<int> as f64` on the self-host x86-64 IR path
// (#6051).
//
// #5992 fixed the wasm half of this and recorded that the register backends
// "can ignore" the op's width + signedness, one 64-bit cvtsi2sd / scvtf
// covering all four conversions. That is true of three of the four — i32, u32
// (which rides its slot zero-extended) and i64 — and false of the fourth: x86
// has only a SIGNED int->double, so a u64 >= 2^63 converts to a NEGATIVE
// double. u64::MAX came out as -1.0, and the fixture asserting it exceeds 1e19
// returned 1 instead of 0. No fixture leg ran the self-host x86-64 path when
// #5992 landed, so the half that was still broken looked covered.
//
// The u64 case now goes through the round-to-odd halving (`y = (x>>1)|(x&1)`,
// convert, double) native emitFConvertI64 uses; u32 is zero-extended into the
// 64-bit source first. Each case asserts the emitted asm reached the expected
// instruction sequence — so a fix that merely changes the answer cannot pass by
// accident — then runs it against the interpreter oracle.
func TestSelfHostIntToF64X86_64IR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range intToF64Cases() {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			want := interpExit(t, interpBin, string(src))
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			if !strings.Contains(string(asm), ".Lir_") {
				t.Fatalf("%s did not lower through the IR (no .Lir_ labels)", tc.name)
			}
			for _, want := range tc.x86Want {
				if !strings.Contains(string(asm), want) {
					t.Errorf("%s: emitted asm lacks %q — the width/signedness is not reaching instruction selection", tc.name, want)
				}
			}
			for _, avoid := range tc.x86Avoid {
				if strings.Contains(string(asm), avoid) {
					t.Errorf("%s: emitted asm contains %q — the conversion took the wrong signedness", tc.name, avoid)
				}
			}
			bin := buildBin(t, gcc, dir, "i2f_"+tc.name, string(asm))
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

// TestSelfHostIntToF64Arm64IR — CI-gated arm64 counterpart. arm64 has both
// conversions, so the u64 case is one instruction (ucvtf) rather than x86's
// halving dance, but the bug was identical: an unconditional `scvtf d0, x0`
// read u64::MAX as -1.0.
func TestSelfHostIntToF64Arm64IR(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range intToF64Cases() {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			want := interpExit(t, interpBin, string(src))
			asm := runCapture(t, x86gcc, x86runner, driverBin, src, "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			for _, w := range tc.arm64Want {
				if !strings.Contains(string(asm), w) {
					t.Errorf("%s: emitted arm64 asm lacks %q", tc.name, w)
				}
			}
			progBin := buildBin(t, arm64gcc, dir, "i2farm_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// intToF64Cases are the four (width, signedness) combinations. The signed forms
// are here for the same reason the wasm test keeps them: a "fix" that made
// every conversion unsigned would satisfy the u32/u64 cases and silently break
// these.
func intToF64Cases() []struct {
	name      string
	src       string
	x86Want   []string
	x86Avoid  []string
	arm64Want []string
} {
	return []struct {
		name      string
		src       string
		x86Want   []string
		x86Avoid  []string
		arm64Want []string
	}{
		// u64::MAX as f64 is ~1.8446744e19, not -1.0 (#6051 — the fixture
		// u64_max_to_f64_is_huge, which returned 1 here and 0 natively).
		{"u64-max-is-above-1e19", `function main(): i32 {
    var i: i64 = 0 - 1i64;
    var u: u64 = i as u64;
    var f: f64 = u as f64;
    var threshold: f64 = 10000000000000000000.0f64;
    if (f > threshold) { return 0; }
    return 1;
}`, []string{"shrq $1, %rcx", "addsd %xmm0, %xmm0"}, nil, []string{"ucvtf d0, x0"}},
		// A u64 BELOW 2^63 must still take the plain signed convert — the
		// halving path is only for the values it cannot express.
		{"u64-below-2p63", `function main(): i32 {
    var u: u64 = 9223372036854775807i64 as u64;
    var f: f64 = u as f64;
    if (f > 9000000000000000000.0f64) { return 0; }
    return 1;
}`, []string{"cvtsi2sd"}, nil, []string{"ucvtf d0, x0"}},
		// u32 with bit 31 set: zero-extended into the 64-bit source, so the
		// signed convert is exact.
		{"u32-roundtrips-through-f64", `function main(): i32 {
    var u: u32 = 3000000000 as u32;
    var f: f64 = u as f64;
    var back: u32 = f as u32;
    if (back == u) { return 0; }
    return 1;
}`, []string{"movl %eax, %eax\n    cvtsi2sd %rax, %xmm0"}, nil, []string{"ucvtf d0, w0"}},
		{"negative-i32-stays-signed", `function main(): i32 {
    var n: i32 = 0 - 5;
    var f: f64 = n as f64;
    if (f < 0.0) { return 0; }
    return 1;
}`, []string{"cvtsi2sd %rax, %xmm0"}, []string{"movl %eax, %eax\n    cvtsi2sd"}, []string{"scvtf d0, x0"}},
		{"negative-i64-stays-signed", `function main(): i32 {
    var n: i64 = 0 - 5000000000;
    var f: f64 = n as f64;
    if (f < 0.0) { return 0; }
    return 1;
}`, []string{"cvtsi2sd %rax, %xmm0"}, []string{"shrq $1, %rcx"}, []string{"scvtf d0, x0"}},
	}
}
