package e2eselfhost

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// The IR builds an i64 or f64 constant either from source text or from its
// value (ir.op_const_i64 / ir.op_const_f64), and every backend read only the
// text: a value-built constant emitted an empty immediate on x86-64 and arm64
// and an empty `i64.const` / `f64.const` on wasm (#8996). The driver
// (examples/self_host/ir_const_numeric_run.fern) lowers this program, rebuilds
// every text constant from its value, and emits through the same substitution
// seam the CLI uses, so the program must answer exactly as it does compiled
// normally. The driver also exits 3 when a NaN payload, an infinity or
// negative zero loses its bits.
const irConstNumericSrc = `function main(): i32 {
    var big: i64 = 4294967303i64;
    var neg: i64 = 0i64 - 9007199254740993i64;
    var hex: i64 = 0x7fffffff00000001i64;
    var um: u64 = 18446744073709551615u64;
    var half: f64 = 1.5;
    var nz: f64 = -0.0;
    var huge: f64 = 1e300;
    var tiny: f64 = 4.9e-324;
    var bad: i32 = 0;
    if (big != 4294967296i64 + 7i64) { bad = bad + 1; }
    if (neg + 9007199254740993i64 != 0i64) { bad = bad + 2; }
    if ((hex >> 32) != 2147483647i64) { bad = bad + 4; }
    if (um != 0u64 - 1u64) { bad = bad + 8; }
    if (half * 2.0 != 3.0) { bad = bad + 16; }
    if (f64_bits(nz) != 0i64 - 9223372036854775807i64 - 1i64) { bad = bad + 32; }
    if (huge / 1e299 < 9.99 || huge / 1e299 > 10.01) { bad = bad + 64; }
    if (f64_bits(tiny) != 1i64) { bad = bad + 128; }
    return 50 + bad;
}
`

func irConstNumericEmit(t *testing.T, runner []string, bin, target string) string {
	t.Helper()
	cmd := runX86_64Bin(runner, bin, "-target", target)
	cmd.Stdin = strings.NewReader(irConstNumericSrc)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("ir_const_numeric_run -target %s: %v\n%s", target, err, errb.String())
	}
	if !strings.Contains(errb.String(), "rebuilt i64=9 f64=10") {
		t.Fatalf("-target %s: the driver rebuilt a different set of constants: %q", target, errb.String())
	}
	return out.String()
}

func TestSelfHostIRConstNumeric(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ir_const_numeric_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "ir_const_numeric_run.fern", "ir_const_numeric_run")

	t.Run("x86-64", func(t *testing.T) {
		asm := irConstNumericEmit(t, runner, bin, "x86-64-linux")
		if stderr, exit := hevRun(t, runner, buildBin(t, gcc, dir, "irconst_x86", asm)); exit != 50 {
			t.Fatalf("exit = %d, want 50\n%s", exit, stderr)
		}
	})
	t.Run("arm64", func(t *testing.T) {
		armgcc, qemu := arm64Tooling(t)
		asm := irConstNumericEmit(t, runner, bin, "arm64-linux")
		cmd := runArm64Bin(qemu, buildBinArm64(t, armgcc, dir, "irconst_arm64", asm))
		var eb strings.Builder
		cmd.Stderr = &eb
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 50 {
			t.Fatalf("exit = %d, want 50\n%s", code, eb.String())
		}
	})
	t.Run("wasm", func(t *testing.T) {
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH")
		}
		wat := irConstNumericEmit(t, runner, bin, "wasm32-wasi")
		if stderr, exit := wasmLcRun(t, dir, "irconst", wat); exit != 50 {
			t.Fatalf("exit = %d, want 50\n%s", exit, stderr)
		}
	})
}
