package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/parser"
)

// An arrow lambda with a BLOCK body infers its return type (#2673).
//
// `(x) => { return e; }` parses to a Lambda whose single body statement is
// `return <block expression>`, because the arrow desugar wraps whatever it
// parsed. When that block always returns it has no trailing value, so its own
// type is `never` — and inference then saw two contributions, e's type from
// the inner return and `never` from the outer one, and called them a conflict.
// The result was E002 demanding an explicit return type on a form that
// `function (x) { return e; }` infers without complaint, which is the friction
// #2673 is about: the anonymous `function` expression cannot be retired while
// the arrow form it should be replaced by is worse at the same job.
//
// `never` is the bottom type and ast.NeverType's contract says it "unifies
// with any type"; assignability and if / match arm unification already read it
// that way. unifyReturnType now does too.
func TestArrowLambdaBlockBodyInfersReturnType(t *testing.T) {
	const prelude = `function apply(f: (i32) => i32, v: i32): i32 { return f(v); }
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
