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

// TestSelfHostIRDiff is the differential gate of the IR rebuild
// (docs/RC-PERCEUS-SELF-HOST-IR-REBUILD.md, slice 7): it compiles a shared
// i32 corpus through BOTH the production AST emit path (asm_run, asm.fern)
// and the new IR emit path (ir_x86_run, ir_x86.fern) and asserts the two
// produce identical exit codes. This is the prerequisite the rollout doc
// requires before flipping x86-64's default to emit from the IR — it proves
// the IR backend is behaviour-equivalent to the established AST backend on
// the language subset they both cover (params/locals, arithmetic, all
// comparisons, bitwise/shift, unary, if/else, while, multi-function
// recursion), not merely that it matches a hand-written expected value.
func TestSelfHostIRDiff(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	// Both drivers share the frontend (util/astwalk/lexer/parser); each adds
	// its own emit modules.
	files := []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_run.fern",
		"ir_x86.fern", "ir_x86_run.fern",
	}
	for _, name := range files {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	buildDriver := func(entry, out string) string {
		prog, _, err := modload.Load(filepath.Join(dir, entry))
		if err != nil {
			t.Fatalf("modload %s: %v", entry, err)
		}
		if err := constfold.Fold(prog); err != nil {
			t.Fatalf("constfold %s: %v", entry, err)
		}
		info, err := checker.Check(prog)
		if err != nil {
			t.Fatalf("check %s: %v", entry, err)
		}
		asm, err := x86_64.Emit(prog, info)
		if err != nil {
			t.Fatalf("emit %s: %v", entry, err)
		}
		asmPath := filepath.Join(dir, out+".s")
		binPath := filepath.Join(dir, out)
		if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
			t.Fatalf("write %s asm: %v", out, err)
		}
		if o, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
			t.Fatalf("%s gcc: %v\n%s", out, err, o)
		}
		return binPath
	}

	astDriver := buildDriver("asm_run.fern", "asm_driver")
	irDriver := buildDriver("ir_x86_run.fern", "ir_driver")

	// emitAndRun pipes src to a driver, captures its emitted asm, assembles +
	// runs it, and returns the inner exit code.
	emitAndRun := func(t *testing.T, driver, tag, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driver)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driver)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		asm, err := cmd.Output()
		if err != nil || len(asm) == 0 {
			t.Fatalf("%s driver failed for %q: %v", tag, src, err)
		}
		asmPath := filepath.Join(dir, tag+"_inner.s")
		binPath := filepath.Join(dir, tag+"_inner")
		if err := os.WriteFile(asmPath, asm, 0o644); err != nil {
			t.Fatalf("write %s inner asm: %v", tag, err)
		}
		if o, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
			t.Fatalf("%s inner gcc: %v\n%s\n--- asm ---\n%s", tag, err, o, asm)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(binPath)
		} else {
			inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), binPath)...)
		}
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("%s inner did not exit normally for %q", tag, src)
		}
		return inner.ProcessState.ExitCode()
	}

	// The shared corpus: every program is in the IR backend's i32 subset, so
	// the AST and IR paths must agree exit-code for exit-code.
	corpus := []struct {
		name string
		src  string
	}{
		{"const", "function main(): i32 { return 42; }"},
		{"arith", "function main(): i32 { return 2 + 3 * 4; }"},
		{"parens", "function main(): i32 { return (1 + 2) * 3; }"},
		{"locals", "function main(): i32 { var x = 2 + 3 * 4; var y = x - 5; return y * 2; }"},
		{"reassign", "function main(): i32 { var x = 5; x = x + 3; return x; }"},
		{"modulo", "function main(): i32 { return 23 % 5; }"},
		{"division", "function main(): i32 { return 84 / 2; }"},
		{"bitwise", "function main(): i32 { return (6 & 3) | 8; }"},
		{"shift", "function main(): i32 { return 1 << 4; }"},
		{"compare-lt", "function main(): i32 { return 5 < 10; }"},
		{"compare-ge", "function main(): i32 { return 7 >= 7; }"},
		{"if-taken", "function main(): i32 { var x = 1; if (5 < 10) { x = 7; } return x; }"},
		{"if-else", "function main(): i32 { var x = 0; if (2 < 1) { x = 3; } else { x = 9; } return x; }"},
		{"early-return", "function main(): i32 { var x = 5; if (x > 3) { return 100; } return x; }"},
		{"nested-if", "function main(): i32 { var x = 5; if (x > 0) { if (x > 3) { x = 100; } else { x = 50; } } return x; }"},
		{"while-sum", "function main(): i32 { var i = 1; var s = 0; while (i <= 5) { s = s + i; i = i + 1; } return s; }"},
		{"while-factorial", "function main(): i32 { var i = 1; var f = 1; while (i <= 5) { f = f * i; i = i + 1; } return f; }"},
		{"if-in-loop", "function main(): i32 { var i = 0; var c = 0; while (i < 10) { if (i > 4) { c = c + 1; } i = i + 1; } return c; }"},
		{"nested-loop", "function main(): i32 { var i = 0; var t = 0; while (i < 3) { var j = 0; while (j < 3) { t = t + 1; j = j + 1; } i = i + 1; } return t; }"},
		{"call-args", "function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(4, 5); }"},
		{"compute", "function compute(a: i32): i32 { var b = a * 2; var c = b + 1; return c; } function main(): i32 { return compute(5); }"},
		{"factorial", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }"},
		{"fib", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(8); }"},
		{"mutual", "function is_even(n: i32): i32 { if (n == 0) { return 1; } return is_odd(n - 1); } function is_odd(n: i32): i32 { if (n == 0) { return 0; } return is_even(n - 1); } function main(): i32 { return is_even(6); }"},
		{"loop-call", "function sq(x: i32): i32 { return x * x; } function main(): i32 { var i = 1; var s = 0; while (i <= 4) { s = s + sq(i); i = i + 1; } return s; }"},
		// i32 array literals + indexing (slice 8): heap allocation via the
		// bump allocator, on both backends.
		{"arr-index", "function main(): i32 { var a = [10, 20, 30]; return a[0] + a[2]; }"},
		{"arr-computed-index", "function main(): i32 { var a = [3, 7, 11, 15]; var i = 2; return a[i]; }"},
		{"arr-loop-sum", "function main(): i32 { var a = [5, 10, 15, 20, 25]; var i = 0; var s = 0; while (i < 5) { s = s + a[i]; i = i + 1; } return s; }"},
		{"arr-expr-elements", "function main(): i32 { var x = 4; var a = [x, x * 2, x + 100]; return a[1] + a[2]; }"},
		{"arr-two", "function main(): i32 { var a = [1, 2]; var b = [100, 200]; return a[1] + b[0]; }"},
		// .len() + index-assignment (slice 9).
		{"arr-len", "function main(): i32 { var a = [10, 20, 30]; return a.len(); }"},
		{"arr-index-plus-len", "function main(): i32 { var a = [10, 20, 30]; return a[2] + a.len(); }"},
		{"arr-len-loop", "function main(): i32 { var a = [4, 8, 12, 16]; var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }"},
		{"set-index", "function main(): i32 { var a = [10, 20, 30]; a[1] = 99; return a[0] + a[1] + a[2]; }"},
		{"set-index-fill", "function main(): i32 { var a = [0, 0, 0, 0, 0]; var i = 0; while (i < 5) { a[i] = i * i; i = i + 1; } return a[0] + a[1] + a[2] + a[3] + a[4]; }"},
		{"set-index-swap", "function main(): i32 { var a = [7, 3]; var t = a[0]; a[0] = a[1]; a[1] = t; return a[0] * 10 + a[1]; }"},
		// Move-on-return (slice 13): array-returning functions. The AST and IR
		// backends must agree (both implement the move).
		{"mov-basic", "function make(): i32[] { var a = [10, 20, 30]; return a; } function main(): i32 { var x = make(); return x[0] + x[2]; }"},
		{"mov-uaf-guard", "function make(): i32[] { var a = [10, 20, 30]; return a; } function main(): i32 { var x = make(); var y = [1, 1, 1]; return x[0] + x[2]; }"},
		{"mov-len", "function make(): i32[] { var a = [5, 6, 7, 8]; return a; } function main(): i32 { var x = make(); return x.len(); }"},
		{"mov-then-mutate", "function make(): i32[] { var a = [1, 2, 3]; return a; } function main(): i32 { var x = make(); x[1] = 99; return x[0] + x[1] + x[2]; }"},
		// Array params, borrowed (slice 14). The AST and IR backends must agree.
		{"param-sum", "function sum(a: i32[]): i32 { var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; } function main(): i32 { var arr = [10, 20, 30]; return sum(arr); }"},
		{"param-borrow-then-use", "function get0(a: i32[]): i32 { return a[0]; } function main(): i32 { var arr = [5, 6, 7]; var x = get0(arr); var y = arr[1]; return x + y; }"},
		{"param-two-arrays", "function pick(a: i32[], b: i32[]): i32 { return a[0] + b[1]; } function main(): i32 { var p = [1, 2]; var q = [10, 20]; return pick(p, q); }"},
		{"param-borrow-noreuse", "function len_of(a: i32[]): i32 { return a.len(); } function main(): i32 { var arr = [3, 4, 5]; var n = len_of(arr); var z = [9, 9, 9]; return arr[0] + arr[2] + n; }"},
	}

	for _, tc := range corpus {
		t.Run(tc.name, func(t *testing.T) {
			ast := emitAndRun(t, astDriver, "ast", tc.src)
			ir := emitAndRun(t, irDriver, "ir", tc.src)
			if ast != ir {
				t.Errorf("AST vs IR mismatch for %q: AST=%d IR=%d\n--- source ---\n%s", tc.name, ast, ir, tc.src)
			}
		})
	}
}
