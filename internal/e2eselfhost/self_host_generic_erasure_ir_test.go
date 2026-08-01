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
	// A `T[]` return at an f64 element: the shared "$arg<i>" strarr entry is
	// now consulted by expr_is_f64arr too, so `a[0]` reads 8-byte f64 (was
	// read as 4-byte i32 → exit 7).
	{"dup-f64arr", "function dup[T](x: T): T[] { return [x, x]; } function main(): i32 { var a = dup(2.5); if (a[0] + a[1] > 4.0) { return 42; } return 7; }", 42},
	// A `T[]` return at an i64 element (expr_is_i64arr consults the entry).
	{"dup-i64arr", "function dup[T](x: T): T[] { return [x, x]; } function main(): i32 { var a = dup(5000000000 as i64); if (a[0] + a[1] == 10000000000 as i64) { return 42; } return 7; }", 42},
	// `var s = f()?` where f returns `Option[T]` at string: expr_is_str's
	// try-unary arm str-tracks the ?-bound s (was untyped → s.len() read 0,
	// exit 37).
	{"opt-try-string", "function some1[T](x: T): Option[T] { return Some(x); } function unwrap_len(): Option[i32] { var s = some1(\"hello\")?; return Some(s.len()); } function main(): i32 { match (unwrap_len()) { Some(n) => { return n + 37; }, None => { return 0; } } }", 42},
	// The `Result[T, E]` sibling: the Ok payload is rewritten to $arg<i>
	// leaving E intact, so `f()?` on a string Ok str-tracks.
	{"result-try-string", "function okg[T](x: T): Result[T, i32] { return Ok(x); } function helper(): Result[i32, i32] { var s = okg(\"hello\")?; return Ok(s.len()); } function main(): i32 { match (helper()) { Ok(n) => { return n + 37; }, Err(e) => { return 0; } } }", 42},
	// The T[]-element and try-string shapes at SCALAR instantiations are
	// unchanged (regression guards).
	{"dup-i32arr-regress", "function dup[T](x: T): T[] { return [x, x]; } function main(): i32 { var a = dup(20); return a[0] + a[1] + 2; }", 42},
	{"opt-try-scalar-regress", "function some1[T](x: T): Option[T] { return Some(x); } function unwrap(): Option[i32] { var n = some1(40)?; return Some(n + 2); } function main(): i32 { match (unwrap()) { Some(v) => { return v; }, None => { return 0; } } }", 42},
	// A TUPLE payload inside an erased-generic Option (`Option[(T, i32)]`): the
	// type-var element is rewritten to $arg<i> anywhere in the spelling (not
	// just a bare payload), so the match-bound `t.0` str-tracks. Before, the
	// raw "(T, i32)" tag mis-typed t.0 and `.len()` read 0 (exit 39).
	{"opt-tuple-payload", "function mk[T](x: T): Option[(T, i32)] { return Some((x, 1)); } function main(): i32 { match (mk(\"abc\")) { Some(t) => { return t.0.len() + t.1 + 38; }, None => { return 0; } } }", 42},
	// The Result[(T, i32), E] sibling via `?`.
	{"result-tuple-payload", "function mk[T](x: T): Result[(T, i32), i32] { return Ok((x, 1)); } function helper(): Result[i32, i32] { var t = mk(\"abc\")?; return Ok(t.0.len() + t.1); } function main(): i32 { match (helper()) { Ok(n) => { return n + 38; }, Err(e) => { return 0; } } }", 42},
	// A TWO-type-var tuple payload (`Option[(K, V)]`) at (string, string): both
	// element vars resolve from their respective arguments.
	{"opt-tuple-two-var", "function mk[K, V](k: K, v: V): Option[(K, V)] { return Some((k, v)); } function main(): i32 { match (mk(\"ab\", \"cde\")) { Some(t) => { return t.0.len() * 10 + t.1.len(); }, None => { return 0; } } }", 23},
}

// TestSelfHostGenericErasureIRX86_64 — erased-generic returns through the
// PRODUCTION x86-64 IR path (asm_ir_run `-ir`).
func TestSelfHostGenericErasureIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
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
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
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
