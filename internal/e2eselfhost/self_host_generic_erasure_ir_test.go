package e2eselfhost

import (
	"os/exec"
	"testing"
)

// genericErasureIRCases pin type-driven dispatch on values returned by
// ERASED-generic functions. The self-host strips UNBOUNDED type params
// (`pair[K, V]` carries type_params=[]), so monomorphize_module never clones
// such functions — they compile once under type erasure. That's fine for the
// uniform 8-byte slot ABI, but the return-type REGISTRIES recorded the raw
// type-var spellings: a `(K, V)` tuple return degraded its elements to
// scalars ("K" != "string"), and a bare `T` return wasn't str-tracked at all.
// Both made string results silently mis-dispatch (`.len()` read 0) on the IR
// path AND the legacy AST fallback — native returns the right answer.
//
// The fix threads positional "$arg<i>" references through the registries:
// tuple_ret_fns_of rewrites a type-var ret segment to the first param
// declared with that var (resolved at the call site by
// resolve_argref_tuple_tags), and str_ret_fns_of records "name|$arg<i>" for
// a bare-type-var return (resolved by expr_is_str via str_ret_argref).
// Exit codes are cross-checked against the Go reference (native -interp).
var genericErasureIRCases = []struct {
	name string
	src  string
	exit int
}{
	// The minimal repro: a (K, V) factory instantiated at (string, i32);
	// s.0.len() read 0 before (self-host exited 3, native 23).
	{"pair-string-elem", "function pair[K, V](k: K, v: V): (K, V) { return (k, v); } function main(): i32 { var s = pair[string, i32](\"xy\", 3); return s.0.len() * 10 + s.1; }", 23},
	// IMPLICIT instantiation (no explicit type args) — same resolution.
	{"pair-string-elem-implicit", "function pair[K, V](k: K, v: V): (K, V) { return (k, v); } function main(): i32 { var s = pair(\"xy\", 3); return s.0.len() * 10 + s.1; }", 23},
	// Destructure form (`var (a, b) = pair(…)`): the element-bind reads the
	// same registry entry, resolved per element.
	{"pair-destructure", "function pair[K, V](k: K, v: V): (K, V) { return (k, v); } function main(): i32 { var (a, b) = pair(\"xy\", 3); return a.len() * 10 + b; }", 23},
	// Single type param mixed with a concrete element ((T, i32)).
	{"wrap-single-tparam", "function wrap[T](x: T): (T, i32) { return (x, 1); } function main(): i32 { var s = wrap(\"abc\"); return s.0.len() * 10 + s.1; }", 31},
	// All-scalar instantiation is unaffected (the $arg reference resolves to
	// "i32" — same reading as the raw type-var tag produced before).
	{"pair-scalar-regress", "function pair[K, V](k: K, v: V): (K, V) { return (k, v); } function main(): i32 { var s = pair(4, 2); return s.0 * 10 + s.1; }", 42},
	// A CONCRETE tuple-returning fn keeps its exact tags (no rewriting).
	{"concrete-pair-regress", "function pair(k: string, v: i32): (string, i32) { return (k, v); } function main(): i32 { var s = pair(\"xy\", 3); return s.0.len() * 10 + s.1; }", 23},
	// The bare-T return (`idg[T](x: T): T`): the call is str-tracked from its
	// argument, so `.len()` dispatches as a string read (was 0 → exit 39).
	{"id-string", "function idg[T](x: T): T { return x; } function main(): i32 { return idg(\"xyz\").len() + 39; }", 42},
	// Bare-T return bound to a local, used as a string.
	{"id-string-binding", "function idg[T](x: T): T { return x; } function main(): i32 { var s = idg(\"ab\" + \"c\"); return s.len() * 10 + 12; }", 42},
	// Bare-T return at a SCALAR instantiation stays scalar.
	{"id-scalar-regress", "function idg[T](x: T): T { return x; } function main(): i32 { return idg(42); }", 42},
	// A `T[]` return (`dup[T](x: T): T[]`) instantiated at string: the
	// "name|$arg<i>" strarr entry re-types the call from its argument, so
	// `a[0].len()` dispatches as a string read (was 0 → exit 36).
	{"dup-strarr", "function dup[T](x: T): T[] { return [x, x]; } function main(): i32 { var a = dup(\"xyz\"); return a[0].len() + a[1].len() + 36; }", 42},
	// An `Option[T]` return (`some1[T](x: T): Option[T]`) at string: the
	// rewritten "Option[$arg<i>]" entry resolves the payload from the
	// argument, so the Some-binder is str-tracked (was 0 → exit 37).
	{"opt-generic-payload", "function some1[T](x: T): Option[T] { return Some(x); } function main(): i32 { var o = some1(\"hello\"); match (o) { Some(s) => { return s.len() + 37; }, None => { return 0; } } }", 42},
	// Scalar instantiations of both shapes stay scalar.
	{"dup-scalar-regress", "function dup[T](x: T): T[] { return [x, x]; } function main(): i32 { var a = dup(20); return a[0] + a[1] + 2; }", 42},
	{"opt-scalar-regress", "function some1[T](x: T): Option[T] { return Some(x); } function main(): i32 { var o = some1(40); match (o) { Some(n) => { return n + 2; }, None => { return 0; } } }", 42},
}

// TestSelfHostGenericErasureIRX86_64 — erased-generic returns through the
// PRODUCTION x86-64 IR path (asm_ir_run `-ir`).
func TestSelfHostGenericErasureIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_arm64.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range genericErasureIRCases {
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

// TestSelfHostGenericErasureIRArm64 — CI-gated arm64 counterpart
// (asm_ir_run `-target arm64 -ir`); the registry fixes are shared irlower
// analysis, so both register backends inherit them.
func TestSelfHostGenericErasureIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "asm.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range genericErasureIRCases {
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
