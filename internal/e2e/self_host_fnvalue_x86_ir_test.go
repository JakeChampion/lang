package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
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
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	prog, _, err := modload.Load(filepath.Join(dir, "asm_ir_run.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	driverAsm := filepath.Join(dir, "driver.s")
	driverBin := filepath.Join(dir, "driver")
	if err := os.WriteFile(driverAsm, []byte(asm), 0o644); err != nil {
		t.Fatalf("write driver asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", driverAsm, "-o", driverBin).CombinedOutput(); err != nil {
		t.Fatalf("driver gcc: %v\n%s", err, out)
	}

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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runIR(t, tc.src); got != tc.expected {
				t.Errorf("fn-value x86 IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
