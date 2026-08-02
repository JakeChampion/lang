package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostFnValueX86IR is the x86-64 correctness gate for plain function
// VALUES on the register IR backend (the wasm sibling is TestSelfHostFnValueIR).
// const_func loads the function's code address (no funcref table — the address
// IS the value), and call_indirect reverses the on-stack args and dispatches
// through it (call *%r11). all_eligible now admits such modules on the register
// backends too. Pinned to hardcoded oracle exit codes via the asm_ir_run `-ir`
// path.
func TestSelfHostFnValueX86IR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	runIR := func(t *testing.T, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		emitted, err := cmd.Output()
		if err != nil || len(emitted) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		innerAsm := filepath.Join(dir, "ir_inner.s")
		innerBin := filepath.Join(dir, "ir_inner")
		if err := os.WriteFile(innerAsm, emitted, 0o644); err != nil {
			t.Fatalf("write inner asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
			t.Fatalf("inner gcc: %v\n%s\n--- asm ---\n%s", err, out, emitted)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(innerBin)
		} else {
			inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), innerBin)...)
		}
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("inner did not exit normally for %q", src)
		}
		return inner.ProcessState.ExitCode()
	}

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		{"value-call", `function work(): i32 { return 42; } function run(fn: () => i32): i32 { return fn(); } function main(): i32 { return run(work); }`, 42},
		{"value-arg", `function inc(x: i32): i32 { return x + 1; } function apply(f: (i32) => i32, v: i32): i32 { return f(v); } function main(): i32 { return apply(inc, 41); }`, 42},
		{"predicate", `function count_if(arr: i32[], pred: (i32) => boolean): i32 { var c: i32 = 0; for x in arr { if (pred(x)) { c = c + 1; } } return c; } function is_big(n: i32): boolean { return n > 10; } function main(): i32 { var a: i32[] = [5, 20, 8, 30, 15]; return count_if(a, is_big); }`, 3},
		{"two-arg", `function addmul(x: i32, y: i32): i32 { return x * 10 + y; } function run2(g: (i32, i32) => i32, p: i32, q: i32): i32 { return g(p, q); } function main(): i32 { return run2(addmul, 4, 2); }`, 42},
		// #3574: bind a bare ZERO-ARG fn name to a `fn`-typed local, then call it.
		// `f` is a value (the fn-typed target disambiguates), not a const-call of
		// f — previously this stored f()'s result and `g()` segfaulted.
		{"bind-zero-arg", `function f(): i32 { return 7; } function main(): i32 { var g: () => i32 = f; return g(); }`, 7},
		{"bind-call-twice", `function f(): i32 { return 7; } function main(): i32 { var g: () => i32 = f; return g() + g(); }`, 14},
		{"bind-one-arg", `function inc(x: i32): i32 { return x + 1; } function main(): i32 { var g: (i32) => i32 = inc; return g(41); }`, 42},
		// #3574 (array half): a `(() => i32)[]` literal of bare named-fn VALUES.
		// Each element is a fn pointer (const_func), not a const-call of f, so the
		// indexed `fns[i]()` dispatches the pointer — previously segfaulted.
		{"arr-bind-call", `function f(): i32 { return 7; } function main(): i32 { var fns: (() => i32)[] = [f]; return fns[0](); }`, 7},
		{"arr-two-sum", `function f(): i32 { return 7; } function g(): i32 { return 5; } function main(): i32 { var fns: (() => i32)[] = [f, g]; return fns[0]() + fns[1](); }`, 12},
		// loop over a bare-named-fn array, calling each through a variable index.
		{"arr-loop", `function a(): i32 { return 1; } function b(): i32 { return 2; } function c(): i32 { return 4; } function main(): i32 { var fns: (() => i32)[] = [a, b, c]; var s: i32 = 0; var i: i32 = 0; while (i < 3) { s = s + fns[i](); i = i + 1; } return s; }`, 7},
		// a 1-arg named-fn array stays correct (already const_func via the generic
		// path; the fn[] interception emits the same const_func).
		{"arr-one-arg", `function inc(x: i32): i32 { return x + 1; } function dbl(x: i32): i32 { return x * 2; } function main(): i32 { var fns: ((i32) => i32)[] = [inc, dbl]; return fns[0](10) + fns[1](10); }`, 31},
		// #3640 slice A: a fn-value PARAM whose RETURN type is a STRUCT. The
		// return-struct name is preserved through parse-time coarsening
		// (ParamDecl.fn_ret) and registered as a `g|P` struct_ret_fns entry in
		// lower_func, so the call-result field read `g().x` resolves P's field
		// index and lowers on the IR path instead of bailing to AST. Previously
		// `() => P` discarded P at coarsening, so `g().x` couldn't resolve the
		// field and the module routed `ast`.
		{"param-ret-struct-field", `struct P { x: i32 } function call(g: () => P): i32 { return g().x; } function mk(): P { return P { x: 4 }; } function main(): i32 { return call(mk); }`, 4},
		{"param-ret-struct-var-2fields", `struct P { x: i32, y: i32 } function call(g: () => P): i32 { var p = g(); return p.x + p.y; } function mk(): P { return P { x: 4, y: 5 }; } function main(): i32 { return call(mk); }`, 9},
		{"param-ret-struct-method", `struct P { x: i32 } function (p: P) dbl(): i32 { return p.x * 2; } function call(g: () => P): i32 { return g().dbl(); } function mk(): P { return P { x: 11 }; } function main(): i32 { return call(mk); }`, 22},
		{"param-ret-struct-witharg", `struct P { x: i32 } function call(g: (i32) => P): i32 { return g(7).x; } function mk(n: i32): P { return P { x: n + 1 }; } function main(): i32 { return call(mk); }`, 8},
		// #3640 slice B.1: a struct-returning fn-value LOCAL `var f: () => P = mk`
		// (the local sibling of the slice-A param case). lower_func registers a
		// `f|P` struct_ret_fns entry — recovering P from the target `mk`'s own
		// struct return — so `f().x` / `var p = f()` / `f().method()` resolve the
		// struct result and lower on the IR path. Previously the field read on the
		// fn-value-local call result couldn't resolve P and bailed to AST (then
		// crashed). The unannotated form `var f = mk` (no `: () => P`) is the
		// separate use-directed-inference slice and stays as-is.
		{"local-ret-struct-field", `struct P { x: i32 } function mk(): P { return P { x: 4 }; } function main(): i32 { var f: () => P = mk; return f().x; }`, 4},
		{"local-ret-struct-var-2fields", `struct P { x: i32, y: i32 } function mk(): P { return P { x: 4, y: 5 }; } function main(): i32 { var f: () => P = mk; var p = f(); return p.x + p.y; }`, 9},
		{"local-ret-struct-method", `struct P { x: i32 } function (p: P) dbl(): i32 { return p.x * 2; } function mk(): P { return P { x: 11 }; } function main(): i32 { var f: () => P = mk; return f().dbl(); }`, 22},
		// #3640 slice B.2: the UNANNOTATED `var f = mk` where mk is a zero-arg
		// struct-returning fn and `f` is later CALLED. infer_fnvalue_locals_module
		// (on the shared IR funnel) binds it as a fn-value rather than const-calling
		// mk — matching the native compiler's use-directed inference — so it rides
		// the slice-B.1 lowering. Previously this stored mk()'s struct and `f()`
		// called the struct box as a code pointer (crash / wrong answer). A bare
		// `var p = mk` that is NOT called stays a const-call (unchanged).
		{"local-infer-field", `struct P { x: i32 } function mk(): P { return P { x: 7 }; } function main(): i32 { var f = mk; return f().x; }`, 7},
		{"local-infer-var-2fields", `struct P { x: i32, y: i32 } function mk(): P { return P { x: 4, y: 5 }; } function main(): i32 { var f = mk; var p = f(); return p.x + p.y; }`, 9},
		{"local-infer-method", `struct P { x: i32 } function (p: P) dbl(): i32 { return p.x * 2; } function mk(): P { return P { x: 11 }; } function main(): i32 { var f = mk; return f().dbl(); }`, 22},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("fn-value x86 IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
