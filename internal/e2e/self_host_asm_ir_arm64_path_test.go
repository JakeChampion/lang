package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostAsmIRArm64Path is the arm64 sibling of TestSelfHostAsmIRPath:
// the differential gate for the arm64 stack-IR emitter (asm_arm64_ir.fern).
// The asm_arm64_ir_run driver's `-ir` flag, when the module is fully
// i32-eligible, emits via the IR path (asm_arm64_ir.emit_module_ir: AST ->
// stack IR -> arm64); otherwise it uses the unchanged AST backend
// (asm_arm64.emit_module). Each program is compiled BOTH ways, assembled with
// the aarch64 toolchain, run under qemu-aarch64, and the two exit codes must
// match — proving the arm64 IR path is behaviour-equivalent to the production
// arm64 AST path on the shared i32 + arrays subset (the rollout prerequisite
// before the arm64 default can flip to the IR). asm_arm64.fern and the
// asm_arm64_run bootstrap are UNCHANGED.
//
// The driver itself runs on the test host (built via the x86-64 backend);
// only the emitted program asm is arm64. CI-gated arm64 (qemu).
func TestSelfHostAsmIRArm64Path(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64.fern", "asm_arm64_ir.fern",
		"asm_arm64_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// Build the driver once via the x86-64 backend (it runs on the host).
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_arm64_ir_run.fern", "driver")

	// emitAndRun pipes src to the driver (optionally with `-ir`), assembles the
	// emitted arm64 asm, runs it under qemu, returns the inner exit code.
	emitAndRun := func(t *testing.T, src string, ir bool) int {
		t.Helper()
		driverArgs := []string{}
		if ir {
			driverArgs = append(driverArgs, "-ir")
		}
		var cmd *exec.Cmd
		if len(x86runner) == 0 {
			cmd = exec.Command(driverBin, driverArgs...)
		} else {
			cmd = exec.Command(x86runner[0], append(append(append([]string{}, x86runner[1:]...), driverBin), driverArgs...)...)
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
		bin := buildBinArm64(t, arm64gcc, dir, tag+"_inner", string(emitted))
		inner := runArm64Bin(qemu, bin)
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
		// Pure i32 (single + multi-function, recursion, control flow).
		{"const", "function main(): i32 { return 42; }"},
		{"arith", "function main(): i32 { return 2 + 3 * 4; }"},
		{"locals", "function main(): i32 { var x = 2 + 3 * 4; var y = x - 5; return y * 2; }"},
		{"reassign", "function main(): i32 { var x = 5; x = x + 3; return x; }"},
		{"modulo", "function main(): i32 { return 23 % 5; }"},
		{"division", "function main(): i32 { return 84 / 2; }"},
		{"bitwise", "function main(): i32 { return (6 & 3) | 8; }"},
		{"shift", "function main(): i32 { return 1 << 4; }"},
		{"compare", "function main(): i32 { return 5 < 10; }"},
		{"if-else", "function main(): i32 { var x = 0; if (2 < 1) { x = 3; } else { x = 9; } return x; }"},
		{"nested-if", "function main(): i32 { var x = 5; if (x > 0) { if (x > 3) { x = 100; } else { x = 50; } } return x; }"},
		{"while-sum", "function main(): i32 { var i = 1; var s = 0; while (i <= 5) { s = s + i; i = i + 1; } return s; }"},
		{"nested-loop", "function main(): i32 { var i = 0; var t = 0; while (i < 3) { var j = 0; while (j < 3) { t = t + 1; j = j + 1; } i = i + 1; } return t; }"},
		{"call-args", "function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(40, 2); }"},
		{"factorial", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }"},
		{"fib", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(8); }"},
		{"mutual", "function is_even(n: i32): i32 { if (n == 0) { return 1; } return is_odd(n - 1); } function is_odd(n: i32): i32 { if (n == 0) { return 0; } return is_even(n - 1); } function main(): i32 { return is_even(6); }"},
		// Arrays: literal, index, len, set-index, alias, two arrays.
		{"arr-index", "function main(): i32 { var a = [10, 20, 30]; return a[0] + a[2]; }"},
		{"arr-loop-sum", "function main(): i32 { var a = [5, 10, 15, 20, 25]; var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }"},
		{"arr-set-fill", "function main(): i32 { var a = [0, 0, 0, 0, 0]; var i = 0; while (i < 5) { a[i] = i * i; i = i + 1; } return a[0] + a[1] + a[2] + a[3] + a[4]; }"},
		{"arr-len", "function main(): i32 { var a = [1, 2, 3, 4]; return a.len(); }"},
		{"arr-two", "function main(): i32 { var a = [1, 2]; var b = [100, 200]; return a[1] + b[0]; }"},
		{"arr-alias", "function main(): i32 { var a = [10, 20, 30]; var b = a; return b[0] + b[2] + a.len(); }"},
		// Cross-function arrays: borrowed params + array return (move-on-return).
		{"arr-param-sum", "function sum(a: i32[]): i32 { var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; } function main(): i32 { var arr = [10, 20, 30]; return sum(arr); }"},
		{"arr-return-move", "function make(): i32[] { var a = [10, 20, 30]; return a; } function main(): i32 { var x = make(); var y = [1, 1, 1]; return x[0] + x[2]; }"},
		{"arr-param-two", "function pick(a: i32[], b: i32[]): i32 { return a[0] + b[1]; } function main(): i32 { var p = [1, 2]; var q = [10, 20]; return pick(p, q); }"},
		// Array-slot reassignment Perceus (retain-new + cow-guarded release-old).
		{"arr-reassign-alias", "function main(): i32 { var xs = [1, 2, 3]; var ys = [4, 5, 6]; ys = xs; return ys[0] + ys[2]; }"},
		{"arr-reassign-fresh", "function main(): i32 { var xs = [1, 2]; xs = [9, 9, 9]; return xs[2]; }"},
		{"arr-rebind-loop", "function main(): i32 { var s = 0; var i = 0; while (i < 4) { var r = [i, i * 2, i * 3]; s = s + r[2]; i = i + 1; } return s; }"},
		// Strings: literal + .len(), concat (+), equality (==/!=), incl. string
		// params. The IR path reuses asm_arm64's 16-byte `[data@0,len@8]` box +
		// __fern_str_concat/_eq helpers; exit codes must match the AST path.
		{"str-len", `function main(): i32 { var s = "hello"; return s.len(); }`},
		{"str-literal-len", `function main(): i32 { return "world!".len(); }`},
		{"str-empty-len", `function main(): i32 { var s = ""; return s.len(); }`},
		{"str-concat-len", `function main(): i32 { var a = "ab"; var b = "cde"; var c = a + b; return c.len(); }`},
		{"str-concat-direct", `function main(): i32 { return ("foo" + "bar").len(); }`},
		{"str-concat-chain", `function main(): i32 { var a = "a"; var b = "bb"; var c = "ccc"; return (a + b + c).len(); }`},
		{"str-eq-true", `function main(): i32 { var a = "hi"; var b = "hi"; if (a == b) { return 7; } return 0; }`},
		{"str-eq-false", `function main(): i32 { var a = "hi"; var b = "ho"; if (a == b) { return 7; } return 9; }`},
		{"str-ne-true", `function main(): i32 { var a = "hi"; var b = "ho"; if (a != b) { return 3; } return 0; }`},
		{"str-concat-eq", `function main(): i32 { var a = "foo"; var b = "foobar"; if (a + "bar" == b) { return 11; } return 0; }`},
		{"str-param-len", `function slen(s: string): i32 { return s.len(); } function main(): i32 { var x = "abcd"; return slen(x); }`},
		{"str-param-concat", `function jn(a: string, b: string): i32 { return (a + b).len(); } function main(): i32 { return jn("xx", "yyy"); }`},
		// String-returning function isn't IR-lowered yet -> module falls back to AST.
		{"str-returning-falls-back", `function greet(): string { return "hi"; } function main(): i32 { var s = greet(); return s.len(); }`},
		// Out of subset -> falls back to AST under -ir; must still match.
		{"method-falls-back", "struct P { x: i32 } pub function (p: P) get(): i32 { return p.x; } function main(): i32 { var p = P { x: 42 }; return p.get(); }"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			astCode := emitAndRun(t, tc.src, false)
			irCode := emitAndRun(t, tc.src, true)
			if astCode != irCode {
				t.Errorf("arm64 AST-path vs IR-path mismatch for %q: AST=%d IR=%d", tc.name, astCode, irCode)
			}
		})
	}
}
