package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// dynFnIndirectCoerceCases exercise the shape #5276's follow-up left open (and
// which self_host_dyn_fn_param_ir_test.go documents as the remaining gap): a
// PRIMITIVE value coerced to `dyn Trait` AT an indirect fn-value call —
// `f(7)` where `f: (dyn Speak) => i32`. The `(dyn Speak) => i32` fn-type spelling
// coarsens to the flat "fn" tag, discarding the per-parameter dyn-ness, so the
// indirect call lowered the raw i32 arg with plain lower_expr — not lower_dyn_arg
// — and op_dyn_dispatch inside the callee read the unboxed primitive as a shape
// pointer (SIGSEGV). The interpreter / native x86-64 are correct (107).
//
// The fix threads the fn-type's dyn parameter positions through a new
// ParamDecl.fn_param_dyn sidecar (parser's non-consuming peek_fn_param_dyn),
// which lower_func seeds as "FNDYN:<name>|<positions>"; the indirect-call arg
// lowering consults it (fn_arg_is_dyn) and dyn-boxes exactly those positions.
//
// Each case is oracle-checked against the interpreter and routing-pinned to
// "ir", returning a non-negative value <= 126.
var dynFnIndirectCoerceCases = []struct {
	name string
	main string
}{
	// The #5276 headline repro: `f(7)` boxes the i32 literal at the indirect
	// call. 7 + 100 = 107 (was SIGSEGV).
	{"prim-literal-at-indirect-call", `trait Speak { function say(self: Self): i32; }
impl Speak for i32 { function say(self: Self): i32 { return self + 100; } }
function apply(f: (dyn Speak) => i32): i32 { return f(7); }
function main(): i32 { return apply((s: dyn Speak) => s.say()); }`},
	// Two impls so the dispatch is meaningful (i32 vs a struct impl); the value
	// is still the boxed i32 literal → 107.
	{"two-impl-indirect", `trait Speak { function say(self: Self): i32; }
impl Speak for i32 { function say(self: Self): i32 { return self + 100; } }
struct Dog {}
impl Speak for Dog { function say(self: Self): i32 { return 5; } }
function apply(f: (dyn Speak) => i32): i32 { return f(7); }
function main(): i32 { return apply((s: dyn Speak) => s.say()); }`},
	// The dyn parameter is at index 1 (an i32 precedes it): only arg 1 is
	// dyn-boxed, arg 0 stays a plain i32. 3 + (7 + 100) = 110.
	{"dyn-param-index-1", `trait Speak { function say(self: Self): i32; }
impl Speak for i32 { function say(self: Self): i32 { return self + 100; } }
function apply(f: (i32, dyn Speak) => i32): i32 { return f(3, 7); }
function main(): i32 { return apply((n: i32, s: dyn Speak) => n + s.say()); }`},
	// Regression guard: a NON-dyn fn param must NOT get its arg spuriously
	// dyn-boxed — a plain i32 flows through unchanged. 7 + 100 = 107.
	{"nondyn-fn-param-unboxed", `function apply(f: (i32) => i32): i32 { return f(7); }
function main(): i32 { return apply((x: i32) => x + 100); }`},
	// A struct-backed dyn value passed at the indirect call already carried its
	// shape pointer (flows unboxed); pin it stays correct alongside the fix. 107.
	{"struct-dyn-at-indirect-call", `trait Speak { function say(self: Self): i32; }
struct Cat { v: i32 }
impl Speak for Cat { function say(self: Self): i32 { return self.v + 100; } }
function apply(f: (dyn Speak) => i32): i32 { var c: Cat = Cat { v: 7 }; return f(c); }
function main(): i32 { return apply((s: dyn Speak) => s.say()); }`},
}

// TestSelfHostDynFnIndirectCoerceIRX86_64 routes each case through the self-host
// x86-64 IR driver, oracle-checked, routing pinned to "ir".
func TestSelfHostDynFnIndirectCoerceIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range dynFnIndirectCoerceCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
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
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
