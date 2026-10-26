package checker

import (
	"errors"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/diag"
)

func TestElementCheckedDestinationReportsOnlyTheElement(t *testing.T) {
	const decls = "struct P { x: i32 }\nstruct A { a: i32 }\nstruct B { b: i32 }\ntype U = A | B;\nenum E { X, Y(i32) }\n"
	for _, src := range []string{
		"function f(p: P): U[] { return [p]; }",
		"function f(): U[] { return [5]; }",
		"function f(p: P): E[] { return [p]; }",
		"function f(p: P): i32 { let u: U[] = [p]; return 0; }",
		"function g(us: U[]): i32 { return 0; }\nfunction f(p: P): i32 { return g([p]); }",
		"struct H { us: U[] }\nfunction f(p: P): H { return H { us: [p] }; }",
	} {
		err := checkSource(t, decls+src+"\nfunction main(): i32 { return 0; }\n")
		var diagnostics diag.Errors
		if !errors.As(err, &diagnostics) || len(diagnostics) != 1 {
			t.Errorf("%s: want one E034, got %v", src, err)
			continue
		}
		var check *Error
		if !errors.As(diagnostics[0], &check) || check.Code() != "E034" {
			t.Errorf("%s: want E034, got %v", src, diagnostics[0])
		}
	}
}

func TestStrArrayDestinationTypesTheLiteral(t *testing.T) {
	const pre = `function main(): i32 { let owned: string = "ab" + "cd"; let s: str = slice_unchecked(owned, 1, 3); `
	for _, lit := range []string{`["x", s]`, `[s, "x"]`, `[s, s]`, `["x", "y"]`} {
		if err := checkSource(t, pre+"let xs: str[] = "+lit+"; return xs.len(); }"); err != nil {
			t.Errorf("%s into str[]: %v", lit, err)
		}
	}
	for _, src := range []string{
		pre + `let xs: str[] = ["x", 5]; return xs.len(); }`,
		pre + `let xs = ["x", s]; return xs.len(); }`,
	} {
		if err := checkSource(t, src); err == nil || !strings.Contains(err.Error(), "array element:") {
			t.Errorf("want E034 for %s, got %v", src, err)
		}
	}
}
