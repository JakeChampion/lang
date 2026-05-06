package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/parser"
)

func checkSource(t *testing.T, src string) error {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = Check(prog)
	return err
}

func TestGoodPrograms(t *testing.T) {
	for _, src := range []string{
		`function f(): number { return 1 + 2; }`,
		`function f(n: number): number { return n * 2; }`,
		`function f(n: number): boolean { return n < 10; }`,
		`function main(): number { var x = 1; var y = x + 2; return y; }`,
		`function main(): number { var a: number[] = [1,2,3]; return a[0]; }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: unexpected error %v", src, err)
		}
	}
}

func TestTypeErrors(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{`function f(): number { return true; }`, "return type mismatch"},
		{`function f(): number { return 1 + true; }`, "requires number"},
		{`function f(): boolean { return 1; }`, "return type mismatch"},
		{`function f() { x; }`, "undefined identifier"},
		{`function f(n: number): number { if (n) { return 0; } return 1; }`, "if condition must be boolean"},
		{`function f() { var x: number = true; }`, "cannot assign boolean"},
	}
	for _, c := range cases {
		err := checkSource(t, c.src)
		if err == nil {
			t.Errorf("%q: expected error, got nil", c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: error %q does not contain %q", c.src, err.Error(), c.want)
		}
	}
}

// The checker should accumulate multiple errors and report them all in
// a single diag.Errors aggregate.
func TestMultipleErrorsAreReported(t *testing.T) {
	src := `function f(): number {
		return true;
		var x = unknownThing;
	}`
	err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected errors")
	}
	msg := err.Error()
	if !strings.Contains(msg, "return type mismatch") {
		t.Errorf("missing return mismatch: %s", msg)
	}
	if !strings.Contains(msg, "undefined identifier") {
		t.Errorf("missing undefined identifier: %s", msg)
	}
}

func TestBuiltinPutchar(t *testing.T) {
	if err := checkSource(t, `function f() { putchar(65); }`); err != nil {
		t.Errorf("putchar(65) should type-check: %v", err)
	}
	if err := checkSource(t, `function f() { putchar(true); }`); err == nil {
		t.Errorf("putchar(true) should fail")
	}
}

func TestFloatArithmeticIsFlagged(t *testing.T) {
	prog, err := parser.Parse(`function f(x: float, y: float): float { return x + y; }`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	bin := prog.Funcs[0].Body.Stmts[0].(*ast.Return).Value.(*ast.Binary)
	if !bin.IsFloat {
		t.Errorf("expected IsFloat = true on float + float")
	}
}

func TestFloatArithmeticTypechecks(t *testing.T) {
	for _, src := range []string{
		`function f(x: float): float { return x + 1.5; }`,
		`function f(x: float): float { return x * 2.0 - 0.5; }`,
		`function f(x: float, y: float): boolean { return x < y; }`,
		`function f(x: float): float { return -x; }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: unexpected error %v", src, err)
		}
	}
}

func TestFloatRejectsMixedArithmetic(t *testing.T) {
	cases := []string{
		`function f(x: float): float { return x + 1; }`,
		`function f(x: number): float { return x + 1.5; }`,
		`function f(x: float): float { return x % 1.0; }`, // % is integer-only
	}
	for _, src := range cases {
		if err := checkSource(t, src); err == nil {
			t.Errorf("%q: expected error", src)
		}
	}
}
