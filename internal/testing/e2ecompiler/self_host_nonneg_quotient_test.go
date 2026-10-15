package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ssabounds.nonneg_quotients turns a signed `x / 2^k` into `x >> k` and
// `x % 2^k` into `x & (2^k - 1)` where `x` is at least zero. Each case answers
// what the interpreter answers on every backend; the negative ones reach a
// division whose dividend is below zero, where the rewrite would be wrong.
var nonnegQuotientCases = []struct{ name, src string }{
	{"ctz_lanes", `@noinline function lanes(x: u64): i32 { return __ctz64(x) / 8 * 10 + __ctz64(x) % 8; }
function main(): i32 { return lanes(1u64 << 21) + lanes(0u64); }`},
	{"guarded", `@noinline function q(k: i32): i32 { if (k >= 0) { return k / 16 * 10 + k % 4; } return 1; }
function main(): i32 { return q(77) + q(0 - 77); }`},
	{"length", `@noinline function half(xs: i32[]): i32 { return xs.len() / 2 + xs.len() % 2 * 10; }
function main(): i32 { return half([1, 2, 3, 4, 5]); }`},
	{"loop_index", `@noinline function sum(xs: u8[]): i32 { let t: i32 = 0; let i: i32 = 0; while (i < xs.len()) { t = t + i / 4 + i % 4; i = i + 1; } return t; }
function main(): i32 { let xs: u8[] = [1u8, 2u8, 3u8, 4u8, 5u8, 6u8, 7u8, 8u8, 9u8]; return sum(xs); }`},
	{"masked", `@noinline function hi(k: i32): i32 { return (k & 255) / 32 * 10 + (k & 255) % 32; }
function main(): i32 { return hi(0 - 3); }`},
	{"scan_set_result", `@noinline function at(s: string): i32 { let tabs: u8[] = [0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 1u8]; let j: i32 = __scan_set(s, 0, tabs); return j / 2 * 10 + j % 2; }
function main(): i32 { return at("abcde\tf"); }`},
	{"negative_dividend", `@noinline function q(k: i32): i32 { return (k / 8 + 10) * 10 + k % 8 + 10; }
function main(): i32 { return q(0 - 13); }`},
	{"guard_admits_negative", `@noinline function q(k: i32): i32 { if (k >= 0 - 1) { return (k / 2 + 10) * 10 + k % 2 + 10; } return 1; }
function main(): i32 { return q(0 - 1); }`},
	{"i64_ctz_lanes", `@noinline function lanes(x: u64): i64 { return (__ctz64(x) as i64) / 8i64 * 10i64 + (__ctz64(x) as i64) % (8 as i64); }
function main(): i32 { return (lanes(1u64 << 21) + lanes(0u64)) as i32; }`},
	{"i64_length", `@noinline function half(xs: i32[]): i64 { return (xs.len() as i64) / 2i64 + (xs.len() as i64) % 2i64 * 10i64; }
function main(): i32 { return half([1, 2, 3, 4, 5]) as i32; }`},
	{"widened_byte", `@noinline function nib(b: u8): i32 { return (b as i32) / 16 * 10 + (b as i32) % 16; }
function main(): i32 { return nib(100u8); }`},
	{"i64_negative_dividend", `@noinline function q(k: i32): i64 { return ((k as i64) / 8i64 + 10i64) * 10i64 + (k as i64) % 8i64 + 10i64; }
function main(): i32 { return q(0 - 13) as i32; }`},
}

// nonnegQuotientTwins pairs a division by 8 whose dividend is proven at least
// zero with one whose dividend may be negative: only the second keeps the
// sign fixup (a shift by 31, or 63 for an i64, and the bias added before the
// shift).
var nonnegQuotientTwins = []struct{ name, proven, unproven string }{
	{"guarded",
		`@noinline function q(k: i32): i32 { if (k >= 0) { return k / 8; } return 0; } function main(): i32 { return q(80); }`,
		`@noinline function q(k: i32): i32 { if (k >= 0 - 1) { return k / 8; } return 0; } function main(): i32 { return q(80); }`},
	{"ctz",
		`@noinline function lanes(x: u64): i32 { return __ctz64(x) / 8; } function main(): i32 { return lanes(1u64 << 40); }`,
		`@noinline function lanes(x: u64): i32 { return (__ctz64(x) - 1) / 8; } function main(): i32 { return lanes(1u64 << 41); }`},
	{"remainder",
		`@noinline function low(xs: i32[]): i32 { return xs.len() % 8; } function main(): i32 { return low([1, 2, 3]); }`,
		`@noinline function low(xs: i32[]): i32 { return (xs.len() - 4) % 8; } function main(): i32 { return low([1, 2, 3, 4, 5, 6, 7]); }`},
	{"i64_widened",
		`@noinline function lanes(x: u64): i64 { return (__ctz64(x) as i64) / 8i64; } function main(): i32 { return lanes(1u64 << 40) as i32; }`,
		`@noinline function lanes(x: u64): i64 { return ((__ctz64(x) - 1) as i64) / 8i64; } function main(): i32 { return lanes(1u64 << 41) as i32; }`},
}

func TestSelfHostNonnegQuotientX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")
	emit := func(t *testing.T, src string) string {
		asm := string(runCapture(t, gcc, runner, driverBin, []byte(src+"\n")))
		if !strings.Contains(asm, ".Lssa_") {
			t.Fatalf("did not lower through the IR (no .Lssa_ labels)")
		}
		return asm
	}
	runExit := func(bin string) int {
		var run *exec.Cmd
		if len(runner) == 0 {
			run = exec.Command(bin)
		} else {
			run = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = run.Run()
		return run.ProcessState.ExitCode()
	}
	for _, tc := range nonnegQuotientCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src+"\n")
			if code := runExit(buildBin(t, gcc, dir, "nnq_"+tc.name, emit(t, tc.src))); code != want {
				t.Errorf("exited %d, want %d (interp oracle)", code, want)
			}
		})
	}
	for _, tw := range nonnegQuotientTwins {
		t.Run("no_sign_fixup/"+tw.name, func(t *testing.T) {
			proven := emit(t, tw.proven)
			unproven := emit(t, tw.unproven)
			signs := func(asm string) int { return strings.Count(asm, "$31,") + strings.Count(asm, "$63,") }
			if got, base := signs(proven), signs(unproven); got >= base {
				t.Fatalf("the dividend's sign was not used: %d sign shifts against the unproven twin's %d", got, base)
			}
			for name, tc := range map[string]struct{ src, asm string }{"proven": {tw.proven, proven}, "unproven": {tw.unproven, unproven}} {
				want := interpExit(t, interpBin, tc.src+"\n")
				if code := runExit(buildBin(t, gcc, dir, "nnqtwin_"+tw.name+"_"+name, tc.asm)); code != want {
					t.Errorf("%s exited %d, want %d", name, code, want)
				}
			}
		})
	}
}

func TestSelfHostNonnegQuotientArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")
	for _, tc := range nonnegQuotientCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src+"\n")
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("the self-host arm64 compiler emitted nothing")
			}
			cmd := runArm64Bin(qemu, buildBin(t, arm64gcc, dir, "nnq_"+tc.name, string(asm)))
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("arm64 exited %d, want %d (interp oracle)", code, want)
			}
		})
	}
}

func TestSelfHostNonnegQuotientWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")
	for _, tc := range nonnegQuotientCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src+"\n")
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed: %v", err)
			}
			watFile := filepath.Join(dir, "nnq_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatal(err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally")
			}
			if code := rcmd.ProcessState.ExitCode(); code != want {
				t.Errorf("wasm exited %d, want %d (interp oracle)", code, want)
			}
		})
	}
}
