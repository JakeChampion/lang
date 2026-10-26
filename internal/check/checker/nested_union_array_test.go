package checker

import (
	"errors"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/diag"
)

func TestNestedUnionArrayDestinations(t *testing.T) {
	for _, decls := range []string{
		`struct A { a: i32 } struct B { b: i32 } type U = A | B;`,
		`enum U { A { a: i32 }, B { b: i32 } }`,
	} {
		for name, source := range map[string]string{
			"nested":   `function f(a: A): U[][] { return [[a]]; }`,
			"option":   `function f(a: A): Option[U[]] { return Some([a]); }`,
			"tuple":    `function f(a: A): (U[], i32) { return ([a], 1); }`,
			"value-if": `function f(a: A, c: boolean): i32 { let xs: U[] = if (c) { [a] } else { [] }; return xs.len(); }`,
			"const":    `const VALUES: U[] = [A { a: 1 }];`,
		} {
			if strings.HasPrefix(decls, "enum") {
				source = strings.ReplaceAll(source, "a: A", "a: U")
				if name == "const" {
					source = `const VALUES: U[] = [A(1)];`
				}
			}
			t.Run(name+"/"+decls, func(t *testing.T) {
				if err := checkSource(t, decls+source+`function main(): i32 { return 0; }`); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestConstUnionArrayReportsTheElement(t *testing.T) {
	err := checkSource(t, `struct A { a: i32 } struct B { b: i32 } struct P { x: i32 }
type U = A | B;
const VALUES: U[] = [P { x: 1 }];
function main(): i32 { return 0; }`)
	var diagnostics diag.Errors
	if !errors.As(err, &diagnostics) || len(diagnostics) != 1 {
		t.Fatalf("want one E034, got %v", err)
	}
	var check *Error
	if !errors.As(diagnostics[0], &check) || check.Code() != "E034" {
		t.Fatalf("want E034, got %v", diagnostics[0])
	}
}
