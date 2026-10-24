package e2ecompiler

import (
	"bytes"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func TestSelfHostIntToF64X86_64IR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range intToF64Cases() {
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
			for _, want := range tc.x86Want {
				if !regexp.MustCompile(want).Match(asm) {
					t.Errorf("%s: emitted asm lacks %q — the width/signedness is not reaching instruction selection", tc.name, want)
				}
			}
			for _, avoid := range tc.x86Avoid {
				if regexp.MustCompile(avoid).Match(asm) {
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

func TestSelfHostIntToF64Arm64IR(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range intToF64Cases() {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			want := interpExit(t, interpBin, string(src))
			asm := runCapture(t, x86gcc, x86runner, driverBin, src, "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			for _, w := range tc.arm64Want {
				if !regexp.MustCompile(w).MatchString(string(asm)) {
					t.Errorf("%s: emitted arm64 asm lacks %q\n%s", tc.name, w, asm)
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
		{"u64-max-is-above-1e19", `function main(): i32 {
    let i: i64 = 0 - 1i64;
    let u: u64 = i as u64;
    let f: f64 = u as f64;
    let threshold: f64 = 10000000000000000000.0f64;
    if (f > threshold) { return 0; }
    return 1;
}`, []string{`shrq \$1, %rcx`, `addsd %xmm0, %xmm0`}, nil, []string{`ucvtf d\d+, x\d+\b`}},
		{"u64-below-2p63", `function main(): i32 {
    let u: u64 = 9223372036854775807i64 as u64;
    let f: f64 = u as f64;
    if (f > 9000000000000000000.0f64) { return 0; }
    return 1;
}`, []string{`cvtsi2sd`}, nil, []string{`ucvtf d\d+, x\d+\b`}},
		{"u32-roundtrips-through-f64", `function main(): i32 {
    let u: u32 = 3000000000 as u32;
    let f: f64 = u as f64;
    let back: u32 = f as u32;
    if (back == u) { return 0; }
    return 1;
}`, []string{`movl %[a-z0-9]+, %[a-z0-9]+\n\s+cvtsi2sd %[a-z0-9]+, %xmm0`}, nil, []string{`ucvtf d\d+, w\d+\b`}},
		{"negative-i32-stays-signed", `function main(): i32 {
    let n: i32 = 0 - 5;
    let f: f64 = n as f64;
    if (f < 0.0) { return 0; }
    return 1;
}`, []string{`cvtsi2sd %[a-z0-9]+, %xmm0`}, []string{`movl %[a-z0-9]+, %[a-z0-9]+\n\s+cvtsi2sd`}, []string{`scvtf d\d+, x\d+\b`}},
		{"negative-i64-stays-signed", `function main(): i32 {
    let n: i64 = 0 - 5000000000;
    let f: f64 = n as f64;
    if (f < 0.0) { return 0; }
    return 1;
}`, []string{`cvtsi2sd %[a-z0-9]+, %xmm0`}, []string{`shrq \$1, %rcx`}, []string{`scvtf d\d+, x\d+\b`}},
	}
}
