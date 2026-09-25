package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// fnptrArrayFieldCases pin struct fields typed `(() => i32)[]` (coarsened to
// "fn[]") that hold NAMED functions or NON-capturing lambdas — issue #5235. They
// were once a separate fn-POINTER representation from closure arrays under the
// same spelling, and the self-host mis-handled that split at construction, on
// the read side and in the struct-drop glue; every read shape segfaulted while
// the native backend and the interpreter were right. Since #10076 a function
// array holds env boxes whatever built it: each element is a `$wrap` or
// `$clo` box, every read dispatches env-first, and struct-drop walks the field
// as a box array. These cases stay as the pin that the named-function and
// non-capturing shapes answer through that one representation.
//
// Exit codes cross-checked against the interpreter and the native Go backend.
var fnptrArrayFieldCases = []struct {
	name string
	src  string
	exit int
}{
	// A LOCAL-BUILT array stored into the field — the literal form with one
	// binding removed. It crashed while the representation depended on how the
	// field was constructed (#5790).
	{"local-built", "struct R { hs: (() => i32)[] }\nfunction seven(): i32 { return 7; }\nfunction main(): i32 { var a: (() => i32)[] = [seven]; var r: R = R { hs: a }; return r.hs[0](); }", 7},
	// The local is REBOUND from named functions to a capturing lambda before the
	// store; both are boxes, so the field's representation does not change.
	{"rebind-retracts-proof", "struct R { hs: (() => i32)[] }\nfunction seven(): i32 { return 7; }\nfunction main(): i32 { var n: i32 = 5; var a: (() => i32)[] = [seven]; a = [() => n]; var r: R = R { hs: a }; return r.hs[0](); }", 5},
	// var f = r.hs[0]; f() — named functions.
	{"bind-named", "function inc(): i32 { return 40; } function dbl(): i32 { return 2; } struct Reg { hs: (() => i32)[] } function main(): i32 { var r = Reg { hs: [inc, dbl] }; var f = r.hs[0]; return f(); }", 40},
	// Second element, to prove per-element identity.
	{"bind-named-second", "function inc(): i32 { return 40; } function dbl(): i32 { return 2; } struct Reg { hs: (() => i32)[] } function main(): i32 { var r = Reg { hs: [inc, dbl] }; var f = r.hs[1]; return f(); }", 2},
	// var f = r.hs[0]; f() — a NON-capturing lambda.
	{"bind-lambda", "struct Reg { hs: (() => i32)[] } function main(): i32 { var r = Reg { hs: [() => 7] }; var f = r.hs[0]; return f(); }", 7},
	// var xs = r.hs; xs[0]() — whole-array alias, then indexed call.
	{"alias", "function inc(): i32 { return 40; } function dbl(): i32 { return 2; } struct Reg { hs: (() => i32)[] } function main(): i32 { var r = Reg { hs: [inc, dbl] }; var xs = r.hs; return xs[0](); }", 40},
	// return r.hs[0]() — direct inline call, no binding.
	{"direct", "function inc(): i32 { return 40; } function dbl(): i32 { return 2; } struct Reg { hs: (() => i32)[] } function main(): i32 { var r = Reg { hs: [inc, dbl] }; return r.hs[0](); }", 40},
	// Named functions that take an argument, bound then called.
	{"bind-arg", "function twice(x: i32): i32 { return x * 2; } function inc1(x: i32): i32 { return x + 1; } struct Reg { hs: ((i32) => i32)[] } function main(): i32 { var r = Reg { hs: [twice, inc1] }; var f = r.hs[0]; return f(21); }", 42},
	// Same, direct inline call with an argument.
	{"direct-arg", "function twice(x: i32): i32 { return x * 2; } struct Reg { hs: ((i32) => i32)[] } function main(): i32 { var r = Reg { hs: [twice] }; return r.hs[0](21); }", 42},
	// RC soundness / drop: build a Reg per iteration and let it go out of scope N
	// times, exercising __struct_drop_Reg on a function-array field. Probe for
	// over-release (__rc_underflow_count) and unbounded heap growth (__heap_bump_bytes):
	// struct-drop walks the field as a box array and frees the buffer once (a
	// `$wrap` box of a named function is a static block, so its dec is a no-op),
	// and the whole-array alias's read-inc is balanced by its exit sweep.
	{"rc-soundness", "function f0(): i32 { return 1; } function f1(): i32 { return 2; } struct Reg { hs: (() => i32)[] } function one(): i32 { var r = Reg { hs: [f0, f1] }; var f = r.hs[0]; var acc: i32 = f(); var xs = r.hs; acc = acc + xs[1](); acc = acc + r.hs[0](); return acc; } function churn(n: i32): i32 { var i: i32 = 0; var s: i32 = 0; while (i < n) { s = one(); i = i + 1; } return s; } function main(): i32 { var w: i32 = churn(3000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(3000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 4096) { return 98; } if (w != x) { return 97; } return 0; }", 0},
}

// TestSelfHostFnptrArrayFieldIRX86_64 — the x86-64 leg, through the production
// driver (asm_ir_run `-ir`).
func TestSelfHostFnptrArrayFieldIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range fnptrArrayFieldCases {
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

// TestSelfHostFnptrArrayFieldIRArm64 — CI-gated arm64 counterpart, built with the
// x86 driver and run under qemu, as TestSelfHostCloArrayFieldBindIRArm64 is.
func TestSelfHostFnptrArrayFieldIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 fnptr-array-field gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range fnptrArrayFieldCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
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

// TestSelfHostFnptrArrayFieldIRWasm runs the same cases through the wasm IR
// backend, whose struct-drop walks a function-array field's element boxes too.
// All case exit codes are <= 120 (the wasm exit-code clamp).
func TestSelfHostFnptrArrayFieldIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host fnptr-array-field wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range fnptrArrayFieldCases {
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
			watFile := filepath.Join(dir, "fnptr_array_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("fnptr-array wasm IR %q = %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
