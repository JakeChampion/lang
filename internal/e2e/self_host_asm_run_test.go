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

// Bootstrap-style end-to-end demo. asm_run.lang is a driver
// that reads lang source from stdin, runs it through the
// self-host lexer + parser + asm emitter, and prints the
// resulting AT&T x86_64 assembly to stdout. This table-driven
// test runs every entry through that pipeline:
//
//   1. Build asm_run.lang once via the production langc.
//   2. For each test case: pipe its source to the driver,
//      capture stdout (= emitted asm), gcc-assemble the asm
//      into a standalone Linux ELF, run it, assert the inner
//      exit code matches the entry's expected value.
//
// End-to-end: lang source → lang-port asm emitter → real
// native binary → expected exit code. Proves the asm.lang
// lowering produces working executables across the full
// feature matrix it covers.

func TestSelfHostAsmRunX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.lang", "parser.lang", "asm.lang", "asm_run.lang"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// Build the driver once.
	prog, _, err := modload.Load(filepath.Join(dir, "asm_run.lang"))
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

	// Each case: pipe source to driver, capture asm, assemble,
	// run, verify exit code.
	cases := []struct {
		name     string
		source   string
		expected int
		stdout   string // "" means don't check
		stdin    string // "" means no stdin
	}{
		{"return-literal", "return 42;", 42, "", ""},
		{"arithmetic", "return 1 + 2 * 3;", 7, "", ""},
		{"parens", "return (1 + 2) * 3;", 9, "", ""},
		{"subtraction", "return 100 - 23;", 77, "", ""},
		{"division", "return 84 / 2;", 42, "", ""},
		{"modulo", "return 23 % 5;", 3, "", ""},
		{"unary-neg", "return 0 - 5 + 10;", 5, "", ""},
		{"comparison-true", "return 5 < 10;", 1, "", ""},
		{"comparison-false", "return 10 < 5;", 0, "", ""},
		{"equality-true", "return 7 == 7;", 1, "", ""},
		{"equality-false", "return 7 == 8;", 0, "", ""},
		{"locals", "var x = 5; var y = 10; return x + y;", 15, "", ""},
		{"reassign", "var x = 5; x = x + 3; return x;", 8, "", ""},
		{"compound-assign", "var x = 1; x *= 6; x += 1; return x;", 7, "", ""},
		{"if-then-branch", "var x = 5; if (x < 10) { return 1; } return 2;", 1, "", ""},
		{"if-else-branch", "var x = 20; if (x < 10) { return 1; } return 2;", 2, "", ""},
		{"if-else-explicit", "if (true) { return 9; } else { return 0; }", 9, "", ""},
		{"while-sum", "var i = 1; var s = 0; while (i <= 5) { s += i; i += 1; } return s;", 15, "", ""},
		{"while-early-return", "var i = 0; while (i < 100) { if (i == 7) { return i; } i += 1; } return 0 - 1;", 7, "", ""},
		{"mixed", "var a = 1 + 2; var b = 4 * 5; var c = a + b; if (c < 100) { return c; } return 0 - 1;", 23, "", ""},
		{"func-decl-call", "function add(x: i32, y: i32): i32 { return x + y; } function main(): i32 { return add(2, 3); }", 5, "", ""},
		{"func-three-args", "function sum3(a: i32, b: i32, c: i32): i32 { return a + b + c; } function main(): i32 { return sum3(10, 20, 30); }", 60, "", ""},
		{"recursive-factorial", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }", 120, "", ""},
		{"recursive-fib", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(8); }", 21, "", ""},
		{"mutual-recursion", "function is_even(n: i32): i32 { if (n == 0) { return 1; } return is_odd(n - 1); } function is_odd(n: i32): i32 { if (n == 0) { return 0; } return is_even(n - 1); } function main(): i32 { return is_even(6); }", 1, "", ""},
		{"func-with-local-vars", "function compute(a: i32): i32 { var b = a * 2; var c = b + 1; return c; } function main(): i32 { return compute(5); }", 11, "", ""},
		{"hello-world", "print(\"Hello, world!\\n\"); return 0;", 0, "Hello, world!\n", ""},
		{"print-twice", "print(\"line 1\\n\"); print(\"line 2\\n\"); return 0;", 0, "line 1\nline 2\n", ""},
		{"print-then-return", "print(\"out\\n\"); return 42;", 42, "out\n", ""},
		{
			"fizzbuzz-1-to-15",
			"function main(): i32 { " +
				"var i = 1; " +
				"while (i <= 15) { " +
				"if (i % 15 == 0) { print(\"FizzBuzz \"); } " +
				"else if (i % 3 == 0) { print(\"Fizz \"); } " +
				"else if (i % 5 == 0) { print(\"Buzz \"); } " +
				"else { print(\". \"); } " +
				"i = i + 1; " +
				"} " +
				"print(\"\\n\"); " +
				"return 0; }",
			0,
			". . Fizz . Buzz Fizz . . Fizz Buzz . Fizz . . FizzBuzz \n",
			"",
		},
		{
			"print-in-function",
			"function greet(): i32 { print(\"hi from greet\\n\"); return 7; } " +
				"function main(): i32 { var r = greet(); return r; }",
			7,
			"hi from greet\n",
			"",
		},
		{
			"print-loop-fixed-count",
			"function main(): i32 { var i = 0; while (i < 4) { print(\"tick\\n\"); i = i + 1; } return 0; }",
			0,
			"tick\ntick\ntick\ntick\n",
			"",
		},
		{
			"print-int-literal",
			"function main(): i32 { print_int(42); print(\"\\n\"); return 0; }",
			0,
			"42\n",
			"",
		},
		{
			"print-int-zero",
			"function main(): i32 { print_int(0); print(\"\\n\"); return 0; }",
			0,
			"0\n",
			"",
		},
		{
			"print-int-negative",
			"function main(): i32 { print_int(0 - 7); print(\"\\n\"); return 0; }",
			0,
			"-7\n",
			"",
		},
		{
			"print-int-computed",
			"function main(): i32 { print_int(2 + 3 * 4); print(\"\\n\"); return 0; }",
			0,
			"14\n",
			"",
		},
		{
			"print-int-from-function",
			"function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } " +
				"function main(): i32 { print_int(fact(8)); print(\"\\n\"); return 0; }",
			0,
			"40320\n",
			"",
		},
		{
			"print-int-counter-loop",
			"function main(): i32 { var i = 1; while (i <= 5) { print_int(i); print(\" \"); i = i + 1; } print(\"\\n\"); return 0; }",
			0,
			"1 2 3 4 5 \n",
			"",
		},
		{
			"fizzbuzz-canonical-1-to-15",
			"function main(): i32 { " +
				"var i = 1; " +
				"while (i <= 15) { " +
				"if (i % 15 == 0) { print(\"FizzBuzz\"); } " +
				"else if (i % 3 == 0) { print(\"Fizz\"); } " +
				"else if (i % 5 == 0) { print(\"Buzz\"); } " +
				"else { print_int(i); } " +
				"print(\"\\n\"); " +
				"i = i + 1; " +
				"} " +
				"return 0; }",
			0,
			"1\n2\nFizz\n4\nBuzz\nFizz\n7\n8\nFizz\nBuzz\n11\nFizz\n13\n14\nFizzBuzz\n",
			"",
		},
		{
			"fibonacci-series-first-10",
			"function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } " +
				"function main(): i32 { var i = 0; while (i < 10) { print_int(fib(i)); print(\" \"); i = i + 1; } print(\"\\n\"); return 0; }",
			0,
			"0 1 1 2 3 5 8 13 21 34 \n",
			"",
		},
		{
			"sum-via-recursion-and-print",
			"function sum(n: i32): i32 { if (n == 0) { return 0; } return n + sum(n - 1); } " +
				"function main(): i32 { print(\"sum(1..10) = \"); print_int(sum(10)); print(\"\\n\"); return 0; }",
			0,
			"sum(1..10) = 55\n",
			"",
		},
		// read_int demos — pipe stdin via the cases struct's
		// stdin field. The inner binary reads, parses, computes,
		// and either prints the result or returns it via exit
		// code.
		{
			"read-int-double-via-exit",
			"function main(): i32 { return read_int() * 2; }",
			42,
			"",
			"21",
		},
		{
			"read-int-print-doubled",
			"function main(): i32 { var n = read_int(); print_int(n * 2); print(\"\\n\"); return 0; }",
			0,
			"50\n",
			"25",
		},
		{
			"read-int-square",
			"function main(): i32 { var n = read_int(); print_int(n * n); print(\"\\n\"); return 0; }",
			0,
			"49\n",
			"7",
		},
		{
			"read-int-negative",
			"function main(): i32 { var n = read_int(); if (n < 0) { print(\"neg\\n\"); } else { print(\"pos\\n\"); } return 0; }",
			0,
			"neg\n",
			"-42",
		},
		{
			"primes-up-to-30",
			"function is_prime(n: i32): i32 { if (n < 2) { return 0; } var i = 2; while (i * i <= n) { if (n % i == 0) { return 0; } i = i + 1; } return 1; } " +
				"function main(): i32 { var i = 2; while (i <= 30) { if (is_prime(i) == 1) { print_int(i); print(\" \"); } i = i + 1; } print(\"\\n\"); return 0; }",
			0,
			"2 3 5 7 11 13 17 19 23 29 \n",
			"",
		},
		{
			"squares-1-to-5",
			"function main(): i32 { var i = 1; while (i <= 5) { print_int(i * i); print(\" \"); i = i + 1; } print(\"\\n\"); return 0; }",
			0,
			"1 4 9 16 25 \n",
			"",
		},
		{
			"power-of-two",
			"function pow2(n: i32): i32 { if (n == 0) { return 1; } return 2 * pow2(n - 1); } " +
				"function main(): i32 { var i = 0; while (i <= 10) { print_int(pow2(i)); print(\" \"); i = i + 1; } print(\"\\n\"); return 0; }",
			0,
			"1 2 4 8 16 32 64 128 256 512 1024 \n",
			"",
		},
		{
			"tuple-literal-access-zero",
			"function main(): i32 { var t = (7, 11, 13); return t.0; }",
			7,
			"",
			"",
		},
		{
			"tuple-literal-access-middle",
			"function main(): i32 { var t = (7, 11, 13); return t.1; }",
			11,
			"",
			"",
		},
		{
			"tuple-literal-access-last",
			"function main(): i32 { var t = (7, 11, 13); return t.2; }",
			13,
			"",
			"",
		},
		{
			"tuple-sum-fields",
			"function main(): i32 { var t = (10, 20, 30); return t.0 + t.1 + t.2; }",
			60,
			"",
			"",
		},
		{
			"tuple-of-expressions",
			"function main(): i32 { var x = 5; var t = (x * 2, x + 1, x - 1); return t.0 + t.1 + t.2; }",
			20,
			"",
			"",
		},
		{
			"array-literal-len",
			"function main(): i32 { var a = [10, 20, 30]; return len(a); }",
			3,
			"",
			"",
		},
		{
			"array-index-first",
			"function main(): i32 { var a = [42, 99, 7]; return a[0]; }",
			42,
			"",
			"",
		},
		{
			"array-index-middle",
			"function main(): i32 { var a = [42, 99, 7]; return a[1]; }",
			99,
			"",
			"",
		},
		{
			"array-index-last",
			"function main(): i32 { var a = [42, 99, 7]; return a[2]; }",
			7,
			"",
			"",
		},
		{
			"array-index-via-var",
			"function main(): i32 { var a = [10, 20, 30, 40]; var i = 2; return a[i]; }",
			30,
			"",
			"",
		},
		{
			"array-sum-via-while",
			"function main(): i32 { var a = [1, 2, 3, 4, 5]; var i = 0; var s = 0; while (i < len(a)) { s = s + a[i]; i = i + 1; } return s; }",
			15,
			"",
			"",
		},
		{
			"array-of-expressions",
			"function main(): i32 { var x = 4; var a = [x, x + 1, x * 2]; return a[0] + a[1] + a[2]; }",
			17,
			"",
			"",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.source))
			emittedAsm, err := cmd.Output()
			if err != nil {
				t.Fatalf("driver run: %v\n--- source ---\n%s", err, tc.source)
			}
			if len(emittedAsm) == 0 {
				t.Fatalf("driver produced no asm output")
			}
			caseDir := t.TempDir()
			innerAsm := filepath.Join(caseDir, "inner.s")
			innerBin := filepath.Join(caseDir, "inner")
			if err := os.WriteFile(innerAsm, emittedAsm, 0o644); err != nil {
				t.Fatalf("write inner asm: %v", err)
			}
			if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
				t.Fatalf("inner gcc: %v\n%s\n--- asm ---\n%s", err, out, emittedAsm)
			}
			var inner *exec.Cmd
			if len(runner) == 0 {
				inner = exec.Command(innerBin)
			} else {
				inner = exec.Command(runner[0], append(runner[1:], innerBin)...)
			}
			if tc.stdin != "" {
				inner.Stdin = bytes.NewReader([]byte(tc.stdin))
			}
			innerStdout, _ := inner.Output()
			if code := inner.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("inner exit code = %d, want %d\n--- source ---\n%s\n--- asm ---\n%s", code, tc.expected, tc.source, emittedAsm)
			}
			if tc.stdout != "" && string(innerStdout) != tc.stdout {
				t.Errorf("inner stdout = %q, want %q\n--- source ---\n%s\n--- asm ---\n%s", string(innerStdout), tc.stdout, tc.source, emittedAsm)
			}
		})
	}
}
