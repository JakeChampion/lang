package e2eselfhost

import (
	"os/exec"
	"testing"
)

// tupleFnIRCases pin tuples with FUNCTION-typed elements on the self-host IR
// path. Before this slice the shapes below didn't even reach the IR tuple
// machinery: parser.fern's parse_type_name coarsened ANY parenthesized type
// containing `=>` to "fn", so `((i32) => i32, i32)` wasn't a tuple type to the
// self-host compiler at all — factories returning one bailed to the legacy AST
// path, which miscompiles the element call (exit 255).
//
// The slice has three layers:
//   - parser: a depth-1 comma inside the parens means TUPLE; fn-typed
//     segments coarsen individually ("((i32)=>i32, i32)" → "(fn, i32)",
//     coarsen_fn_elems) instead of swallowing the whole type;
//   - lift: every fn-VALUED tuple element (capturing lambda, no-capture
//     lambda, unshadowed bare fn name) wraps into a `__mkclo$…` env box, so
//     the element representation is uniformly a closure box;
//   - irlower: the "clo" element tag (literal-side elem_type_tag +
//     declared-side tuple_elem_tags/tuple_type_elem_tag map "fn" → "clo")
//     drives env-first `t.N(args)` dispatch, closure-local binding for
//     `var f = t.0`, and the destructure bind.
//
// Exit codes are cross-checked against the Go reference (native -interp).
var tupleFnIRCases = []struct {
	name string
	src  string
	exit int
}{
	// A capturing lambda in a tuple RETURNED from a factory, element called
	// through the caller's binding — the original probe that exited 255.
	{"returned-capturing", "function mk(): ((i32) => i32, i32) { var n = 5; var t = (function (x: i32): i32 { return x + n; }, 1); return t; } function main(): i32 { var t = mk(); return t.0(37); }", 42},
	// Non-capturing lambda element (wrapped to a $wrap trampoline box).
	{"returned-nocapture", "function mk(): ((i32) => i32, i32) { var t = (function (x: i32): i32 { return x + 1; }, 1); return t; } function main(): i32 { var t = mk(); return t.0(41); }", 42},
	// Local tuple, never crosses a function boundary.
	{"local-tuple", "function main(): i32 { var n = 5; var t = (function (x: i32): i32 { return x + n; }, 1); return t.0(37); }", 42},
	// A bare NAMED function element, local binding.
	{"named-fn-local", "function dbl(x: i32): i32 { return x * 2; } function main(): i32 { var t = (dbl, 1); return t.0(21); }", 42},
	// A bare NAMED function element in a RETURNED tuple: the lift wraps the
	// unshadowed module-fn ident into a trampoline box, so the declared-type
	// "clo" tag and the runtime representation agree.
	{"named-fn-returned", "function dbl(x: i32): i32 { return x * 2; } function mk(): ((i32) => i32, i32) { return (dbl, 1); } function main(): i32 { var t = mk(); return t.0(21); }", 42},
	// TWO closures in one tuple.
	{"two-closures", "function mk(): ((i32) => i32, (i32) => i32) { var n = 1; var m = 2; var t = (function (x: i32): i32 { return x + n; }, function (x: i32): i32 { return x + m; }); return t; } function main(): i32 { var t = mk(); return t.0(19) + t.1(20); }", 42},
	// Destructure the returned tuple and call the bound element (`var (f, k)
	// = mk(); f(…)`): the "clo" tag binds f a closure local.
	{"destructure-call", "function mk(): ((i32) => i32, i32) { var n = 5; var t = (function (x: i32): i32 { return x + n; }, 5); return t; } function main(): i32 { var (f, k) = mk(); return f(32) + k; }", 42},
	// Regression: a plain scalar/string tuple keeps its precise spelling and
	// behaviour under the new fn-segment coarsening.
	{"scalar-tuple-regress", "function mk(): (string, i32) { return (\"hello\", 37); } function main(): i32 { var t = mk(); return t.0.len() + t.1; }", 42},
}

// TestSelfHostTupleFnIRX86_64 — fn-typed tuple elements through the PRODUCTION
// x86-64 IR path (asm_ir_run `-ir`).
func TestSelfHostTupleFnIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_arm64.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleFnIRCases {
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

// TestSelfHostTupleFnIRArm64 — CI-gated arm64 counterpart via the arm64 IR
// path (asm_ir_run `-target arm64 -ir`). Shares the fixes in parser.fern +
// irlower.fern; tuple slots are uniform 8-byte on both register backends.
func TestSelfHostTupleFnIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "asm.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleFnIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
