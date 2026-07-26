package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostZeroArgFnValueIRX86_64 covers a local bound to a bare ZERO-arg
// function name (`var f = mk`) and used only as a call target. The self-host's
// "bare 0-arg receiver-less fn name = a call to it" rule (#2954) otherwise
// mis-lowered `var f = mk` to `var f = mk()` — binding the RESULT — so a later
// `f()` called a non-function and segfaulted (an IR-path miscompile for an i32
// return; an AST one for a struct). The parser's inline_callonly_fn_values pass
// recognises the call-only case and inlines it (drops the binding, rewrites
// `f(args)` to `mk(args)` — a 0-arg fn-value called is exactly its direct call),
// matching the native compiler; a `const` used as a VALUE (`var f = K; f + 1`)
// is left as a const-call. Each case asserts the oracle exit code; a size bound
// proves the small IR path (a segfaulting AST bail would differ).
func TestSelfHostZeroArgFnValueIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		// the canonical miscompile: 0-arg fn-value, i32 return, called.
		{"i32_return", `function mk(): i32 { return 9; }
function main(): i32 { var f = mk; return f(); }`, 9},
		// 0-arg fn-value returning a struct, field access off the call.
		{"struct_return", `struct P { x: i32 }
function mk(): P { return P { x: 9 }; }
function main(): i32 { var f = mk; return f().x; }`, 9},
		// called more than once — each call inlines to a direct call.
		{"called_twice", `function five(): i32 { return 5; }
function main(): i32 { var f = five; return f() + f(); }`, 10},
		// called inside a loop.
		{"loop_call", `function one(): i32 { return 1; }
function main(): i32 { var f = one; var s = 0; var i = 0; while (i < 4) { s = s + f(); i = i + 1; } return s; }`, 4},
		// a >0-arg fn-value still works (already did; the pass also inlines it).
		{"arg_fn", `function dbl(n: i32): i32 { return n * 2; }
function main(): i32 { var f = dbl; return f(7); }`, 14},
		// SOUNDNESS: a `const` bound to a var and used as a VALUE must NOT be
		// inlined — it stays a const-call (f = 10), so `f + 5` = 15.
		{"const_as_value", `const TEN: i32 = 10;
function main(): i32 { var f = TEN; return f + 5; }`, 15},
		// 0-arg fn in an unannotated ARRAY, called via index — the array analog.
		// `var fns = [mk]` would const-call each element (storing mk()'s result),
		// so `fns[0]()` calls an integer as a code pointer and segfaults; the pass
		// types it `fn[]` (a fn-pointer array) when used only as indexed calls.
		{"array_index_call", `function mk(): i32 { return 7; }
function main(): i32 { var fns = [mk]; return fns[0](); }`, 7},
		// two 0-arg fns in an array, both called by index.
		{"array_two_fns", `function a(): i32 { return 3; }
function b(): i32 { return 4; }
function main(): i32 { var fns = [a, b]; return fns[0]() + fns[1](); }`, 7},
		// an indexed call inside a loop.
		{"array_loop_call", `function one(): i32 { return 1; }
function main(): i32 { var fns = [one]; var s = 0; var i = 0; while (i < 5) { s = s + fns[0](); i = i + 1; } return s; }`, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 || len(asm) > 18000 {
				t.Fatalf("asm is %d bytes — expected small IR output; the fn-value module likely bailed/miscompiled", len(asm))
			}
			progBin := buildBin(t, gcc, dir, "zfv_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("zero-arg fn-value %q exit %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
