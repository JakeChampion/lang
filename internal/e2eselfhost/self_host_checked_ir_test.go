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
}

// TestSelfHostCheckedX86IR pins the checked operators on the x86-64 IR backend
// of the self-hosted compiler.
func TestSelfHostCheckedX86IR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_arm64.fern", "asm_arm64_ir.fern", "asm_ir_run.fern",
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
