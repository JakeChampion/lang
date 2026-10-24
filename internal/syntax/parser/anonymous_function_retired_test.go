package parser

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/diag"
)

func firstCode(err error) string {
	if es, ok := err.(diag.Errors); ok && len(es) > 0 {
		err = es[0]
	}
	if pe, ok := err.(*Error); ok {
		return pe.ErrCode
	}
	return ""
}

func TestAnonymousFunctionExpressionIsRetired(t *testing.T) {
	for _, src := range []string{
		`function main(): i32 { let f = function (x: i32): i32 { return x + 1; }; return f(41); }`,
		`function g(f: (i32) => i32): i32 { return f(1); }
function main(): i32 { return g(function (x: i32): i32 { return x + 1; }); }`,
		`function main(): i32 { return (function (): i32 { return 7; })(); }`,
		`function main(): void { let f = function (): void {}; f(); }`,
	} {
		_, err := Parse(src)
		if err == nil {
			t.Errorf("the anonymous `function` expression still parses:\n%s", src)
			continue
		}
		if got := firstCode(err); got != "P006" {
			t.Errorf("want code P006, got %q: %v", got, err)
		}
		if !strings.Contains(err.Error(), "=>") {
			t.Errorf("the diagnostic should name the arrow form that replaced it, got: %v", err)
		}
	}
}

func TestNamedFunctionDeclarationStillParses(t *testing.T) {
	for _, src := range []string{
		`function main(): i32 { return 0; }`,
		`function main(): i32 { function inner(x: i32): i32 { return x + 1; } return inner(41); }`,
		`struct P { x: i32 }
function (p: P) get(): i32 { return p.x; }
function main(): i32 { return P { x: 42 }.get(); }`,
	} {
		if _, err := Parse(src); err != nil {
			t.Errorf("a named declaration no longer parses: %v\n%s", err, src)
		}
	}
}
