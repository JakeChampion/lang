package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// cloArrayFieldBindCases pin issue #5160 defect #2: BINDING a closure-array
// element loaded from a struct field, then calling the binding — `var f =
// reg.hs[i]; f()` and `for h in reg.hs { h() }`. The element is a closure box
// owned by the struct's field array; before the fix the bind lost the "this is a
// closure box, not a bare fn pointer" tag, so `f()` / `h()` called the box
// pointer as a raw code address and SIGSEGV'd.
//
// The fix (irlower.fern) makes the whole struct-field-closure-array shape
// IR-eligible: closure-array struct fields are recorded per construction as
// "CLOF:" markers on the closure_fns registry (clof_of_stmt, folded into
// closure_ret_fns_of), and closurearr_field_type reads them so
//   - `var xs = r.hs`      binds a closure-array local (dup'd, is_closurearr),
//   - `var f = r.hs[i]`    binds a closure-local BORROW (env-first `f()`, NOT
//                          is_arr — the struct owns and reclaims the box, so the
//                          exit-sweep must not dec it), and
//   - `for h in r.hs`      binds the loop var a closure-local borrow (foreach
//                          field-kind 4).
// Only CAPTURING closure fields are tagged (clof_elem_is_boxed_closure): a
// non-capturing lambda / named-fn field is a fn-POINTER array, a separate shape.
//
// Exit codes cross-checked against the interpreter and the native Go backend.
var cloArrayFieldBindCases = []struct {
	name string
	src  string
	exit int
}{
	// bind one element, call it.
	{"bind", "struct Reg { hs: (() => i32)[] } function main(): i32 { var n: i32 = 40; var r = Reg { hs: [() => n] }; var f = r.hs[0]; return f(); }", 40},
	// bind two elements, call both.
	{"bind-multi", "struct Reg { hs: (() => i32)[] } function main(): i32 { var n: i32 = 2; var r = Reg { hs: [() => n, () => n + 1] }; var a = r.hs[0]; var b = r.hs[1]; return a() + b(); }", 5},
	// bind an element that takes an argument.
	{"bind-arg", "struct Reg { hs: ((i32) => i32)[] } function main(): i32 { var n: i32 = 5; var r = Reg { hs: [(x: i32) => x + n] }; var f = r.hs[0]; return f(10); }", 15},
	// foreach over the field directly, accumulate.
	{"foreach", "struct Reg { hs: (() => i32)[] } function main(): i32 { var n: i32 = 40; var r = Reg { hs: [() => n] }; var acc: i32 = 0; for h in r.hs { acc = acc + h(); } return acc; }", 40},
	// foreach over a multi-element field.
	{"foreach-multi", "struct Reg { hs: (() => i32)[] } function main(): i32 { var n: i32 = 10; var r = Reg { hs: [() => n, () => n + 5, () => n + 20] }; var acc: i32 = 0; for h in r.hs { acc = acc + h(); } return acc; }", 55},
	// bind the WHOLE field to a local closure array, then index it.
	{"whole-field-index", "struct Reg { hs: (() => i32)[] } function main(): i32 { var n: i32 = 7; var r = Reg { hs: [() => n, () => n + 35] }; var xs = r.hs; return xs[0]() + xs[1](); }", 49},
	// bind the whole field, then foreach the local.
	{"whole-field-foreach", "struct Reg { hs: (() => i32)[] } function main(): i32 { var n: i32 = 7; var r = Reg { hs: [() => n, () => n + 28] }; var xs = r.hs; var acc: i32 = 0; for h in xs { acc = acc + h(); } return acc; }", 42},
	// RC soundness: run the mixed bind/foreach/whole-field shape N times and
	// probe for over-release (__rc_underflow) and unbounded heap growth
	// (__heap_bump_bytes) — the borrowed element boxes must be reclaimed by the
	// struct exactly once, never double-freed by the binding's exit-sweep.
	{"rc-soundness", "struct Reg { hs: (() => i32)[] } function one(k: i32): i32 { var r = Reg { hs: [() => k, () => k + 1] }; var f = r.hs[0]; var acc: i32 = f(); for h in r.hs { acc = acc + h(); } var xs = r.hs; acc = acc + xs[1](); return acc; } function churn(n: i32): i32 { var i: i32 = 0; var s: i32 = 0; while (i < n) { s = one(i); i = i + 1; } return s; } function main(): i32 { var w: i32 = churn(3000); var b1: i32 = __heap_bump_bytes(); var x: i32 = churn(3000); var b2: i32 = __heap_bump_bytes(); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 4096) { return 98; } if (w != x) { return 97; } return 0; }", 0},
}

// TestSelfHostCloArrayFieldBindIRX86_64 runs the bind/foreach cases through the
// x86-64 IR driver (asm_ir_run `-ir`) — the shape now lowers on the IR path.
func TestSelfHostCloArrayFieldBindIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_arm64.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range cloArrayFieldBindCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostCloArrayFieldBindIRArm64 — CI-gated arm64 counterpart via the arm64
// IR path (asm_ir_run `-target arm64 -ir`). The frontend (irlower.fern) is shared,
// so this pins that the shared lowering + arm64 instruction selection agree.
func TestSelfHostCloArrayFieldBindIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 clo-array-field-bind gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "asm.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range cloArrayFieldBindCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostCloArrayFieldBindIRWasm runs the same cases through the wasm IR
// backend (wasm_ir_run `-ir`). Excludes the rc-soundness probe, whose
// __heap_bump_bytes / __rc_underflow builtins are register-backend-only.
func TestSelfHostCloArrayFieldBindIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host clo-array-field-bind wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range cloArrayFieldBindCases {
		if tc.name == "rc-soundness" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "clo_field_bind_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("clo-array-field-bind wasm IR %q = %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
