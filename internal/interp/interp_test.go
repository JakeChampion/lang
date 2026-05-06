package interp

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/parser"
)

func evalProgramValue(t *testing.T, src string) Value {
	t.Helper()
	v, _ := evalProgram(t, src)
	return v
}

func evalProgram(t *testing.T, src string) (Value, *Interp) {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	i := New()
	for _, fn := range prog.Funcs {
		i.Register(fn)
	}
	if _, ok := i.Funcs["main"]; !ok {
		t.Fatalf("program has no main")
	}
	v, err := i.CallByName("main", nil)
	if err != nil {
		t.Fatalf("call main: %v", err)
	}
	return v, i
}

func TestSimpleArithmetic(t *testing.T) {
	v, _ := evalProgram(t, `function main(): number { return 1 + 2 * 3; }`)
	if n, ok := v.(Number); !ok || n != 7 {
		t.Errorf("got %v, want 7", v)
	}
}

func TestRecursionFactorial(t *testing.T) {
	v, _ := evalProgram(t, `
		function fact(n: number): number {
			if (n == 0) { return 1; }
			return n * fact(n - 1);
		}
		function main(): number { return fact(6); }`)
	if n, ok := v.(Number); !ok || n != 720 {
		t.Errorf("got %v, want 720", v)
	}
}

func TestForLoopWithBreakContinue(t *testing.T) {
	v, _ := evalProgram(t, `
		function main(): number {
			var sum: number = 0;
			for (var i: number = 0; i < 10; i = i + 1) {
				if (i == 3) { continue; }
				if (i == 7) { break; }
				sum = sum + i;
			}
			return sum;
		}`)
	// 0+1+2 (skip 3) +4+5+6 (break before 7) = 18
	if n, ok := v.(Number); !ok || n != 18 {
		t.Errorf("got %v, want 18", v)
	}
}

func TestArrayIndexAndAssign(t *testing.T) {
	v, _ := evalProgram(t, `
		function main(): number {
			var a: number[] = [10, 20, 30];
			a[1] = 99;
			return a[0] + a[1] + a[2];
		}`)
	if n, ok := v.(Number); !ok || n != 139 {
		t.Errorf("got %v, want 139", v)
	}
}

func TestPrintBuiltin(t *testing.T) {
	prog, err := parser.Parse(`function main(): void { print("hello"); }`)
	if err != nil {
		t.Fatal(err)
	}
	i := New()
	var buf bytes.Buffer
	i.Stdout = &buf
	for _, fn := range prog.Funcs {
		i.Register(fn)
	}
	if _, err := i.CallByName("main", nil); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "hello\n" {
		t.Errorf("stdout = %q, want \"hello\\n\"", got)
	}
}

func TestIndirectCallViaLocal(t *testing.T) {
	v, _ := evalProgram(t, `
		function dbl(x: number): number { return x * 2; }
		function main(): number {
			var f = dbl;
			return f(7);
		}`)
	if n, ok := v.(Number); !ok || n != 14 {
		t.Errorf("got %v, want 14", v)
	}
}

func TestREPLEvaluatesExpressions(t *testing.T) {
	in := strings.NewReader("1 + 2 * 3\n\"hi\"\nvar x = 5;\nx + 10\n")
	var out bytes.Buffer
	if err := REPL(in, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"7", "hi", "15"} {
		if !strings.Contains(got, want) {
			t.Errorf("REPL output missing %q\n--- got ---\n%s", want, got)
		}
	}
}

func TestREPLDeclaresThenCallsFunction(t *testing.T) {
	in := strings.NewReader("function dbl(x: number): number { return x * 2; }\ndbl(21)\n")
	var out bytes.Buffer
	if err := REPL(in, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "42") {
		t.Errorf("expected 42 in REPL output, got:\n%s", out.String())
	}
}

func TestInterpLenOfString(t *testing.T) {
	if got := evalProgramValue(t, `function main(): number { return len("hello"); }`); got != Number(5) {
		t.Errorf("got %v, want 5", got)
	}
}

func TestInterpLenOfArray(t *testing.T) {
	if got := evalProgramValue(t, `function main(): number {
		var a: number[] = [1, 2, 3, 4];
		return len(a);
	}`); got != Number(4) {
		t.Errorf("got %v, want 4", got)
	}
}

func TestInterpStringIndex(t *testing.T) {
	if got := evalProgramValue(t, `function main(): number {
		var s: string = "ABC";
		return s[1];
	}`); got != Number(int64('B')) {
		t.Errorf("got %v, want %d", got, 'B')
	}
}

func TestInterpStringEquality(t *testing.T) {
	src := `function main(): boolean {
		var a: string = "hello";
		var b: string = "hello";
		return a == b;
	}`
	if got := evalProgramValue(t, src); got != Bool(true) {
		t.Errorf("got %v, want true", got)
	}
}
