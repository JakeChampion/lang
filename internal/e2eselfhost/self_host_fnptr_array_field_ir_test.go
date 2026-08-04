package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// fnptrArrayFieldCases pin the PLAIN-fn-pointer struct-field-array shapes —
// issue #5235. A struct field typed `(() => i32)[]` (coarsened to "fn[]") that
// holds NAMED functions or NON-capturing lambdas is a fn-POINTER array, not a
// closure array — yet both spellings are "fn[]", so the self-host IR compiler
// mis-handled it three ways: (A) construction const-CALLED each 0-arg fn ident
// (storing f()'s result), (B) the read side bound the element / whole array as a
// closure and dispatched env-first (calling the raw code pointer as a box), and
// (C) the struct-drop glue walked the fn-pointer array as a box array (dec'ing
// raw code addresses → SIGSEGV / freelist corruption). All four read shapes
// segfaulted before the fix; the native Go backend + interpreter were correct.
//
// The fix (all in examples/self_host/): irlower emits op_const_func per element +
// op_arr_make at construction (fn_value_array_lit), a whole-program registry
// (fnptr_arr_fields_of, piggybacked on LowerState.closure_fns as "FNPTR:<T>.<f>"
// markers) distinguishes fn-pointer arrays from closure arrays so the read side
// (field_access_is_fnarr) binds a RAW fn pointer + plain call_indirect, and each
// backend's struct-drop generator buffer-only-frees a registered fn-pointer field
// (no element walk). A NON-capturing lambda element (`[() => 7]`) is hoisted by
// the lift to a `__lam_N` fn-name, so it reaches the same path as `[inc, dbl]`.
//
// Exit codes cross-checked against the interpreter and the native Go backend.
var fnptrArrayFieldCases = []struct {
	name string
	src  string
	exit int
}{
	// A LOCAL-BUILT pointer array stored into the field — the same all-fn-value
	// construction as the literal form, one binding removed. This CRASHED on
	// both the AST and IR paths while interp and native x86-64 both returned 7:
	// fnptr_scan credited only an inline array literal, so the field landed in
	// `bad`, field_access_is_fnarr declined it, and field_access_is_closurearr
	// claimed it by negation — dispatching env-first on a raw code pointer.
	{"local-built", "struct R { hs: (() => i32)[] }\nfunction seven(): i32 { return 7; }\nfunction main(): i32 { var a: (() => i32)[] = [seven]; var r: R = R { hs: a }; return r.hs[0](); }", 7},
	// The soundness guard for that widening: the local is REBOUND to a closure
	// array before the store, so the all-fn-value proof must be retracted. If it
	// is not, the field is credited as raw pointers and the call invokes an env
	// box as code. Exercises the retract-on-assignment path directly.
	{"rebind-retracts-proof", "struct R { hs: (() => i32)[] }\nfunction seven(): i32 { return 7; }\nfunction main(): i32 { var n: i32 = 5; var a: (() => i32)[] = [seven]; a = [() => n]; var r: R = R { hs: a }; return r.hs[0](); }", 5},
	// var f = r.hs[0]; f() — named functions.
	{"bind-named", "function inc(): i32 { return 40; } function dbl(): i32 { return 2; } struct Reg { hs: (() => i32)[] } function main(): i32 { var r = Reg { hs: [inc, dbl] }; var f = r.hs[0]; return f(); }", 40},
	// Second element, to prove per-element fn-pointer identity.
	{"bind-named-second", "function inc(): i32 { return 40; } function dbl(): i32 { return 2; } struct Reg { hs: (() => i32)[] } function main(): i32 { var r = Reg { hs: [inc, dbl] }; var f = r.hs[1]; return f(); }", 2},
	// var f = r.hs[0]; f() — NON-capturing lambda (lift hoists it to __lam_N).
	{"bind-lambda", "struct Reg { hs: (() => i32)[] } function main(): i32 { var r = Reg { hs: [() => 7] }; var f = r.hs[0]; return f(); }", 7},
	// var xs = r.hs; xs[0]() — whole-array alias, then indexed call.
	{"alias", "function inc(): i32 { return 40; } function dbl(): i32 { return 2; } struct Reg { hs: (() => i32)[] } function main(): i32 { var r = Reg { hs: [inc, dbl] }; var xs = r.hs; return xs[0](); }", 40},
	// return r.hs[0]() — direct inline call, no binding.
	{"direct", "function inc(): i32 { return 40; } function dbl(): i32 { return 2; } struct Reg { hs: (() => i32)[] } function main(): i32 { var r = Reg { hs: [inc, dbl] }; return r.hs[0](); }", 40},
	// Named fn-pointer array whose elements take an argument, bound then called.
	{"bind-arg", "function twice(x: i32): i32 { return x * 2; } function inc1(x: i32): i32 { return x + 1; } struct Reg { hs: ((i32) => i32)[] } function main(): i32 { var r = Reg { hs: [twice, inc1] }; var f = r.hs[0]; return f(21); }", 42},
	// Same, direct inline call with an argument.
	{"direct-arg", "function twice(x: i32): i32 { return x * 2; } struct Reg { hs: ((i32) => i32)[] } function main(): i32 { var r = Reg { hs: [twice] }; return r.hs[0](21); }", 42},
	// RC soundness / drop (Fix C): build a Reg per iteration and let it go out of
	// scope N times, exercising __struct_drop_Reg on a fn-pointer field. Probe for
	// over-release (__rc_underflow) and unbounded heap growth (__heap_bump_bytes):
	// the field buffer is a real rc box the struct owns at rc=1, so struct-drop
	// buffer-only-frees it exactly once (elements are raw code addresses, never
	// touched), and the whole-array alias's read-inc is balanced by its exit sweep.
	{"rc-soundness", "function f0(): i32 { return 1; } function f1(): i32 { return 2; } struct Reg { hs: (() => i32)[] } function one(): i32 { var r = Reg { hs: [f0, f1] }; var f = r.hs[0]; var acc: i32 = f(); var xs = r.hs; acc = acc + xs[1](); acc = acc + r.hs[0](); return acc; } function churn(n: i32): i32 { var i: i32 = 0; var s: i32 = 0; while (i < n) { s = one(); i = i + 1; } return s; } function main(): i32 { var w: i32 = churn(3000); var b1: i32 = (__heap_bump_bytes() as i32); var x: i32 = churn(3000); var b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 4096) { return 98; } if (w != x) { return 97; } return 0; }", 0},
}

// TestSelfHostFnptrArrayFieldIRX86_64 — the x86-64 irlower fix, through the
// production driver (asm_ir_run `-ir`).
func TestSelfHostFnptrArrayFieldIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
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

// TestSelfHostFnptrArrayFieldIRArm64 — CI-gated arm64 counterpart. The fix is in
// the shared irlower.fern plus asm_arm64.fern's struct-drop generator, so the
// arm64 IR backend picks it up; this pins that. Mirrors
// TestSelfHostCloArrayFieldBindIRArm64 (built with the x86 driver, run under qemu).
func TestSelfHostFnptrArrayFieldIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 fnptr-array-field gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range fnptrArrayFieldCases {
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

// TestSelfHostFnptrArrayFieldIRWasm runs the same cases through the wasm IR
// backend — the runtime pin for the wasm struct-drop change (Fix C on the
// wasm side: a registered fn-pointer array field is buffer-only-freed, its raw
// code-address elements never walked as boxes). All case exit codes are <= 120
// (the wasm exit-code clamp).
func TestSelfHostFnptrArrayFieldIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host fnptr-array-field wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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
