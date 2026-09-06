package parser

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/diag"
)

// firstCode returns the stable code on the first diagnostic in err, which the
// parser returns as a diag.Errors of everything it found in one pass.
func firstCode(err error) string {
	if es, ok := err.(diag.Errors); ok && len(es) > 0 {
		err = es[0]
	}
	if pe, ok := err.(*Error); ok {
		return pe.ErrCode
	}
	return ""
}

// `function` introduces a named declaration and nothing else (#2673).
//
// The anonymous `(…) => { … }` expression and the arrow lambda built the
// same node, so the language carried two spellings for one construct. The arrow
// form is the survivor: it says at the opening token which form is being read,
// where the `function` spelling looked like a declaration until the parser
// reached the name slot and found none.
//
// The diagnostic names the replacement. A bare "expected a name here" would be
// true and useless — the reader wrote a lambda on purpose.
func TestAnonymousFunctionExpressionIsRetired(t *testing.T) {
	for _, src := range []string{
		`function main(): i32 { var f = function (x: i32): i32 { return x + 1; }; return f(41); }`,
		`function g(f: (i32) => i32): i32 { return f(1); }
function main(): i32 { return g(function (x: i32): i32 { return x + 1; }); }`,
		`function main(): i32 { return (function (): i32 { return 7; })(); }`,
		`function main(): void { var f = function (): void {}; f(); }`,
	} {
		_, err := Parse(src)
		if err == nil {
			t.Errorf("the anonymous `function` expression still parses:\n%s", src)
			continue
		}
		// The code rides on the Error, not in its message — the CLI renders
		// it as `error[P006]` and `fern -explain P006` answers from it.
		if got := firstCode(err); got != "P006" {
			t.Errorf("want code P006, got %q: %v", got, err)
		}
		if !strings.Contains(err.Error(), "=>") {
			t.Errorf("the diagnostic should name the arrow form that replaced it, got: %v", err)
		}
	}
}

// The named declaration — the form `function` still introduces — is unaffected,
// at top level and nested inside another function's body.
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
