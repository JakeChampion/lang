package wasm

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

func compileToWAT(t *testing.T, src string) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	wat, err := Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return wat
}

func mustContain(t *testing.T, wat, needle string) {
	t.Helper()
	if !strings.Contains(wat, needle) {
		t.Errorf("expected output to contain %q\n--- output ---\n%s", needle, wat)
	}
}

func TestSimpleFunction(t *testing.T) {
	wat := compileToWAT(t, `function main(): number { return 42; }`)
	mustContain(t, wat, "(module")
	mustContain(t, wat, `(func $main (result i32)`)
	mustContain(t, wat, "i32.const 42")
	mustContain(t, wat, `(export "main" (func $main))`)
}

func TestArithmeticOps(t *testing.T) {
	wat := compileToWAT(t, `function main(): number { return 7 % 3 + (12 & 10); }`)
	mustContain(t, wat, "i32.rem_s")
	mustContain(t, wat, "i32.and")
	mustContain(t, wat, "i32.add")
}

func TestComparisonReturnsI32(t *testing.T) {
	wat := compileToWAT(t, `function f(): boolean { return 1 < 2; }`)
	mustContain(t, wat, "i32.lt_s")
	mustContain(t, wat, "(result i32)")
}

func TestShortCircuitAnd(t *testing.T) {
	wat := compileToWAT(t, `function f(a: boolean, b: boolean): boolean { return a && b; }`)
	// `&&` lowers to a conditional that yields i32 on both arms.
	mustContain(t, wat, "if (result i32)")
}

func TestIfElse(t *testing.T) {
	wat := compileToWAT(t, `function f(n: number): number {
		if (n == 0) { return 1; } else { return 2; }
	}`)
	mustContain(t, wat, "if")
	mustContain(t, wat, "else")
	mustContain(t, wat, "end")
}

func TestWhileBreakContinue(t *testing.T) {
	wat := compileToWAT(t, `function f(): number {
		var i = 0;
		while (true) {
			if (i == 5) { break; }
			i = i + 1;
		}
		return i;
	}`)
	mustContain(t, wat, "block $break")
	mustContain(t, wat, "loop $loop")
	mustContain(t, wat, "br $break")
}

func TestForLoopWithStep(t *testing.T) {
	wat := compileToWAT(t, `function f(): number {
		var sum = 0;
		for (var i = 0; i < 10; i = i + 1) {
			if (i < 5) { continue; }
			sum = sum + i;
		}
		return sum;
	}`)
	// continue must `br` out of the inner block (the $cont label) so
	// the step still runs.
	mustContain(t, wat, "block $cont")
	mustContain(t, wat, "br $cont")
}

func TestRecursionDirectCall(t *testing.T) {
	wat := compileToWAT(t, `function fact(n: number): number {
		if (n == 0) { return 1; }
		return n * fact(n - 1);
	}`)
	mustContain(t, wat, "call $fact")
}

func TestStringsRejectedWithClearError(t *testing.T) {
	prog, err := parser.Parse(`function main(): void { print("hi"); }`)
	if err != nil {
		t.Fatal(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Emit(prog, info)
	if err == nil {
		t.Fatal("expected error for unsupported feature")
	}
}
