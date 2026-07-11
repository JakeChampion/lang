package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// chainedFnArgCallCases pin #4767: a call to a function that takes a fn/closure
// argument, with its array result consumed INLINE by a chained method or index
// (`take_while(c, lt5).len()`), miscompiled on the self-host IR path to a
// SIGSEGV. The lift pass (lift_inline_closures_expr) wraps fn-typed call
// arguments into uniform env boxes, but its ExprCall arm never recursed into
// the CALLEE — so a fn-arg-taking call sitting in method-receiver position
// (under the callee's ExprFieldAccess) kept its argument a raw const_func
// while the callee's fn param dispatched it env-first, loading "box slot 0"
// from a bare code pointer and jumping to garbage. Binding the result to a
// var first dodged it (a var-init IS walked), which is why the self-host
// bootstrap never tripped it. Generic and non-generic callees both crashed;
// the generic in the original report was a red herring.
//
// Cases run through the single-program driver (no imports), covering the
// crash shape, the non-generic sibling, an inline-lambda argument, a nested
// fn-arg call in receiver position, and the (already-working) index chain
// as a regression pin.
var chainedFnArgCallCases = []struct {
	name string
	src  string
	want int
}{
	// The #4767 repro: generic + named-fn arg + inline `.len()` chain.
	{"chained-len-generic", `function take_while[T](xs: T[], p: (T) => boolean): T[] {
    var out: T[] = [];
    var i: i32 = 0;
    while (i < xs.len() && p(xs[i])) { out = out.append(xs[i]); i = i + 1; }
    return out;
}
function lt5(x: i32): boolean { return x < 5; }
function main(): i32 {
    var c: i32[] = [1, 2, 3];
    return take_while(c, lt5).len();
}`, 3},
	// Non-generic sibling — proves the generic was not part of the trigger.
	{"chained-len-nongeneric", `function take_while_i(xs: i32[], p: (i32) => boolean): i32[] {
    var out: i32[] = [];
    var i: i32 = 0;
    while (i < xs.len() && p(xs[i])) { out = out.append(xs[i]); i = i + 1; }
    return out;
}
function lt5(x: i32): boolean { return x < 5; }
function main(): i32 {
    var c: i32[] = [1, 2, 3];
    return take_while_i(c, lt5).len();
}`, 3},
	// Inline typed-lambda argument in the chained call.
	{"chained-len-lambda-arg", `function take_while[T](xs: T[], p: (T) => boolean): T[] {
    var out: T[] = [];
    var i: i32 = 0;
    while (i < xs.len() && p(xs[i])) { out = out.append(xs[i]); i = i + 1; }
    return out;
}
function main(): i32 {
    var c: i32[] = [1, 2, 3];
    return take_while(c, (x: i32) => x < 2).len();
}`, 1},
	// A fn-arg call NESTED in the receiver of another fn-arg call — the
	// recursion must reach the inner call through the outer callee.
	{"chained-len-nested", `function take_while[T](xs: T[], p: (T) => boolean): T[] {
    var out: T[] = [];
    var i: i32 = 0;
    while (i < xs.len() && p(xs[i])) { out = out.append(xs[i]); i = i + 1; }
    return out;
}
function lt5(x: i32): boolean { return x < 5; }
function main(): i32 {
    var c: i32[] = [1, 2, 3];
    return take_while(take_while(c, lt5), (x: i32) => x < 3).len();
}`, 2},
	// Index chain on the fn-arg call result (worked pre-fix via ExprIndex's
	// existing recursion) — pinned so the two consumption shapes stay in sync.
	{"chained-index", `function take_while[T](xs: T[], p: (T) => boolean): T[] {
    var out: T[] = [];
    var i: i32 = 0;
    while (i < xs.len() && p(xs[i])) { out = out.append(xs[i]); i = i + 1; }
    return out;
}
function lt5(x: i32): boolean { return x < 5; }
function main(): i32 {
    var c: i32[] = [1, 2, 3];
    return take_while(c, lt5)[1];
}`, 2},
}

// TestSelfHostChainedFnArgCallIRX86_64 drives the #4767 chained fn-arg-call
// shapes through the self-hosted x86-64 compiler (asm_run), oracle-checking
// the exit codes against the native-verified values.
func TestSelfHostChainedFnArgCallIRX86_64(t *testing.T) {
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

	for _, tc := range chainedFnArgCallCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s self-host x86-64 = %d, want %d (-1 = signal crash, the #4767 shape)", tc.name, code, tc.want)
			}
		})
	}
}
