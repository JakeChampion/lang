package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The saturating operators `+|` / `-|` / `*|` / `<<|` (#5542) clamp to the operand
// type's [MIN, MAX] instead of wrapping. irlower.lower_sat_binary emits the
// clamp as a void-`if` + store-to-temp chain over ordinary IR ops (no new
// opcode), so every self-host IR backend lowers it unchanged. These cases
// mirror the native oracle in `internal/e2e/saturating_arith_test.go`.
var saturatingIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// i32: clamp high, clamp low, and the non-saturating passthrough.
	{"i32-add-hi", `function main(): i32 { var a: i32 = 2147483647; var b: i32 = 1; if ((a +| b) == 2147483647) { return 1; } return 0; }`, 1},
	{"i32-sub-lo", `function main(): i32 { var a: i32 = 0 - 2147483647 - 1; var b: i32 = 1; if ((a -| b) == a) { return 2; } return 0; }`, 2},
	{"i32-add-plain", `function main(): i32 { var a: i32 = 40; var b: i32 = 2; return a +| b; }`, 42},
	{"i32-sub-plain", `function main(): i32 { var a: i32 = 50; var b: i32 = 8; return a -| b; }`, 42},
	{"i32-mul-plain", `function main(): i32 { var a: i32 = 6; var b: i32 = 7; return a *| b; }`, 42},
	{"i32-mul-hi", `function main(): i32 { var a: i32 = 100000; if ((a *| a) == 2147483647) { return 3; } return 0; }`, 3},
	{"i32-mul-lo", `function main(): i32 { var a: i32 = 0 - 100000; var b: i32 = 100000; if ((a *| b) == (0 - 2147483647 - 1)) { return 4; } return 0; }`, 4},
	// The `MIN / -1 == MIN` total-division edge the round-trip check would
	// otherwise miss.
	{"i32-mul-min-neg1", `function main(): i32 { var a: i32 = 0 - 2147483647 - 1; var b: i32 = 0 - 1; if ((a *| b) == 2147483647) { return 5; } return 0; }`, 5},
	{"i32-mul-neg1-min", `function main(): i32 { var a: i32 = 0 - 1; var b: i32 = 0 - 2147483647 - 1; if ((a *| b) == 2147483647) { return 6; } return 0; }`, 6},
	{"i32-mul-zero", `function main(): i32 { var a: i32 = 0; var b: i32 = 0 - 2147483647 - 1; if ((a *| b) == 0) { return 7; } return 0; }`, 7},
	// The wrapping operators are untouched.
	{"i32-add-wraps", `function main(): i32 { var a: i32 = 2147483647; var b: i32 = 1; if ((a + b) == (0 - 2147483647 - 1)) { return 8; } return 0; }`, 8},
	// Precedence: `*` binds tighter than `+|`, `*|` tighter than `+`.
	{"prec-mul-first", `function main(): i32 { var a: i32 = 2; var b: i32 = 3; var c: i32 = 4; return a +| b * c; }`, 14},
	{"prec-satmul-first", `function main(): i32 { var a: i32 = 2; var b: i32 = 3; var c: i32 = 4; return a + b *| c; }`, 14},

	// i64 at full width — no wider host type, so add/sub pre-check and mul
	// post-checks the wrapped product.
	{"i64-add-hi", `function main(): i32 { var x: i64 = 9223372036854775807; var y: i64 = 5; if ((x +| y) == 9223372036854775807) { return 9; } return 0; }`, 9},
	{"i64-sub-lo", `function main(): i32 { var x: i64 = 0 - 9223372036854775807 - 1; var y: i64 = 5; if ((x -| y) == x) { return 10; } return 0; }`, 10},
	{"i64-mul-hi", `function main(): i32 { var x: i64 = 9223372036854775807; var y: i64 = 5; if ((x *| y) == 9223372036854775807) { return 11; } return 0; }`, 11},
	{"i64-mul-min-neg1", `function main(): i32 { var x: i64 = 0 - 9223372036854775807 - 1; var y: i64 = 0 - 1; if ((x *| y) == 9223372036854775807) { return 12; } return 0; }`, 12},
	{"i64-plain", `function main(): i32 { var x: i64 = 40; var y: i64 = 2; return (x +| y) as i32; }`, 42},
	{"i64-mul-plain", `function main(): i32 { var x: i64 = 6; var y: i64 = 7; return (x *| y) as i32; }`, 42},

	// u32 / u64 take the unsigned pre-checks (clamp low is 0, not MIN).
	{"u32-add-hi", `function main(): i32 { var p: u32 = 4294967290; var q: u32 = 10; if ((p +| q) == 4294967295) { return 13; } return 0; }`, 13},
	{"u32-sub-lo", `function main(): i32 { var p: u32 = 4294967290; var q: u32 = 10; if ((q -| p) == 0) { return 14; } return 0; }`, 14},
	{"u32-mul-hi", `function main(): i32 { var p: u32 = 4294967290; var q: u32 = 10; if ((p *| q) == 4294967295) { return 15; } return 0; }`, 15},
	{"u32-plain", `function main(): i32 { var p: u32 = 6; var q: u32 = 7; return (p *| q) as i32; }`, 42},
	{"u64-add-hi", `function main(): i32 { var m: u64 = 18446744073709551615; var n: u64 = 1; if ((m +| n) == 18446744073709551615) { return 16; } return 0; }`, 16},
	{"u64-sub-lo", `function main(): i32 { var m: u64 = 18446744073709551615; var n: u64 = 1; if ((n -| m) == 0) { return 17; } return 0; }`, 17},
	{"u64-mul-hi", `function main(): i32 { var m: u64 = 18446744073709551615; var n: u64 = 2; if ((m *| n) == 18446744073709551615) { return 18; } return 0; }`, 18},

	// u8 rides sub-i32 slots: the clamp is 255, and the saturated result is
	// in range by construction so the wrap mask never has to run.
	{"u8-add-hi", `function main(): i32 { var u: u8 = 250; var v: u8 = 10; if ((u +| v) == 255) { return 19; } return 0; }`, 19},
	{"u8-sub-lo", `function main(): i32 { var u: u8 = 250; var v: u8 = 10; if ((v -| u) == 0) { return 20; } return 0; }`, 20},
	{"u8-mul-hi", `function main(): i32 { var u: u8 = 250; var v: u8 = 10; if ((u *| v) == 255) { return 21; } return 0; }`, 21},
	{"u8-plain", `function main(): i32 { var u: u8 = 6; var v: u8 = 7; return (u *| v) as i32; }`, 42},

	// `<<|` post-checks by shifting back rather than pre-checking, so its
	// cases pin both directions of the round-trip at every width.
	{"shl-i32-hi", `function main(): i32 { var a: i32 = 1; var b: i32 = 31; if ((a <<| b) == 2147483647) { return 22; } return 0; }`, 22},
	{"shl-i32-lo", `function main(): i32 { var a: i32 = 0 - 2; var b: i32 = 31; if ((a <<| b) == (0 - 2147483647 - 1)) { return 23; } return 0; }`, 23},
	{"shl-i32-exact-min", `function main(): i32 { var a: i32 = 0 - 1; var b: i32 = 31; if ((a <<| b) == (0 - 2147483647 - 1)) { return 24; } return 0; }`, 24},
	{"shl-i32-plain", `function main(): i32 { var a: i32 = 21; var b: i32 = 1; return a <<| b; }`, 42},
	{"shl-i32-zero", `function main(): i32 { var a: i32 = 0; var b: i32 = 31; if ((a <<| b) == 0) { return 25; } return 0; }`, 25},
	{"shl-i32-mask", `function main(): i32 { var a: i32 = 42; var b: i32 = 32; return a <<| b; }`, 42},
	{"shl-u8-hi", `function main(): i32 { var u: u8 = 200; var v: u8 = 1; if ((u <<| v) == 255) { return 26; } return 0; }`, 26},
	{"shl-u8-plain", `function main(): i32 { var u: u8 = 21; var v: u8 = 1; return (u <<| v) as i32; }`, 42},
	{"shl-u32-hi", `function main(): i32 { var p: u32 = 4294967290; var q: u32 = 1; if ((p <<| q) == 4294967295) { return 27; } return 0; }`, 27},
	{"shl-u32-edge", `function main(): i32 { var p: u32 = 1; var q: u32 = 31; if ((p <<| q) == 2147483648) { return 28; } return 0; }`, 28},
	{"shl-i64-hi", `function main(): i32 { var x: i64 = 9223372036854775807; var y: i64 = 1; if ((x <<| y) == 9223372036854775807) { return 29; } return 0; }`, 29},
	{"shl-i64-lo", `function main(): i32 { var x: i64 = 0 - 1; var y: i64 = 63; if ((x <<| y) == (0 - 9223372036854775807 - 1)) { return 30; } return 0; }`, 30},
	{"shl-i64-plain", `function main(): i32 { var x: i64 = 21; var y: i64 = 1; return (x <<| y) as i32; }`, 42},
	{"shl-u64-hi", `function main(): i32 { var m: u64 = 18446744073709551615; var n: u64 = 1; if ((m <<| n) == 18446744073709551615) { return 31; } return 0; }`, 31},
	// Same tier as `<<`, left-associative.
	{"shl-prec", `function main(): i32 { var a: i32 = 1; var b: i32 = 2; var c: i32 = 3; return a <<| b + c; }`, 32},
}

// TestSelfHostSaturatingX86IR pins the saturating operators on the x86-64 IR
// backend of the self-hosted compiler.
func TestSelfHostSaturatingX86IR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	runIR := func(t *testing.T, name, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		emitted, err := cmd.Output()
		if err != nil || len(emitted) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		innerAsm := filepath.Join(dir, "sat_"+name+".s")
		innerBin := filepath.Join(dir, "sat_"+name)
		if err := os.WriteFile(innerAsm, emitted, 0o644); err != nil {
			t.Fatalf("write inner asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
			t.Fatalf("inner gcc for %q: %v\n%s", src, err, out)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(innerBin)
		} else {
			inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), innerBin)...)
		}
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("inner did not exit normally for %q", src)
		}
		return inner.ProcessState.ExitCode()
	}

	for _, tc := range saturatingIRCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.name, tc.src); got != tc.expected {
				t.Errorf("saturating x86 IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}

// TestSelfHostSaturatingWasmIR pins the same cases on the wasm IR backend,
// where the operand widths are native i32 / i64 rather than 64-bit registers
// holding narrower values.
func TestSelfHostSaturatingWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host saturating wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	runIR := func(t *testing.T, name, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		wat, err := cmd.Output()
		if err != nil || len(wat) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		watFile := filepath.Join(dir, "sat_"+name+".wat")
		if err := os.WriteFile(watFile, wat, 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		run := exec.Command("wasmtime", "run", watFile)
		_ = run.Run()
		if run.ProcessState == nil || !run.ProcessState.Exited() {
			t.Fatalf("wasmtime did not exit normally for %q:\n%s", src, wat)
		}
		return run.ProcessState.ExitCode()
	}

	for _, tc := range saturatingIRCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.name, tc.src); got != tc.expected {
				t.Errorf("saturating wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
