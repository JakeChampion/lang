package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostWasmRun exercises the self-hosted wasm emitter
// (examples/self_host/wasm.fern) end to end. wasm_run.fern reads Fern
// source from stdin, runs it through the self-host lexer + parser +
// wasm.emit_module, and prints a WASI core module in text format (WAT).
// For each case the test:
//
//  1. builds wasm_run.fern once with the Go x86-64 backend,
//  2. pipes the source in, capturing the emitted WAT,
//  3. runs it with `wasmtime run prog.wat`,
//  4. asserts the process exit code matches the program's result
//     (the emitter lowers `return <expr>;` to `proc_exit(<expr>)`).
//
// This is the wasm analogue of TestSelfHostAsmRunX86_64, and the first
// slice of the wasm backend on the path to retiring the Go wasm backend.
// Covered now: integer literals + unary `-` + binary `+ - * / %`.
func TestSelfHostWasmRun(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "wasm.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	cases := []struct {
		name   string
		source string
		exit   int
		stdout string // checked only when non-empty
	}{
		{"return-literal", "function main(): i32 { return 42; }", 42, ""},
		{"bare-return", "return 42;", 42, ""},
		{"arithmetic", "function main(): i32 { return 1 + 2 * 3; }", 7, ""},
		{"parens", "function main(): i32 { return (1 + 2) * 3; }", 9, ""},
		{"subtraction", "function main(): i32 { return 100 - 23; }", 77, ""},
		{"division", "function main(): i32 { return 84 / 2; }", 42, ""},
		{"modulo", "function main(): i32 { return 23 % 5; }", 3, ""},
		{"div-rem-combined", "function main(): i32 { return 84 / 2 + 23 % 5; }", 45, ""},
		// Non-trapping div/rem (would trap with naked i32.div_s/rem_s).
		{"div-by-zero", "function main(): i32 { return 5 / 0; }", 0, ""},
		{"mod-by-zero", "function main(): i32 { return 7 % 0; }", 7, ""},
		// INT_MIN / -1 == INT_MIN (no overflow trap); INT_MIN % -1 == 0.
		// INT_MIN is spelled `0 - 2147483647 - 1` to avoid a literal that
		// doesn't fit i32; the divisor `0 - 1` keeps it a runtime value.
		{"int-min-div-neg1", "function main(): i32 { var x = 0 - 2147483647 - 1; var d = 0 - 1; if (x / d == x) { return 42; } return 1; }", 42, ""},
		{"int-min-mod-neg1", "function main(): i32 { var x = 0 - 2147483647 - 1; var d = 0 - 1; if (x % d == 0) { return 42; } return 1; }", 42, ""},
		{"unary-neg", "function main(): i32 { return 0 - 5 + 10; }", 5, ""},
		{"nested", "function main(): i32 { return (2 + 3) * (4 + 4) - 1; }", 39, ""},
		{"no-return-exits-0", "function main(): i32 { }", 0, ""},
		// Locals + reassignment.
		{"locals", "function main(): i32 { var x = 5; var y = 10; return x + y; }", 15, ""},
		{"reassign", "function main(): i32 { var x = 5; x = x + 3; return x; }", 8, ""},
		{"compound-assign", "function main(): i32 { var x = 1; x *= 6; x += 1; return x; }", 7, ""},
		// Comparisons (0/1 results).
		{"comparison-true", "function main(): i32 { return 5 < 10; }", 1, ""},
		{"comparison-false", "function main(): i32 { return 10 < 5; }", 0, ""},
		{"equality-true", "function main(): i32 { return 7 == 7; }", 1, ""},
		// Logical + not.
		{"and", "function main(): i32 { if (true && true) { return 1; } return 0; }", 1, ""},
		{"or", "function main(): i32 { if (false || true) { return 1; } return 0; }", 1, ""},
		{"not", "function main(): i32 { if (!false) { return 1; } return 0; }", 1, ""},
		{"and-with-comparison", "function main(): i32 { var x = 5; if (x > 0 && x < 10) { return 1; } return 0; }", 1, ""},
		// if / else.
		{"if-then-branch", "function main(): i32 { var x = 5; if (x < 10) { return 1; } return 2; }", 1, ""},
		{"if-else-branch", "function main(): i32 { var x = 20; if (x < 10) { return 1; } return 2; }", 2, ""},
		{"if-else-explicit", "function main(): i32 { if (true) { return 9; } else { return 0; } }", 9, ""},
		// while.
		{"while-sum", "function main(): i32 { var i = 1; var s = 0; while (i <= 5) { s += i; i += 1; } return s; }", 15, ""},
		{"while-early-return", "function main(): i32 { var i = 0; while (i < 100) { if (i == 7) { return i; } i += 1; } return 99; }", 7, ""},
		// break / continue.
		{"break-in-while", "function main(): i32 { var i = 0; var s = 0; while (i < 100) { if (i == 5) { break; } s = s + i; i = i + 1; } return s; }", 10, ""},
		{"continue-in-while", "function main(): i32 { var i = 0; var s = 0; while (i < 10) { i = i + 1; if (i == 5) { continue; } s = s + i; } return s; }", 50, ""},
		// Mixed.
		{"mixed", "function main(): i32 { var a = 1 + 2; var b = 4 * 5; var c = a + b; if (c < 100) { return c; } return 0 - 1; }", 23, ""},
		// Function calls: free functions, args, locals, recursion,
		// mutual recursion.
		{"func-call", "function add(x: i32, y: i32): i32 { return x + y; } function main(): i32 { return add(2, 3); }", 5, ""},
		{"func-three-args", "function sum3(a: i32, b: i32, c: i32): i32 { return a + b + c; } function main(): i32 { return sum3(10, 20, 30); }", 60, ""},
		{"func-with-local-vars", "function compute(a: i32): i32 { var b = a * 2; var c = b + 1; return c; } function main(): i32 { return compute(5); }", 11, ""},
		{"recursive-factorial", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }", 120, ""},
		{"recursive-fib", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(8); }", 21, ""},
		{"mutual-recursion", "function is_even(n: i32): i32 { if (n == 0) { return 1; } return is_odd(n - 1); } function is_odd(n: i32): i32 { if (n == 0) { return 0; } return is_even(n - 1); } function main(): i32 { return is_even(6); }", 1, ""},
		{"call-in-condition", "function dbl(n: i32): i32 { return n * 2; } function main(): i32 { if (dbl(5) == 10) { return 42; } return 1; }", 42, ""},
		// Strings + linear memory: write (verbatim) and print (+\n) of
		// string literals, lowered to fd_write on the data section.
		{"write-verbatim", "function main(): i32 { write(\"hi\"); return 0; }", 0, "hi"},
		{"print-adds-newline", "function main(): i32 { print(\"hello\"); return 0; }", 0, "hello\n"},
		{"print-twice", "function main(): i32 { print(\"line 1\"); print(\"line 2\"); return 0; }", 0, "line 1\nline 2\n"},
		{"write-then-return", "function main(): i32 { write(\"out\"); return 42; }", 42, "out"},
		{"hello-world", "function main(): i32 { print(\"Hello, world!\"); return 0; }", 0, "Hello, world!\n"},
		{"write-with-embedded-newline", "function main(): i32 { write(\"a\\nb\"); return 0; }", 0, "a\nb"},
		{"print-in-function", "function greet(): i32 { print(\"hi from greet\"); return 7; } function main(): i32 { return greet(); }", 7, "hi from greet\n"},
		{"print-dedup-same-literal", "function main(): i32 { write(\"x\"); write(\"x\"); return 0; }", 0, "xx"},
		// print_int: integer → decimal formatted into memory.
		{"print-int-literal", "function main(): i32 { print_int(42); return 0; }", 0, "42"},
		{"print-int-zero", "function main(): i32 { print_int(0); return 0; }", 0, "0"},
		{"print-int-negative", "function main(): i32 { print_int(0 - 7); return 0; }", 0, "-7"},
		{"print-int-computed", "function main(): i32 { print_int(2 + 3 * 4); return 0; }", 0, "14"},
		{"print-int-multidigit", "function main(): i32 { print_int(1234567); return 0; }", 0, "1234567"},
		{"print-int-then-newline", "function main(): i32 { print_int(99); write(\"\\n\"); return 0; }", 0, "99\n"},
		{"print-int-int-min", "function main(): i32 { print_int(0 - 2147483647 - 1); return 0; }", 0, "-2147483648"},
		{"print-int-from-function", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { print_int(fact(8)); return 0; }", 0, "40320"},
		{"print-int-loop", "function main(): i32 { var i = 1; while (i <= 5) { print_int(i); write(\" \"); i = i + 1; } return 0; }", 0, "1 2 3 4 5 "},
		{"fizzbuzz-1-to-15", "function main(): i32 { var i = 1; while (i <= 15) { if (i % 15 == 0) { write(\"FizzBuzz\"); } else if (i % 3 == 0) { write(\"Fizz\"); } else if (i % 5 == 0) { write(\"Buzz\"); } else { print_int(i); } write(\"\\n\"); i = i + 1; } return 0; }", 0, "1\n2\nFizz\n4\nBuzz\nFizz\n7\n8\nFizz\nBuzz\n11\nFizz\n13\n14\nFizzBuzz\n"},
		// String values: literals flow through locals / params / returns
		// / reassignment as i32 pointers to [len][bytes] blocks.
		{"string-local", "function main(): i32 { var s = \"hello\"; write(s); return 0; }", 0, "hello"},
		{"string-local-print", "function main(): i32 { var s = \"hi\"; print(s); return 0; }", 0, "hi\n"},
		{"string-reassign", "function main(): i32 { var s = \"a\"; s = \"b\"; write(s); return 0; }", 0, "b"},
		{"string-two-locals", "function main(): i32 { var a = \"foo\"; var b = \"bar\"; write(a); write(b); return 0; }", 0, "foobar"},
		{"string-param", "function emit(s: string): i32 { write(s); return 0; } function main(): i32 { emit(\"param-str\"); return 0; }", 0, "param-str"},
		{"string-return", "function greet(): string { return \"howdy\"; } function main(): i32 { write(greet()); return 0; }", 0, "howdy"},
		{"string-return-local", "function pick(): string { var s = \"chosen\"; return s; } function main(): i32 { print(pick()); return 0; }", 0, "chosen\n"},
		{"string-through-call", "function id(s: string): string { return s; } function main(): i32 { write(id(\"echo\")); return 0; }", 0, "echo"},
		{"string-in-loop", "function main(): i32 { var s = \"x\"; var i = 0; while (i < 3) { write(s); i = i + 1; } return 0; }", 0, "xxx"},
		// String concatenation (+): bump-allocated [len][bytes] result.
		{"concat-literals", "function main(): i32 { write(\"foo\" + \"bar\"); return 0; }", 0, "foobar"},
		{"concat-var-literal", "function main(): i32 { var s = \"hello, \"; write(s + \"world\"); return 0; }", 0, "hello, world"},
		{"concat-two-vars", "function main(): i32 { var a = \"ab\"; var b = \"cd\"; write(a + b); return 0; }", 0, "abcd"},
		{"concat-chain", "function main(): i32 { write(\"a\" + \"b\" + \"c\" + \"d\"); return 0; }", 0, "abcd"},
		{"concat-to-local", "function main(): i32 { var s = \"x\" + \"y\"; write(s); return 0; }", 0, "xy"},
		{"concat-then-print", "function main(): i32 { var g = \"hi, \" + \"there\"; print(g); return 0; }", 0, "hi, there\n"},
		{"concat-param", "function greet(name: string): string { return \"hi \" + name; } function main(): i32 { write(greet(\"sam\")); return 0; }", 0, "hi sam"},
		{"concat-in-loop", "function main(): i32 { var s = \"\"; var i = 0; while (i < 3) { s = s + \"ab\"; i = i + 1; } write(s); return 0; }", 0, "ababab"},
		{"concat-empty", "function main(): i32 { var s = \"\"; write(s + \"end\"); return 0; }", 0, "end"},
		// int + still adds (not concat) — type-directed.
		{"int-plus-still-adds", "function main(): i32 { var a = 20; var b = 22; return a + b; }", 42, ""},
		// String equality / comparison + .len() (type-directed).
		{"str-eq-true", "function main(): i32 { var a = \"hi\"; if (a == \"hi\") { return 1; } return 0; }", 1, ""},
		{"str-eq-false", "function main(): i32 { var a = \"hi\"; if (a == \"ho\") { return 1; } return 0; }", 0, ""},
		{"str-eq-vars", "function main(): i32 { var a = \"foo\"; var b = \"f\" + \"oo\"; if (a == b) { return 42; } return 0; }", 42, ""},
		{"str-neq", "function main(): i32 { var a = \"x\"; if (a != \"y\") { return 1; } return 0; }", 1, ""},
		{"str-lt", "function main(): i32 { if (\"abc\" < \"abd\") { return 1; } return 0; }", 1, ""},
		{"str-lt-prefix", "function main(): i32 { if (\"ab\" < \"abc\") { return 1; } return 0; }", 1, ""},
		{"str-gt", "function main(): i32 { if (\"b\" > \"a\") { return 1; } return 0; }", 1, ""},
		{"str-ge-equal", "function main(): i32 { if (\"same\" >= \"same\") { return 1; } return 0; }", 1, ""},
		{"str-len", "function main(): i32 { var s = \"hello\"; return s.len(); }", 5, ""},
		{"str-len-empty", "function main(): i32 { var s = \"\"; return s.len(); }", 0, ""},
		{"str-len-literal", "function main(): i32 { return \"abcd\".len(); }", 4, ""},
		{"str-len-concat", "function main(): i32 { var s = \"ab\" + \"cde\"; return s.len(); }", 5, ""},
		{"str-len-param", "function l(s: string): i32 { return s.len(); } function main(): i32 { return l(\"seven!!\"); }", 7, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.source))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", watPath)
			out, _ := cmd.Output() // captures the program's stdout
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n--- WAT ---\n%s", tc.name, code, tc.exit, wat)
			}
			if tc.stdout != "" && string(out) != tc.stdout {
				t.Errorf("%s: wasm stdout = %q, want %q\n--- WAT ---\n%s", tc.name, string(out), tc.stdout, wat)
			}
		})
	}
}
