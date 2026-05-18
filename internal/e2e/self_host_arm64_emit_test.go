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

// Sibling to TestSelfHostAsmRunX86_64 — exercises the
// asm_arm64.lang ARM64 codegen layer. The driver
// (asm_arm64_run.lang) is compiled on the host (x86_64),
// reads lang source from stdin, and prints aarch64 assembly
// to stdout. The Go test pipes each source in, gcc-assembles
// the output with aarch64-linux-gnu-gcc, then runs the
// resulting binary under qemu-aarch64 (or natively on arm64
// hosts) and asserts the exit code matches.
//
// Scope mirrors asm_arm64.lang's: i32 literals + arithmetic
// (+ - * / %) + unary `-` + `return` only. Locals / control
// flow / functions land in follow-up PRs.

func TestSelfHostAsmArm64Bootstrap(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.lang", "parser.lang", "asm_arm64.lang", "asm_arm64_run.lang"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// Build the driver as an x86_64 binary — the driver itself
	// runs on the test host, only its OUTPUT is arm64 asm.
	prog, _, err := modload.Load(filepath.Join(dir, "asm_arm64_run.lang"))
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
	if out, err := exec.Command(x86gcc, "-static", "-nostdlib", "-no-pie", driverAsm, "-o", driverBin).CombinedOutput(); err != nil {
		t.Fatalf("driver gcc: %v\n%s", err, out)
	}

	cases := []struct {
		name     string
		source   string
		expected int
	}{
		{"return-literal", "return 42;", 42},
		{"arithmetic", "return 1 + 2 * 3;", 7},
		{"parens", "return (1 + 2) * 3;", 9},
		{"subtraction", "return 100 - 23;", 77},
		{"division", "return 84 / 2;", 42},
		{"modulo", "return 23 % 5;", 3},
		{"unary-neg-via-zero-minus", "return 0 - 5 + 10;", 5},
		{"nested-arith", "return (2 + 3) * 4;", 20},
		{"cmp-lt-true", "return 5 < 10;", 1},
		{"cmp-lt-false", "return 10 < 5;", 0},
		{"cmp-le-true", "return 5 <= 5;", 1},
		{"cmp-gt-true", "return 7 > 3;", 1},
		{"cmp-ge-true", "return 7 >= 7;", 1},
		{"cmp-eq-true", "return 4 == 4;", 1},
		{"cmp-eq-false", "return 4 == 5;", 0},
		{"cmp-ne-true", "return 4 != 5;", 1},
		{"bool-true", "return true;", 1},
		{"bool-false", "return false;", 0},
		{"if-then-taken", "if (true) { return 9; } else { return 0; }", 9},
		{"if-else-taken", "if (false) { return 9; } else { return 7; }", 7},
		{"if-no-else-fall", "if (false) { return 9; } return 5;", 5},
		{"if-cond-via-cmp", "if (5 < 10) { return 1; } else { return 2; }", 1},
		{"locals-single", "var x = 5; return x;", 5},
		{"locals-three", "var a = 10; var b = 20; var c = 30; return a + b + c;", 60},
		{"reassign", "var x = 5; x = x + 3; return x;", 8},
		{"compound-assign", "var x = 1; x *= 6; x += 1; return x;", 7},
		{"while-sum-counter", "var i = 1; var s = 0; while (i <= 5) { s += i; i += 1; } return s;", 15},
		{"while-early-return", "var i = 0; while (i < 100) { if (i == 7) { return i; } i += 1; } return 0 - 1;", 7},
		{"func-decl-call", "function add(x: i32, y: i32): i32 { return x + y; } function main(): i32 { return add(2, 3); }", 5},
		{"func-three-args", "function sum3(a: i32, b: i32, c: i32): i32 { return a + b + c; } function main(): i32 { return sum3(10, 20, 30); }", 60},
		{"recursive-factorial", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }", 120},
		{"recursive-fib", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(8); }", 21},
		{"mutual-recursion", "function is_even(n: i32): i32 { if (n == 0) { return 1; } return is_odd(n - 1); } function is_odd(n: i32): i32 { if (n == 0) { return 0; } return is_even(n - 1); } function main(): i32 { return is_even(6); }", 1},
		{"func-with-local-vars", "function compute(a: i32): i32 { var b = a * 2; var c = b + 1; return c; } function main(): i32 { return compute(5); }", 11},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Run the driver (x86_64 binary) to get the arm64 asm.
			var cmd *exec.Cmd
			if len(x86runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(x86runner[0], append(x86runner[1:], driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.source))
			emittedAsm, err := cmd.Output()
			if err != nil {
				t.Fatalf("driver run: %v\n--- source ---\n%s", err, tc.source)
			}
			caseDir := t.TempDir()
			innerAsm := filepath.Join(caseDir, "inner.s")
			innerBin := filepath.Join(caseDir, "inner")
			if err := os.WriteFile(innerAsm, emittedAsm, 0o644); err != nil {
				t.Fatalf("write inner asm: %v", err)
			}
			// Assemble + link as an arm64 binary.
			if out, err := exec.Command(gcc, "-static", "-nostdlib", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
				t.Fatalf("inner gcc: %v\n%s\n--- asm ---\n%s", err, out, emittedAsm)
			}
			inner := runArm64Bin(qemu, innerBin)
			_, _ = inner.CombinedOutput()
			if code := inner.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("inner exit code = %d, want %d\n--- source ---\n%s\n--- asm ---\n%s", code, tc.expected, tc.source, emittedAsm)
			}
		})
	}
}
