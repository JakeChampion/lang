package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestArrowLambdaBlockBodyInfersReturnType(t *testing.T) {
	const prelude = `function apply(f: (i32) => i32, v: i32): i32 { return f(v); }
function run(f: (i32) => void, v: i32): void { f(v); }
`
	for _, tc := range []struct{ name, src string }{
		{"single return", prelude + `
function main(): i32 {
    let g = (x: i32) => { return x * 2; };
    return apply(g, 4);
}`},
		{"statements then return", prelude + `
function main(): i32 {
    let g = (x: i32) => { let y: i32 = x + 1; return y * 2; };
    return apply(g, 3);
}`},
		{"branch returns on every path", prelude + `
function main(): i32 {
    let g = (x: i32) => { if (x > 0) { return x; } return 0 - x; };
    return apply(g, 0 - 5);
}`},
		{"tail value, no return", prelude + `
function main(): i32 {
    let g = (x: i32) => { let y: i32 = x + 1; y * 2 };
    return apply(g, 3);
}`},
		{"expression body", prelude + `
function main(): i32 { return apply((x: i32) => x * 2, 4); }`},
		{"explicit return type with a block body", prelude + `
function main(): i32 {
    let g = (x: i32): i32 => { let y: i32 = x + 1; return y * 2; };
    return apply(g, 3);
}`},
		{"statement-only body is void", prelude + `
function main(): i32 {
    let seen: i32 = 0;
    let g = (x: i32) => { seen = seen + x; };
    run(g, 4);
    return seen - 4;
}`},
		{"empty body is void", prelude + `
function main(): i32 {
    let g = (x: i32) => {};
    run(g, 4);
    return 0;
}`},
		{"explicit void return type", prelude + `
function main(): i32 {
    let seen: i32 = 0;
    let g = (x: i32): void => { seen = seen + x; };
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

func TestArrowLambdaBlockBodyStillReportsRealConflicts(t *testing.T) {
	prog, err := parser.Parse(`function main(): i32 {
    let g = (x: i32) => { if (x > 0) { return x; } return "no"; };
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
