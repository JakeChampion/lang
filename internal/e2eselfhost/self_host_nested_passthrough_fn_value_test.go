package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A generic passthrough hands its erased-generic argument straight back, so a
// fn value at such a parameter is a fn value at the CALL too — which is why the
// lift boxes it and the read side classifies the call as a closure box. Both
// sides recurse through a chain of them (`pick(t, id(<lambda>), …)`) except the
// one predicate that decides whether to descend, which answered only for a bare
// lambda or fn name. The inner lambda stayed raw, its sibling was boxed, and it
// reached lower_expr asking for a `<fd>$clo` nothing had built — the module
// bailed with "function value <fn>$clo not defined" (#6256).
//
// Driven through the fern.fern CLI rather than asm_ir_run: the driver-level
// tests compile these shapes either way, and only the module-loading CLI path
// reproduces the bail. Reduced from fernsmith seeds 215 and 393, which are the
// corpus rows this closes.
var nestedPassthroughFnValueCases = []struct {
	name string
	src  string
}{
	// The BOXING half: a passthrough nested inside another. The predicate that
	// decides whether to descend answered only for a bare lambda or fn name, so
	// the inner lambda stayed raw while its sibling was boxed, and it reached
	// lower_expr asking for a `<fd>$clo` nothing had built.
	{"array_elem_nested_passthrough_lambda", `function id[T](x: T): T { return x; }
function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }
function gen(c: boolean, p1: i32): ((i32) => i32)[] { var b: boolean = c; return (if (b) { [((x: i32) => x)] } else { [pick(!b, id(((y: i32) => (y + p1))), ((z: i32) => 9i32))] }); }
function main(): i32 { var fs: ((i32) => i32)[] = gen(false, 5i32); return fs[0i32](3i32) & 63i32; }`}, // 8
	// An IIFE arm that yields a passthrough CALL holding a raw lambda. The hoist
	// claims a capturing IIFE only when an arm yields a lambda, and this arm
	// yields a call — so nothing walked the arms and the lambda one level in
	// bailed the module. A passthrough hands its argument back, so a lambda
	// there is an arm lambda.
	{"iife_arm_passthrough_holds_lambda", `function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }
function main(): i32 { var v0: (i32) => i32 = ((a: i32) => 40i32); var n: i32 = 2i32; var t: boolean = false; var xs: ((i32) => i32)[] = [(if (t) { v0 } else { pick(t, v0, ((x: i32) => (x + n))) })]; return xs[0i32](3i32) & 63i32; }`}, // 5

	// The DESTINATION half, and the reason the boxing above could not land on
	// its own. The lift boxes a fn value at an erased-generic parameter
	// unconditionally, so `[pick(c, <lambda>, <lambda>)]` is an array of boxes —
	// but the registry that decides whether a function returns a closure ARRAY
	// asked whether element 0 was a lambda, a `__mkclo$` box or a known factory
	// call, and a passthrough call is none of those. The caller then bound the
	// result as a plain fn-pointer array and `fs[0](3)` bare-dispatched a box:
	// compiled clean, no bail, SIGSEGV. Widening the boxing side first would
	// have turned safe bails into more of these.
	//
	// returned_iife_of_arrays is the same miss one level in: a value-position
	// if/match in return position is an IIFE, so the arrays sit inside its arms
	// and the registry's call arm only knew the named-callee form.
	{"returned_array_passthrough_element", `function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }
function gen(c: boolean, p1: i32): ((i32) => i32)[] { var s: boolean = !c; return [pick(s, ((y: i32) => (y + p1)), ((z: i32) => 9i32))]; }
function main(): i32 { var fs: ((i32) => i32)[] = gen(false, 5i32); return fs[0i32](3i32) & 63i32; }`}, // 8
	{"returned_iife_of_arrays_passthrough", `function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }
function gen(c: boolean, p1: i32): ((i32) => i32)[] { var t: boolean = c; var s: boolean = !c; return (if (t) { [((x: i32) => x)] } else { [pick(s, ((y: i32) => (y + p1)), ((z: i32) => 9i32)), ((w: i32) => 7i32)] }); }
function main(): i32 { var fs: ((i32) => i32)[] = gen(false, 5i32); return (fs[0i32](3i32) + fs[1i32](0i32)) & 63i32; }`}, // 15

	// The capture side. Hoisting the IIFE means carrying its captures as
	// ordinary parameters, and cap_param_for will only build one from an EXACT
	// signature — a lambda initialiser or a `__mkclo$` target. `var v0: (i32) =>
	// i32 = pick(…)` is neither, so it declined and the module bailed. The
	// signature was not unrecoverable, it was dropped: the parser reads the
	// annotation's return into `v_fn_ret` and then stored only the coarse "fn"
	// tag on the StmtVar. Carrying that return pairs it with the annotated
	// params of the lambda the passthrough forwards, which is exact. Reduced
	// from fernsmith seed 393.
	{"iife_capture_bound_from_passthrough", `function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }
function gen(p0: i32): i32 { var v0: (i32) => i32 = pick(true, ((a: i32) => 45i32), ((b: i32) => 46i32)); var v1: ((i32) => i32)[] = [(if (false) { v0 } else { pick(false, v0, ((x: i32) => (x + p0))) })]; return v1[0i32](1i32); }
function main(): i32 { return gen(3i32) & 63i32; }`}, // 4

	// The ARRAY path's capture scope. A value-position if/match parses as an
	// IIFE, and its arms can bind names of their own — a match arm's payload.
	// The non-array twin already reads captures against `iife_scope_fd` so those
	// count, and so does `iife_arm_lambda_captures`; the array gate did not, so
	// `Ok(a) => [(x) => x + a]` looked capture-free, the gate answered 0, and the
	// arm lambda stayed raw for a `<fd>$clo` nobody built. Reduced from fernsmith
	// seed 211, where the payload-capturing arm sits beside a nested if/match.
	{"arm_array_payload_capture_scope", `function mk(r: Result[i32, i32]): ((i32) => i32)[] { return (match (r) { Ok(a) => [((x: i32) => (x + a))], Err(b) => (match (r) { Ok(c) => [((y: i32) => y)], Err(d) => [((z: i32) => (z + d))] }) }); }
function main(): i32 { var fs: ((i32) => i32)[] = mk(Err(4i32)); return fs[0i32](3i32) & 63i32; }`}, // 7
	// An arm spelled `id([…])` rather than `[…]`. A generic passthrough hands the
	// array literal straight back, so the arm carries the same elements — but the
	// gate demanded a literal and abandoned the whole rewrite, leaving a sibling
	// arm's capturing lambda raw. The destination moved first here too:
	// `expr_is_closure_array` now sees through the same passthrough, so the
	// binding reads as a closure array rather than bare-dispatching a box.
	// Reduced from fernsmith seed 42.
	{"arm_array_passthrough_forwards_literal", `enum Status { Active, Inactive, Pending }
function id[T](x: T): T { return x; }
function main(): i32 { var v1: Status = Pending; var v3: ((i32) => i32)[] = (match (v1) { Active => [((a: i32) => (match (v1) { Active => a, Inactive => a, Pending => 673i32 }))], Inactive => [((b: i32) => b)], Pending => id([((c: i32) => 126i32)]) }); return v3[0i32](3i32) & 63i32; }`}, // 62

	// The control: one passthrough deep, built and consumed in the same
	// function, which both sides already agreed on.
	{"array_elem_single_passthrough_control", `function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }
function main(): i32 { var n: i32 = 4i32; var t: boolean = true; var xs: ((i32) => i32)[] = [pick(t, ((x: i32) => (x + n)), ((z: i32) => 9i32))]; return xs[0i32](1i32) & 63i32; }`}, // 5
}

// TestSelfHostNestedPassthroughFnValueX86_64 asserts values against the interp
// oracle on the self-host x86-64 backend.
func TestSelfHostNestedPassthroughFnValueX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range nestedPassthroughFnValueCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			asmPath := filepath.Join(proj, "out.s")
			cmd := exec.Command(fernBin, "-target", "x86-64-linux", "-emit", "asm", mainPath, stdlibRoot, "-o", asmPath)
			cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
			if out, cerr := cmd.CombinedOutput(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, out)
			}
			binPath := filepath.Join(proj, "out.bin")
			if out, lerr := exec.Command(gcc, "-nostdlib", "-static", "-no-pie", "-o", binPath, asmPath).CombinedOutput(); lerr != nil {
				t.Fatalf("link: %v (%s)", lerr, out)
			}
			var rcmd *exec.Cmd
			if len(runner) == 0 {
				rcmd = exec.Command(binPath)
			} else {
				rcmd = exec.Command(runner[0], append(runner[1:], binPath)...)
			}
			_ = rcmd.Run()
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}

// TestSelfHostNestedPassthroughFnValueWasm is the wasm leg. The boxing decision
// lives in irlower.fern, which every backend shares, so a case that regresses
// only here is a dispatch bug rather than a lift one.
func TestSelfHostNestedPassthroughFnValueWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping nested-passthrough fn-value wasm cases")
	}
	gcc, _ := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range nestedPassthroughFnValueCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			outWat := filepath.Join(proj, "out.wat")
			var stderr strings.Builder
			cmd := exec.Command(fernBin, "-target", "wasm32-wasi", "-emit", "asm", mainPath, stdlibRoot, "-o", outWat)
			cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
			cmd.Stderr = &stderr
			if cerr := cmd.Run(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, stderr.String())
			}
			rcmd := exec.Command("wasmtime", "run", outWat)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}
