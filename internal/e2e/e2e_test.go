// Package e2e exercises the full pipeline: compile a .lang source string to
// ARM32 assembly, link it with the system's ARM cross-compiler, and run the
// resulting binary under qemu-arm.
//
// Each test SKIPS (rather than fails) when the cross-compiler or qemu-arm
// is not installed, so `go test ./...` stays green on machines without an
// ARM toolchain. CI installs both, so coverage is exercised there.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen"
	"github.com/jakechampion/lang/internal/parser"
)

func tooling(t *testing.T) (gcc, qemu string) {
	t.Helper()
	for _, c := range []string{"arm-linux-gnueabihf-gcc", "arm-linux-gnueabi-gcc"} {
		if p, err := exec.LookPath(c); err == nil {
			gcc = p
			break
		}
	}
	for _, c := range []string{"qemu-arm", "qemu-arm-static"} {
		if p, err := exec.LookPath(c); err == nil {
			qemu = p
			break
		}
	}
	if gcc == "" || qemu == "" {
		t.Skipf("ARM cross toolchain not available (gcc=%q qemu=%q)", gcc, qemu)
	}
	return gcc, qemu
}

func compileAndRun(t *testing.T, src string) (stdout string, exitCode int) {
	t.Helper()
	gcc, qemu := tooling(t)

	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}

	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s\n--- asm ---\n%s", err, out, asm)
	}
	cmd := exec.Command(qemu, binPath)
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode()
}

func TestExitCode(t *testing.T) {
	_, code := compileAndRun(t, `function main(): number { return 42; }`)
	if code != 42 {
		t.Errorf("exit = %d, want 42", code)
	}
}

// A leaf function with several params exercises the register-pinned
// prologue: the body still produces the right answer despite never
// touching the stack to read a/b/c/d.
func TestLeafFunctionAllRegArgs(t *testing.T) {
	src := `
		function leaf(a: number, b: number, c: number, d: number): number {
			return (a + b) * (c + d);
		}
		function main(): number { return leaf(2, 3, 4, 5); }`
	_, code := compileAndRun(t, src)
	if code != 45 {
		t.Errorf("exit = %d, want 45 ((2+3)*(4+5))", code)
	}
}

// Function-type syntax in parameter declarations lets a function
// accept another function as a value and call it indirectly.
func TestFunctionTypeAsParameter(t *testing.T) {
	src := `
		function add(a: number, b: number): number { return a + b; }
		function apply(f: (number, number) => number, a: number, b: number): number {
			return f(a, b);
		}
		function main(): number { return apply(add, 40, 2); }`
	_, code := compileAndRun(t, src)
	if code != 42 {
		t.Errorf("exit = %d, want 42", code)
	}
}

// Storing a function name in a var and calling through that var
// goes through the indirect-call path (blx r12).
func TestFunctionValueIndirectCall(t *testing.T) {
	src := `
		function add(a: number, b: number): number { return a + b; }
		function main(): number {
			var f = add;
			return f(40, 2);
		}`
	_, code := compileAndRun(t, src)
	if code != 42 {
		t.Errorf("exit = %d, want 42", code)
	}
}

func TestArithmeticAndCalls(t *testing.T) {
	src := `
		function add(a: number, b: number): number { return a + b; }
		function main(): number { return add(40, 2); }`
	_, code := compileAndRun(t, src)
	if code != 42 {
		t.Errorf("exit = %d, want 42", code)
	}
}

func TestFactorialRecursion(t *testing.T) {
	src := `
		function fact(n: number): number {
			if (n == 0) { return 1; }
			return n * fact(n - 1);
		}
		function main(): number { return fact(5); }`
	_, code := compileAndRun(t, src)
	if code != 120 {
		t.Errorf("exit = %d, want 120", code)
	}
}

func TestWhileLoop(t *testing.T) {
	src := `
		function main(): number {
			var sum: number = 0;
			var i: number = 1;
			while (i <= 10) { sum = sum + i; i = i + 1; }
			return sum;
		}`
	_, code := compileAndRun(t, src)
	if code != 55 {
		t.Errorf("exit = %d, want 55 (1+2+...+10)", code)
	}
}

func TestDivision(t *testing.T) {
	src := `function main(): number { return 100 / 7; }`
	_, code := compileAndRun(t, src)
	if code != 14 {
		t.Errorf("exit = %d, want 14", code)
	}
}

func TestComparisonsAndShortCircuit(t *testing.T) {
	src := `
		function inRange(x: number, lo: number, hi: number): boolean {
			return lo <= x && x <= hi;
		}
		function main(): number {
			if (inRange(5, 1, 10) && !inRange(20, 1, 10)) { return 1; }
			return 0;
		}`
	_, code := compileAndRun(t, src)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
}

func TestPutcharOutput(t *testing.T) {
	src := `
		function main(): number {
			putchar(72);  // H
			putchar(73);  // I
			putchar(10);  // \n
			return 0;
		}`
	out, code := compileAndRun(t, src)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if out != "HI\n" {
		t.Errorf("output = %q, want \"HI\\n\"", out)
	}
}

func TestBreakInWhile(t *testing.T) {
	src := `
		function main(): number {
			var i: number = 0;
			while (true) {
				if (i == 7) { break; }
				i = i + 1;
			}
			return i;
		}`
	_, code := compileAndRun(t, src)
	if code != 7 {
		t.Errorf("exit = %d, want 7", code)
	}
}

func TestContinueInForRunsStep(t *testing.T) {
	// Sum 5..9 (skip i < 5) = 5+6+7+8+9 = 35.
	// `continue` must still run the step, otherwise we'd loop forever.
	src := `
		function main(): number {
			var sum: number = 0;
			for (var i: number = 0; i < 10; i = i + 1) {
				if (i < 5) { continue; }
				sum = sum + i;
			}
			return sum;
		}`
	_, code := compileAndRun(t, src)
	if code != 35 {
		t.Errorf("exit = %d, want 35", code)
	}
}

func TestForLoop(t *testing.T) {
	src := `
		function main(): number {
			var sum: number = 0;
			for (var i: number = 1; i <= 10; i = i + 1) {
				sum = sum + i;
			}
			return sum;
		}`
	_, code := compileAndRun(t, src)
	if code != 55 {
		t.Errorf("exit = %d, want 55", code)
	}
}

func TestSixArgFunction(t *testing.T) {
	src := `
		function sum6(a: number, b: number, c: number,
		              d: number, e: number, f: number): number {
			return a + b + c + d + e + f;
		}
		function main(): number { return sum6(1, 2, 4, 8, 16, 32); }`
	_, code := compileAndRun(t, src)
	if code != 63 {
		t.Errorf("exit = %d, want 63", code)
	}
}

func TestModulo(t *testing.T) {
	src := `function main(): number { return 17 % 5; }`
	_, code := compileAndRun(t, src)
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}

func TestBitwiseAndOr(t *testing.T) {
	// (12 & 10) | 1 = 8 | 1 = 9
	src := `function main(): number { return (12 & 10) | 1; }`
	_, code := compileAndRun(t, src)
	if code != 9 {
		t.Errorf("exit = %d, want 9", code)
	}
}

func TestShifts(t *testing.T) {
	// (1 << 5) >> 2 = 32 >> 2 = 8
	src := `function main(): number { return (1 << 5) >> 2; }`
	_, code := compileAndRun(t, src)
	if code != 8 {
		t.Errorf("exit = %d, want 8", code)
	}
}

func TestStringConcat(t *testing.T) {
	src := `function main(): number {
		var s: string = "Hello, " + "world!";
		print(s);
		return 0;
	}`
	out, code := compileAndRun(t, src)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if out != "Hello, world!\n" {
		t.Errorf("output = %q, want %q", out, "Hello, world!\n")
	}
}

func TestStringConcatChained(t *testing.T) {
	// Three-part concat exercises a nested concat inside the helper —
	// `(a + b) + c` allocates twice.
	src := `function main(): number {
		print("foo" + "-" + "bar");
		return 0;
	}`
	out, _ := compileAndRun(t, src)
	if out != "foo-bar\n" {
		t.Errorf("output = %q", out)
	}
}

func TestStringPrint(t *testing.T) {
	src := `function main(): number {
		print("Hello, world!");
		return 0;
	}`
	out, code := compileAndRun(t, src)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	// `print` lowers to puts, which appends a newline.
	if out != "Hello, world!\n" {
		t.Errorf("output = %q, want %q", out, "Hello, world!\n")
	}
}

func TestStringEscapes(t *testing.T) {
	src := `function main(): number {
		print("tab:\there\nnext line");
		return 0;
	}`
	out, _ := compileAndRun(t, src)
	if out != "tab:\there\nnext line\n" {
		t.Errorf("output = %q", out)
	}
}

func TestArraySumAndMutation(t *testing.T) {
	src := `
		function main(): number {
			var a: number[] = [10, 20, 30, 40];
			a[2] = 100;
			return a[0] + a[1] + a[2] + a[3];
		}`
	_, code := compileAndRun(t, src)
	if code != 170 {
		t.Errorf("exit = %d, want 170 (10+20+100+40)", code)
	}
}
