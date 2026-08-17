package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/parser"
)

// A lambda's declared parameter / return types go through the same
// `resolveType` pre-pass as every other declaration, so a name the parser
// could only guess at — a union alias, an enum, a resource — reaches the
// type-equality check in its resolved form.
//
// Before #6996 the pre-pass walked statements only and the check-time
// fallback fired solely inside a generic function, so a lambda outside one
// kept the parser's provisional `StructType{U}` while the signature it had
// to match had already become `EnumType{U}`. `ast.Equal` said no and both
// sides printed as `U`: "expected (U) => i32, got (U) => i32".
func TestLambdaDeclaredTypesResolve(t *testing.T) {
	const decls = `struct A { v: i32 }
struct B { w: i32 }
type U = A | B;
enum E { X, Y }
function applyU(u: U, f: (U) => i32): i32 { return f(u); }
function applyA(a: A, f: (A) => i32): i32 { return f(a); }
function applyE(e: E, f: (E) => i32): i32 { return f(e); }
function takesU(u: U): i32 { return 1; }
`
	for _, tc := range []struct{ name, src string }{
		// The three rows that already passed before #6996 — the
		// regression guard on the fix.
		{"struct-annotated lambda param", decls + `
function main(): i32 {
    return applyA(A { v: 7 }, (x: A) => x.v);
}`},
		{"union-annotated lambda inside a generic function", decls + `
function wrap[T](t: T): i32 { return applyU(A { v: 7 }, (x: U) => 1); }
function main(): i32 { return wrap(0i32); }`},
		{"top-level function used as a function value", decls + `
function main(): i32 {
    var h: (U) => i32 = takesU;
    return h(A { v: 7 });
}`},

		// The rows that failed.
		{"union-annotated lambda param in a non-generic function", decls + `
function main(): i32 {
    return applyU(A { v: 7 }, (x: U) => 1);
}`},
		{"union-annotated lambda in a var initialiser", decls + `
function main(): i32 {
    var g: (U) => i32 = ((v: U) => takesU(v));
    return g(A { v: 7 });
}`},

		// The same defect reached every nominal name the parser cannot
		// classify, not just union aliases.
		{"enum-annotated lambda param", decls + `
function main(): i32 { return applyE(E.X, (x: E) => 1); }`},

		// Nesting: the pre-pass reaches a lambda at any expression
		// depth, where the check-time fallback saw only the outermost
		// one (an inner lambda's `c.current` is the outer lambda's
		// synthetic decl, which carries no type parameters).
		{"lambda nested inside a lambda", decls + `
function main(): i32 {
    var outer: (i32) => i32 = ((n: i32) => applyU(A { v: n }, (x: U) => 1));
    return outer(7);
}`},
		{"lambda inside a nested named function", decls + `
function main(): i32 {
    function inner(): i32 { return applyU(A { v: 7 }, (x: U) => 1); }
    return inner();
}`},

		// A `var` annotation nested in an expression-position block is
		// the other half of the same statement-only walk.
		{"var annotation inside a value block", decls + `
function main(): i32 {
    var t: i32 = {
        var h: (U) => i32 = takesU;
        h(A { v: 7 })
    };
    return t;
}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if _, err := Check(prog); err != nil {
				t.Errorf("checker rejected a valid program: %v", err)
			}
		})
	}
}

// A type that really is unknown still has to be reported. The pre-pass
// rewrites names it recognises; it must not launder an undeclared one into
// something that silently type-checks.
func TestLambdaParamUnknownTypeStillReported(t *testing.T) {
	const src = `function main(): i32 {
    var f: (i32) => i32 = ((x: Wibble) => 1);
    return f(1);
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = Check(prog)
	if err == nil {
		t.Fatalf("checker accepted a lambda parameter of an undeclared type")
	}
	if !strings.Contains(err.Error(), "Wibble") {
		t.Errorf("diagnostic does not name the unknown type: %v", err)
	}
}
