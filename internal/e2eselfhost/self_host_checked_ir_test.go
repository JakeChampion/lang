package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The checked operators `+?` / `-?` / `*?` (#5542) yield `Some(result)` when
// the exact result fits the operand type and `None` on overflow.
// irlower.lower_checked_binary emits the clamp condition lower_sat_binary
// tests (the same per-backend-proven shape), then constructs `None` /
// `Some(wrapped)` as a void-`if` + store-to-temp over op_opt_none /
// op_opt_make — the Option rides a default (un-i64-marked) pointer slot, so
// every self-host IR backend lowers it unchanged. Each program matches the
// result and returns a distinctive exit code, mirroring the native oracle in
// `internal/e2e/checked_arith_test.go`.
var checkedIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// i32 signed add / sub.
	{"i32-add-overflow", `function main(): i32 { var a: i32 = 2147483647; var b: i32 = 1; match (a +? b) { Some(v) => { return 1; }, None => { return 11; } } }`, 11},
	{"i32-add-ok", `function main(): i32 { var a: i32 = 40; var b: i32 = 2; match (a +? b) { Some(v) => { return v; }, None => { return 99; } } }`, 42},
	{"i32-sub-underflow", `function main(): i32 { var a: i32 = 0 - 2147483647 - 1; var b: i32 = 1; match (a -? b) { Some(v) => { return 2; }, None => { return 12; } } }`, 12},
	{"i32-sub-ok", `function main(): i32 { var a: i32 = 50; var b: i32 = 8; match (a -? b) { Some(v) => { return v; }, None => { return 99; } } }`, 42},
	// i32 signed mul, incl. the (-1, MIN) division-round-trip edge.
	{"i32-mul-overflow", `function main(): i32 { var a: i32 = 100000; var b: i32 = 100000; match (a *? b) { Some(v) => { return 3; }, None => { return 13; } } }`, 13},
	{"i32-mul-neg1-min", `function main(): i32 { var a: i32 = 0 - 1; var b: i32 = 0 - 2147483647 - 1; match (a *? b) { Some(v) => { return 4; }, None => { return 14; } } }`, 14},
	{"i32-mul-min-neg1", `function main(): i32 { var a: i32 = 0 - 2147483647 - 1; var b: i32 = 0 - 1; match (a *? b) { Some(v) => { return 5; }, None => { return 15; } } }`, 15},
	{"i32-mul-ok", `function main(): i32 { var a: i32 = 6; var b: i32 = 7; match (a *? b) { Some(v) => { return v; }, None => { return 99; } } }`, 42},
	{"i32-mul-zero", `function main(): i32 { var a: i32 = 0; var b: i32 = 0 - 2147483647 - 1; match (a *? b) { Some(v) => { if (v == 0) { return 55; } return 98; }, None => { return 99; } } }`, 55},

	// u8: clamp low is 0, result in range by construction.
	{"u8-add-overflow", `function main(): i32 { var u: u8 = 250; var w: u8 = 10; match (u +? w) { Some(v) => { return 6; }, None => { return 21; } } }`, 21},
	{"u8-sub-underflow", `function main(): i32 { var u: u8 = 250; var w: u8 = 10; match (w -? u) { Some(v) => { return 7; }, None => { return 22; } } }`, 22},
	{"u8-sub-ok", `function main(): i32 { var u: u8 = 250; var w: u8 = 10; match (u -? w) { Some(v) => { if ((v as i32) == 240) { return 24; } return 98; }, None => { return 99; } } }`, 24},
	{"u8-mul-overflow", `function main(): i32 { var u: u8 = 250; var w: u8 = 10; match (u *? w) { Some(v) => { return 8; }, None => { return 23; } } }`, 23},

	// u32.
	{"u32-add-overflow", `function main(): i32 { var p: u32 = 4294967290; var q: u32 = 10; match (p +? q) { Some(v) => { return 9; }, None => { return 31; } } }`, 31},
	{"u32-sub-underflow", `function main(): i32 { var p: u32 = 4294967290; var q: u32 = 10; match (q -? p) { Some(v) => { return 10; }, None => { return 32; } } }`, 32},
	{"u32-mul-ok", `function main(): i32 { var q: u32 = 10; match (q *? q) { Some(v) => { return v as i32; }, None => { return 99; } } }`, 100},

	// i64 at full width — no wider host type.
	{"i64-add-overflow", `function main(): i32 { var x: i64 = 9223372036854775807; var y: i64 = 5; match (x +? y) { Some(v) => { return 40; }, None => { return 41; } } }`, 41},
	{"i64-mul-neg1-min", `function main(): i32 { var x: i64 = 0 - 1; var y: i64 = 0 - 9223372036854775807 - 1; match (x *? y) { Some(v) => { return 43; }, None => { return 44; } } }`, 44},
	{"i64-mul-ok", `function main(): i32 { var x: i64 = 6; var y: i64 = 7; match (x *? y) { Some(v) => { return v as i32; }, None => { return 99; } } }`, 42},

	// u64.
	{"u64-add-overflow", `function main(): i32 { var m: u64 = 18446744073709551615; var n: u64 = 1; match (m +? n) { Some(v) => { return 50; }, None => { return 51; } } }`, 51},
	{"u64-sub-underflow", `function main(): i32 { var m: u64 = 0; var n: u64 = 1; match (m -? n) { Some(v) => { return 52; }, None => { return 53; } } }`, 53},
	{"u64-add-ok", `function main(): i32 { var m: u64 = 21; var n: u64 = 21; match (m +? n) { Some(v) => { return v as i32; }, None => { return 99; } } }`, 42},

	// /? and %?: None on a zero divisor and (signed) on MIN / -1. The
	// division is deferred into the Some branch so a `84 /? 0` never traps.
	{"i32-div-ok", `function main(): i32 { var a: i32 = 84; var b: i32 = 2; match (a /? b) { Some(v) => { return v; }, None => { return 99; } } }`, 42},
	{"i32-div-zero", `function main(): i32 { var a: i32 = 84; var b: i32 = 0; match (a /? b) { Some(v) => { return 1; }, None => { return 41; } } }`, 41},
	{"i32-div-minneg1", `function main(): i32 { var a: i32 = 0 - 2147483647 - 1; var b: i32 = 0 - 1; match (a /? b) { Some(v) => { return 2; }, None => { return 43; } } }`, 43},
	{"i32-rem-ok", `function main(): i32 { var a: i32 = 85; var b: i32 = 43; match (a %? b) { Some(v) => { return v; }, None => { return 99; } } }`, 42},
	{"i32-rem-zero", `function main(): i32 { var a: i32 = 85; var b: i32 = 0; match (a %? b) { Some(v) => { return 3; }, None => { return 44; } } }`, 44},
	{"i32-rem-minneg1", `function main(): i32 { var a: i32 = 0 - 2147483647 - 1; var b: i32 = 0 - 1; match (a %? b) { Some(v) => { return 4; }, None => { return 45; } } }`, 45},
	{"u32-div-ok", `function main(): i32 { var a: u32 = 100; var b: u32 = 4; match (a /? b) { Some(v) => { return v as i32; }, None => { return 99; } } }`, 25},
	{"u32-div-zero", `function main(): i32 { var a: u32 = 100; var b: u32 = 0; match (a /? b) { Some(v) => { return 5; }, None => { return 46; } } }`, 46},
	{"u32-rem-ok", `function main(): i32 { var a: u32 = 100; var b: u32 = 7; match (a %? b) { Some(v) => { return v as i32; }, None => { return 99; } } }`, 2},
	{"u8-div-ok", `function main(): i32 { var a: u8 = 84; var b: u8 = 2; match (a /? b) { Some(v) => { return v as i32; }, None => { return 99; } } }`, 42},
	{"u8-div-zero", `function main(): i32 { var a: u8 = 84; var b: u8 = 0; match (a /? b) { Some(v) => { return 6; }, None => { return 47; } } }`, 47},
	{"i64-div-ok", `function main(): i32 { var a: i64 = 84; var b: i64 = 2; match (a /? b) { Some(v) => { return v as i32; }, None => { return 99; } } }`, 42},
	{"i64-div-zero", `function main(): i32 { var a: i64 = 84; var b: i64 = 0; match (a /? b) { Some(v) => { return 7; }, None => { return 48; } } }`, 48},
	{"i64-div-minneg1", `function main(): i32 { var a: i64 = 0 - 9223372036854775807 - 1; var b: i64 = 0 - 1; match (a /? b) { Some(v) => { return 8; }, None => { return 49; } } }`, 49},
	{"i64-rem-ok", `function main(): i32 { var a: i64 = 85; var b: i64 = 43; match (a %? b) { Some(v) => { return v as i32; }, None => { return 99; } } }`, 42},
	{"u64-div-zero", `function main(): i32 { var m: u64 = 100; var z: u64 = 0; match (m /? z) { Some(v) => { return 9; }, None => { return 50; } } }`, 50},
	{"u64-div-ok", `function main(): i32 { var m: u64 = 84; var z: u64 = 2; match (m /? z) { Some(v) => { return v as i32; }, None => { return 99; } } }`, 42},

	// <<? and >>?: None on an out-of-range shift count (< 0 or >= width);
	// the value is masked to the operand width (`255u8 <<? 1 == 254`).
	{"i32-shl-ok", `function main(): i32 { var a: i32 = 1; var b: i32 = 4; match (a <<? b) { Some(v) => { return v; }, None => { return 99; } } }`, 16},
	{"i32-shl-oor", `function main(): i32 { var a: i32 = 1; var b: i32 = 32; match (a <<? b) { Some(v) => { return 1; }, None => { return 41; } } }`, 41},
	{"i32-shl-neg", `function main(): i32 { var a: i32 = 1; var b: i32 = 0 - 1; match (a <<? b) { Some(v) => { return 2; }, None => { return 42; } } }`, 42},
	{"i32-shr-ok", `function main(): i32 { var a: i32 = 256; var b: i32 = 4; match (a >>? b) { Some(v) => { return v; }, None => { return 99; } } }`, 16},
	{"i32-shr-oor", `function main(): i32 { var a: i32 = 256; var b: i32 = 33; match (a >>? b) { Some(v) => { return 3; }, None => { return 43; } } }`, 43},
	{"i32-shr-arith", `function main(): i32 { var a: i32 = 0 - 16; var b: i32 = 2; match (a >>? b) { Some(v) => { if (v == (0 - 4)) { return 44; } return 88; }, None => { return 89; } } }`, 44},
	{"u32-shl-ok", `function main(): i32 { var a: u32 = 8; var b: u32 = 2; match (a <<? b) { Some(v) => { return v as i32; }, None => { return 99; } } }`, 32},
	{"u32-shl-oor", `function main(): i32 { var a: u32 = 1; var b: u32 = 32; match (a <<? b) { Some(v) => { return 4; }, None => { return 45; } } }`, 45},
	{"u32-shl-wrap", `function main(): i32 { var a: u32 = 4294967295; match (a <<? (1 as u32)) { Some(v) => { if (v == 4294967294) { return 46; } return 88; }, None => { return 89; } } }`, 46},
	{"u32-shr-logical", `function main(): i32 { var a: u32 = 4294967288; match (a >>? (2 as u32)) { Some(v) => { if (v == 1073741822) { return 47; } return 88; }, None => { return 89; } } }`, 47},
	{"u8-shl-ok", `function main(): i32 { var a: u8 = 1; var b: u8 = 3; match (a <<? b) { Some(v) => { return v as i32; }, None => { return 99; } } }`, 8},
	{"u8-shl-oor", `function main(): i32 { var a: u8 = 1; var b: u8 = 8; match (a <<? b) { Some(v) => { return 5; }, None => { return 48; } } }`, 48},
	{"u8-shl-wrap", `function main(): i32 { var a: u8 = 255; match (a <<? (1 as u8)) { Some(v) => { if ((v as i32) == 254) { return 49; } return 88; }, None => { return 89; } } }`, 49},
	{"i64-shl-ok", `function main(): i32 { var a: i64 = 1; var b: i64 = 40; match (a <<? b) { Some(v) => { if (v == 1099511627776) { return 50; } return 88; }, None => { return 89; } } }`, 50},
	{"i64-shl-oor", `function main(): i32 { var a: i64 = 1; var b: i64 = 64; match (a <<? b) { Some(v) => { return 6; }, None => { return 51; } } }`, 51},
	{"i64-shr-ok", `function main(): i32 { var a: i64 = 1024; var b: i64 = 4; match (a >>? b) { Some(v) => { return v as i32; }, None => { return 99; } } }`, 64},
	{"u64-shl-oor", `function main(): i32 { var a: u64 = 1; var b: u64 = 64; match (a <<? b) { Some(v) => { return 7; }, None => { return 52; } } }`, 52},
	{"u64-shr-ok", `function main(): i32 { var a: u64 = 1024; var b: u64 = 5; match (a >>? b) { Some(v) => { return v as i32; }, None => { return 99; } } }`, 32},
}

// TestSelfHostCheckedX86IR pins the checked operators on the x86-64 IR backend
// of the self-hosted compiler.
func TestSelfHostCheckedX86IR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern",
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
		innerAsm := filepath.Join(dir, "chk_"+name+".s")
		innerBin := filepath.Join(dir, "chk_"+name)
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

	for _, tc := range checkedIRCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.name, tc.src); got != tc.expected {
				t.Errorf("checked x86 IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}

// TestSelfHostCheckedWasmIR pins the same cases on the wasm IR backend, where
// the operand widths are native i32 / i64 and the Option rides an i32 pointer
// slot rather than a 64-bit register.
func TestSelfHostCheckedWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host checked wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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
		watFile := filepath.Join(dir, "chk_"+name+".wat")
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

	for _, tc := range checkedIRCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.name, tc.src); got != tc.expected {
				t.Errorf("checked wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
