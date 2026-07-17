package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// closureEscapeCases exercise a closure that ESCAPES its defining function
// through a local variable binding — `var f = function (…) { … }; return f;` —
// and is then called by the caller (`var g = factory(); g(args)`). This is
// distinct from the already-covered shapes:
//
//   - `return function (…) { … }` returned DIRECTLY (higher_order), and
//   - a closure passed DOWN as an argument (`apply(g, …)`, closure-arg).
//
// A var-bound lambda is always materialised as a heap closure BOX
// (`[_, fn_ptr, caps…]`), so a caller must dispatch `g(args)` env-first (load
// fn_ptr from the box, pass the box as the hidden env). Before the fix,
// closure_ret_fns_of only recognised `return <lambda-literal>` as
// closure-returning, so a factory ending in `return f` left the caller's
// `var g = factory()` bound as a plain scalar; `g(args)` then called the raw
// box POINTER as code → SIGSEGV. Now `return <ident bound to a closure>` (gated
// on an `fn` return type, with a fixpoint for transitive factory chains) is
// recognised too, so the caller dispatches env-first.
//
// escape-counter additionally pins the mutable-scalar-capture semantics across
// an escape (SH-057): the counter's `x = x + 1` writes persist in the box's
// capture slot between calls, so two calls yield 1 then 2 (sum 3), matching the
// Go reference. Exit codes are cross-checked vs the Go backend.
var closureEscapeCases = []struct {
	name string
	src  string
	exit int
}{
	{"escape-noncapture", "function mk(): (i32) => i32 { var f = function (b: i32): i32 { return b + 1; }; return f; } function main(): i32 { var g = mk(); return g(5); }", 6},
	{"escape-capture", "function adder(a: i32): (i32) => i32 { var f = function (b: i32): i32 { return a + b; }; return f; } function main(): i32 { var add10 = adder(10); return add10(6); }", 16},
	{"escape-counter", "function make_counter(): () => i32 { var x = 0; var inc = function (): i32 { x = x + 1; return x; }; return inc; } function main(): i32 { var c = make_counter(); var a = c(); var b = c(); return a + b; }", 3},
	{"escape-zero-arg", "function mk(): () => i32 { var f = function (): i32 { return 11; }; return f; } function main(): i32 { var g = mk(); return g(); }", 11},
	// Transitive: a factory that FORWARDS another factory's closure box
	// (`return makeAdder();`). Requires closure_ret_fns_of to recognise
	// `return <closure-returning call>` (fixpoint-ordered).
	{"transitive-factory", "function makeAdder(): (i32) => i32 { var g = function (x: i32): i32 { return x + 1; }; return g; } function outer(): (i32) => i32 { return makeAdder(); } function main(): i32 { var f = outer(); return f(41); }", 42},
	// Nested: a factory whose body defines and calls a LOCAL closure that
	// itself returns a closure, then forwards the result (`return inner();`).
	{"nested-factory", "function outer(): (i32) => i32 { var inner = function (): (i32) => i32 { var g = function (x: i32): i32 { return x + 1; }; return g; }; return inner(); } function main(): i32 { var f = outer(); return f(41); }", 42},
	// Captured-fn-value escape: a closure that CAPTURES a fn-value and
	// ESCAPES must dispatch the captured closure env-first when it calls it
	// inside its body (the synthesized `var base: fn = __env[i]` capture read
	// is marked a closure local). Before the fix, `base(x)` bare-called the
	// captured box pointer → SIGSEGV.
	{"escape-captures-fn", "function mk(base: (i32) => i32): (i32) => i32 { var f = function (x: i32): i32 { return base(x); }; return f; } function dbl(x: i32): i32 { return x * 2; } function main(): i32 { var g = mk(dbl); return g(21); }", 42},
	// Composition: the escaped closure captures a fn-value it applies twice.
	{"escape-compose-twice", "function twice(f: (i32) => i32): (i32) => i32 { var g = function (x: i32): i32 { return f(f(x)); }; return g; } function inc(x: i32): i32 { return x + 1; } function main(): i32 { var d = twice(inc); return d(40); }", 42},
	// Return an ARRAY of closures, then call an element via the caller's
	// binding: `var arr = mk(); arr[0](x)`. The caller must mark arr's slot
	// is_closurearr (mk returns `((i32) => i32)[]`) so `arr[0](x)` dispatches
	// env-first; before the fix it bare-called the box pointer → SIGSEGV.
	{"return-closure-array-via-var", "function mk(): ((i32) => i32)[] { var n = 5; var a = [function (x: i32): i32 { return x + n; }]; return a; } function main(): i32 { var arr = mk(); return arr[0](37); }", 42},
	// Same, with the array returned directly (`return [<closure>]`).
	{"return-closure-array-direct", "function mk(): ((i32) => i32)[] { return [function (x: i32): i32 { return x + 1; }]; } function main(): i32 { var arr = mk(); return arr[0](41); }", 42},
	// A closure array passed as a PARAMETER: fn_param_sigs_of's call-site
	// analysis (flag '3') proves every caller passes a closure array, so the
	// param slot is marked is_closurearr and `fns[0](x)` inside the callee
	// dispatches env-first; before the fix it bare-called the element box →
	// SIGSEGV.
	{"closure-array-param", "function mk(): ((i32) => i32)[] { var n = 5; var a = [function (x: i32): i32 { return x + n; }]; return a; } function consume(fns: ((i32) => i32)[]): i32 { return fns[0](37); } function main(): i32 { var arr = mk(); return consume(arr); }", 42},
	// The counterpart guard: a BARE fn-pointer array (`[inc, dbl]`) passed to
	// the same-shaped param must KEEP the plain call_indirect dispatch — the
	// '3' proof must not fire (also pinned by TestSelfHostAsmIRPath's
	// fnarr-elem-call-loop, which this case mirrors).
	{"bare-fnarr-param", "function apply(fns: ((i32) => i32)[], n: i32): i32 { var s = 0; var i = 0; while (i < fns.len()) { s = s + fns[i](n); i = i + 1; } return s; } function inc(n: i32): i32 { return n + 1; } function dbl(n: i32): i32 { return n * 2; } function main(): i32 { return apply([inc, dbl], 10); }", 31},
	// Indexing a closure-array factory's result DIRECTLY (`mk()[0](x)`, no
	// binding): the ExprIndex-callee dispatch must recognise a call to a
	// closurearr-ret fn as the array source; before the fix the shape fell to
	// the legacy AST path, which miscompiled it (exit 0).
	{"closure-array-direct-index-call", "function mk(): ((i32) => i32)[] { var n = 5; var a = [function (x: i32): i32 { return x + n; }]; return a; } function main(): i32 { return mk()[0](37); }", 42},
	// A factory whose closure returns live in IF/ELSE branches (not the last
	// statement): closure_ret_fns_of must collect nested returns; before the
	// fix the caller's binding stayed a plain scalar → SIGSEGV.
	{"ifelse-branch-factory", "function mk(flag: boolean): (i32) => i32 { if (flag) { var f = function (x: i32): i32 { return x + 1; }; return f; } else { var g = function (x: i32): i32 { return x + 2; }; return g; } } function main(): i32 { var a = mk(true); var b = mk(false); return a(20) + b(19); }", 42},
	// A factory FORWARDING another factory's closure array (`return mk();`):
	// closurearr_ret_fns_of needs the transitive call form (fixpoint-ordered,
	// like closure_ret_fns_of); before the fix → SIGSEGV.
	{"transitive-closure-array-factory", "function mk(): ((i32) => i32)[] { var n = 5; var a = [function (x: i32): i32 { return x + n; }]; return a; } function outer(): ((i32) => i32)[] { return mk(); } function main(): i32 { var arr = outer(); return arr[0](37); }", 42},
	// Regression: a directly-returned lambda (bare fn pointer, no box) must
	// keep working under the extended detection.
	{"direct-return", "function adder(a: i32): (i32) => i32 { return function (b: i32): i32 { return a + b; }; } function main(): i32 { var add10 = adder(10); return add10(5); }", 15},
}

// TestSelfHostClosureEscapeIRX86_64 — escaping var-bound closures through the
// PRODUCTION x86-64 IR path (asm_ir_run `-ir`).
func TestSelfHostClosureEscapeIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_arm64.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range closureEscapeCases {
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

// TestSelfHostClosureEscapeIRArm64 — CI-gated arm64 counterpart via the arm64
// IR path (asm_ir_run `-target arm64 -ir`). Shares the fix in irlower.fern.
func TestSelfHostClosureEscapeIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "asm.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range closureEscapeCases {
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
