package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// f32 shares the 8-byte f64 slot on the self-host IR path (there is no distinct
// 4-byte f32 slot yet — #4366), so f32 values are stored and computed as f64.
// A bare no-op `as f32` therefore FAILED to round to single precision: a value
// that is not f32-representable (e.g. 16777217.0 = 2^24+1, or a large int)
// stayed at full f64 precision, diverging from native — which gives f32 a true
// 4-byte slot and rounds at the cast (cvtsd2ss / fcvt s,d). irlower now emits
// the f32_bits/f32_from_bits round-trip (demote to f32, promote back) on an
// `as f32` cast, applying single-precision rounding while keeping the value in
// the f64 slot. These tests pin that behaviour against the native
// oracle. They are NOT differential vs the legacy AST backend (which does not
// round f32 either — a legacy-AST gap the IR path closes, per the IR-widening
// policy), so each program's result is pinned to a hardcoded oracle value.
//
// Oracle exit codes are the native interpreter's / native x86 compiled answer
// (both verified separately): each program returns 1 exactly when the cast
// rounded 2^24+1 down to 2^24, and 0 when it did not.
var f32CastCases = []struct {
	name     string
	src      string
	expected int
}{
	// float literal cast: 16777217.0 as f32 rounds to 16777216.0 -> 1
	{"lit-round", `function main(): i32 { var x: f32 = 16777217.0 as f32; if ((x as f64) == 16777216.0) { return 1; } return 0; }`, 1},
	// int cast: 16777217 as f32 rounds to 16777216.0 -> 1
	{"int-round", `function main(): i32 { var x: f32 = 16777217 as f32; if ((x as f64) == 16777216.0) { return 1; } return 0; }`, 1},
	// cast in a non-binding (argument) position also rounds -> 1
	{"nonbind-round", `function chk(v: f64): i32 { if (v == 16777216.0) { return 1; } return 0; } function main(): i32 { return chk((16777217.0 as f32) as f64); }`, 1},
	// a small f32-representable value round-trips exactly: 2.5 -> 2
	{"exact-small", `function main(): i32 { var a: f32 = 2.5 as f32; return a as i32; }`, 2},
	// a small exact int cast to f32 is unchanged: 5 -> 5
	{"exact-int", `function main(): i32 { var a: f32 = 5 as f32; return a as i32; }`, 5},
	// `as f64` / `as float` stay identity (no spurious rounding): 3.5 -> 3
	{"f64-identity", `function main(): i32 { var x: f64 = 3.5 as f64; return x as i32; }`, 3},
}

// TestSelfHostF32CastWasmIR pins f32 cast rounding on the wasm IR backend
// (wasm_ir.fern): `as f32` emits f32.demote_f64 + f64.promote_f32.
func TestSelfHostF32CastWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host f32-cast wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	runIR := func(t *testing.T, src string) int {
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
		watFile := filepath.Join(dir, "f32_prog.wat")
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

	for _, tc := range f32CastCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("f32-cast wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}

// TestSelfHostF32CastX86IR pins f32 cast rounding on the x86-64 IR backend
// (asm_ir.fern): `as f32` emits cvtsd2ss + cvtss2sd.
func TestSelfHostF32CastX86IR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	runIR := func(t *testing.T, src string) int {
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
		innerAsm := filepath.Join(dir, "f32_inner.s")
		innerBin := filepath.Join(dir, "f32_inner")
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

	for _, tc := range f32CastCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("f32-cast x86 IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
