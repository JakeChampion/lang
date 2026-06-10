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

// TestSelfHostAsmIRPath is the differential gate for integrating the stack IR
// into the PRODUCTION x86-64 backend. The asm_ir_run driver's `-ir` flag, when
// the module is fully i32-eligible, emits via the IR path (asm_ir.emit_module_ir:
// AST -> stack IR -> asm, ABI-identical to asm.fern's output); otherwise it uses
// the unchanged AST backend (asm.emit_module). This compiles each program BOTH
// ways and asserts identical exit codes — proving the IR path is behaviour-
// equivalent to the production AST path, the rollout prerequisite
// (docs/RC-PERCEUS-SELF-HOST-IR-REBUILD.md §3) before the IR path can become the
// default. asm.fern and the asm_run driver are UNCHANGED, so the byte-identical
// self-bootstrap and the ~50 asm_run harnesses are unaffected.
//
// Slice 16 eligibility is pure i32 functions (no params, no user calls, no
// arrays), so single-function i32 programs exercise the IR path; multi-
// function / array programs fall back to AST under `-ir` and must still match.
func TestSelfHostAsmIRPath(t *testing.T) {
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
	// Build the asm_run driver once via the production x86-64 backend.
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

	// emitAndRun pipes src to the driver (optionally with `-ir`), assembles
	// the emitted asm, runs it, returns the inner exit code.
	emitAndRun := func(t *testing.T, src string, ir bool) int {
		t.Helper()
		args := []string{}
		if ir {
			args = append(args, "-ir")
		}
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, args...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), args...)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		emitted, err := cmd.Output()
		if err != nil || len(emitted) == 0 {
			t.Fatalf("driver failed (ir=%v) for %q: %v", ir, src, err)
		}
		tag := "ast"
		if ir {
			tag = "ir"
		}
		innerAsm := filepath.Join(dir, tag+"_inner.s")
		innerBin := filepath.Join(dir, tag+"_inner")
		if err := os.WriteFile(innerAsm, emitted, 0o644); err != nil {
			t.Fatalf("write inner asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
			t.Fatalf("inner gcc (ir=%v): %v\n%s\n--- asm ---\n%s", ir, err, out, emitted)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(innerBin)
		} else {
			inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), innerBin)...)
		}
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("inner did not exit normally (ir=%v) for %q", ir, src)
		}
		return inner.ProcessState.ExitCode()
	}

	cases := []struct {
		name string
		src  string
	}{
		// Pure i32 single functions -> exercised by the IR path under -ir.
		{"const", "function main(): i32 { return 42; }"},
		{"arith", "function main(): i32 { return 2 + 3 * 4; }"},
		{"parens", "function main(): i32 { return (1 + 2) * 3; }"},
		{"locals", "function main(): i32 { var x = 2 + 3 * 4; var y = x - 5; return y * 2; }"},
		{"reassign", "function main(): i32 { var x = 5; x = x + 3; return x; }"},
		{"modulo", "function main(): i32 { return 23 % 5; }"},
		{"division", "function main(): i32 { return 84 / 2; }"},
		{"bitwise", "function main(): i32 { return (6 & 3) | 8; }"},
		{"shift", "function main(): i32 { return 1 << 4; }"},
		{"compare", "function main(): i32 { return 5 < 10; }"},
		{"unary-not", "function main(): i32 { return !(5 > 10); }"},
		{"if-taken", "function main(): i32 { var x = 1; if (5 < 10) { x = 7; } return x; }"},
		{"if-else", "function main(): i32 { var x = 0; if (2 < 1) { x = 3; } else { x = 9; } return x; }"},
		{"early-return", "function main(): i32 { var x = 5; if (x > 3) { return 100; } return x; }"},
		{"nested-if", "function main(): i32 { var x = 5; if (x > 0) { if (x > 3) { x = 100; } else { x = 50; } } return x; }"},
		{"while-sum", "function main(): i32 { var i = 1; var s = 0; while (i <= 5) { s = s + i; i = i + 1; } return s; }"},
		{"while-factorial", "function main(): i32 { var i = 1; var f = 1; while (i <= 5) { f = f * i; i = i + 1; } return f; }"},
		{"if-in-loop", "function main(): i32 { var i = 0; var c = 0; while (i < 10) { if (i > 4) { c = c + 1; } i = i + 1; } return c; }"},
		{"nested-loop", "function main(): i32 { var i = 0; var t = 0; while (i < 3) { var j = 0; while (j < 3) { t = t + 1; j = j + 1; } i = i + 1; } return t; }"},
		// Params + direct calls (slice 17) -> now IR-eligible; the IR path must
		// match the AST path through asm.fern's stack-arg ABI.
		{"call-one-arg", "function inc(n: i32): i32 { return n + 1; } function main(): i32 { return inc(41); }"},
		{"call-two-args", "function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(40, 2); }"},
		{"call-three-args", "function f(a: i32, b: i32, c: i32): i32 { return a * 100 + b * 10 + c; } function main(): i32 { return f(1, 2, 3); }"},
		{"call-arg-order", "function sub(a: i32, b: i32): i32 { return a - b; } function main(): i32 { return sub(50, 8); }"},
		{"call-nested-args", "function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(add(10, 20), add(5, 7)); }"},
		{"recursion-factorial", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }"},
		{"recursion-fib", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(8); }"},
		{"mutual-recursion", "function is_even(n: i32): i32 { if (n == 0) { return 1; } return is_odd(n - 1); } function is_odd(n: i32): i32 { if (n == 0) { return 0; } return is_even(n - 1); } function main(): i32 { return is_even(6); }"},
		{"call-in-loop", "function sq(x: i32): i32 { return x * x; } function main(): i32 { var i = 1; var s = 0; while (i <= 4) { s = s + sq(i); i = i + 1; } return s; }"},
		{"compute-via-call", "function compute(a: i32): i32 { var b = a * 2; var c = b + 1; return c; } function main(): i32 { return compute(5); }"},
		// Within-function i32 arrays (slice 18) -> IR path with the freestanding
		// allocator + Perceus RC; values must match the AST array runtime.
		{"arr-index", "function main(): i32 { var a = [10, 20, 30]; return a[0] + a[2]; }"},
		{"arr-loop-sum", "function main(): i32 { var a = [5, 10, 15, 20, 25]; var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }"},
		{"arr-expr-elems", "function main(): i32 { var x = 4; var a = [x, x * 2, x + 100]; return a[1] + a[2]; }"},
		{"arr-set-index", "function main(): i32 { var a = [10, 20, 30]; a[1] = 99; return a[0] + a[1] + a[2]; }"},
		{"arr-set-fill", "function main(): i32 { var a = [0, 0, 0, 0, 0]; var i = 0; while (i < 5) { a[i] = i * i; i = i + 1; } return a[0] + a[1] + a[2] + a[3] + a[4]; }"},
		{"arr-len", "function main(): i32 { var a = [1, 2, 3, 4]; return a.len(); }"},
		{"arr-two", "function main(): i32 { var a = [1, 2]; var b = [100, 200]; return a[1] + b[0]; }"},
		{"arr-alias", "function main(): i32 { var a = [10, 20, 30]; var b = a; return b[0] + b[2] + a.len(); }"},
		// Cross-function arrays (slice 19): borrowed array params + array
		// returns (move-on-return). Whole module is IR-eligible, so caller and
		// callee share irlower's layout — the move/borrow paths run end-to-end.
		{"arr-param-sum", "function sum(a: i32[]): i32 { var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; } function main(): i32 { var arr = [10, 20, 30]; return sum(arr); }"},
		{"arr-param-borrow-noreuse", "function len_of(a: i32[]): i32 { return a.len(); } function main(): i32 { var arr = [3, 4, 5]; var n = len_of(arr); var z = [9, 9, 9]; return arr[0] + arr[2] + n; }"},
		{"arr-return-move", "function make(): i32[] { var a = [10, 20, 30]; return a; } function main(): i32 { var x = make(); var y = [1, 1, 1]; return x[0] + x[2]; }"},
		{"arr-return-then-mutate", "function make(): i32[] { var a = [1, 2, 3]; return a; } function main(): i32 { var x = make(); x[1] = 99; return x[0] + x[1] + x[2]; }"},
		{"arr-param-two", "function pick(a: i32[], b: i32[]): i32 { return a[0] + b[1]; } function main(): i32 { var p = [1, 2]; var q = [10, 20]; return pick(p, q); }"},
		// Out of the IR subset -> falls back to the AST emitter under -ir; must
		// still match (proves the fallback path is intact).
		{"method-falls-back", "struct P { x: i32 } pub function (p: P) get(): i32 { return p.x; } function main(): i32 { var p = P { x: 42 }; return p.get(); }"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			astCode := emitAndRun(t, tc.src, false)
			irCode := emitAndRun(t, tc.src, true)
			if astCode != irCode {
				t.Errorf("AST-path vs IR-path mismatch for %q: AST=%d IR=%d", tc.name, astCode, irCode)
			}
		})
	}
}
