package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/parser"
)

// An arrow lambda with a BLOCK body infers its return type, and a body that
// only runs statements is void (#2673).
//
// A braced body is the lambda's own body, spliced in statement for statement:
// a trailing value written without a `;` is the returned value, and a body that
// yields nothing is void — the shape `(x) => { … }` has always had. When
// `=>` instead wrapped whatever followed it in a `return`, a block that always
// returns contributed its own `never` to inference (read as an E002 conflict
// against the inner return's type) and a block that yielded nothing at all was
// a value-less block in value position (E061). Both demanded of the arrow form
// what the anonymous `function` spelling it replaces never asked for, which is
// the friction #2673 is about: that spelling cannot be retired while the form
// meant to replace it is worse at the same job.
//
// `never` absorbing in unifyReturnType is the other half of the inference story
// and still holds: it is the bottom type, ast.NeverType's contract says it
// "unifies with any type", and assignability and if / match arm unification
// already read it that way.
func TestArrowLambdaBlockBodyInfersReturnType(t *testing.T) {
	const prelude = `function apply(f: (i32) => i32, v: i32): i32 { return f(v); }
function run(f: (i32) => void, v: i32): void { f(v); }
`
	for _, tc := range []struct{ name, src string }{
		// The shape that failed: a block body whose only statement returns.
		{"single return", prelude + `
function main(): i32 {
    var g = (x: i32) => { return x * 2; };
    return apply(g, 4);
}`},
		{"statements then return", prelude + `
function main(): i32 {
    var g = (x: i32) => { var y: i32 = x + 1; return y * 2; };
    return apply(g, 3);
}`},
		{"branch returns on every path", prelude + `
function main(): i32 {
    var g = (x: i32) => { if (x > 0) { return x; } return 0 - x; };
    return apply(g, 0 - 5);
}`},
		// Already worked, and must keep working: the tail-valued block has a
		// value of its own, so `never` never enters the unification.
		{"tail value, no return", prelude + `
function main(): i32 {
    var g = (x: i32) => { var y: i32 = x + 1; y * 2 };
    return apply(g, 3);
}`},
		// The plain expression body, which is the overwhelming majority of
		// arrow lambdas in the tree.
		{"expression body", prelude + `
function main(): i32 { return apply((x: i32) => x * 2, 4); }`},
		// An explicit return type was the workaround; it must still be
		// accepted rather than becoming redundant-and-rejected.
		{"explicit return type with a block body", prelude + `
function main(): i32 {
    var g = (x: i32): i32 => { var y: i32 = x + 1; return y * 2; };
    return apply(g, 3);
}`},
		// A body that only runs statements yields nothing, so the lambda is
		// void rather than a value-less block where a value is required.
		{"statement-only body is void", prelude + `
function main(): i32 {
    var seen: i32 = 0;
    var g = (x: i32) => { seen = seen + x; };
    run(g, 4);
    return seen - 4;
}`},
		{"empty body is void", prelude + `
function main(): i32 {
    var g = (x: i32) => {};
    run(g, 4);
    return 0;
}`},
		{"explicit void return type", prelude + `
function main(): i32 {
    var seen: i32 = 0;
    var g = (x: i32): void => { seen = seen + x; };
    run(g, 4);
    return seen - 4;
}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if _, err := Check(prog); err != nil {
				t.Errorf("should check, got: %v", err)
			}
		})
	}
}

// The absorption must not swallow a REAL conflict: two returns with
// incompatible value types are still E002, block body or not.
func TestArrowLambdaBlockBodyStillReportsRealConflicts(t *testing.T) {
	prog, err := parser.Parse(`function main(): i32 {
    var g = (x: i32) => { if (x > 0) { return x; } return "no"; };
    return 0;
}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = Check(prog)
	if err == nil {
		t.Fatal("expected E002 for genuinely conflicting return types")
	}
	if !strings.Contains(err.Error(), "conflicting return types") {
		t.Errorf("want a conflicting-return-types diagnostic, got: %v", err)
	}
}
