package parser

import (
	"strings"
	"testing"
)

// A LOCAL (nested) function must have a body. The top-level path has always
// refused a body-less declaration — "only @import functions may omit a body" —
// but parseLocalFunction never applied the rule, so `function A();` nested in a
// body produced a FuncDecl with a nil Body and every consumer then assumed it
// was non-nil.
//
// `function A(){function A();}` (FuzzParse's minimised input, #7694) crashed
// THREE separate nil dereferences: the parser's own for-each desugar, the
// checker's stream-for-each lowering, and checkLocalFunc. Guarding each would
// have been three patches for one cause; the rule belongs where the top-level
// path already applies it. `@import` is a top-level attribute, so the local
// rule needs no exemption.
//
// The crash itself is pinned by the corpus entry at
// testdata/fuzz/FuzzParse/8ccfbc87cb1c942b; these pin the DIAGNOSTIC, which the
// corpus entry cannot (it only asserts the absence of a panic).
func TestLocalFunctionMustHaveBody(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"fuzz-minimised", `function A(){function A();}`},
		{"typed-return", `function outer(): i32 { function inner(): i32; return 0; }`},
		{"with-params", `function outer(): i32 { function inner(a: i32, b: i32): i32; return 0; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.src)
			if err == nil {
				t.Fatalf("a body-less local function must be refused, got no error")
			}
			if !strings.Contains(err.Error(), "has no body") {
				t.Errorf("error should name the missing body, got: %v", err)
			}
		})
	}
}

// The shapes either side of the rule must be unaffected: a local function WITH
// a body still parses, and a top-level `@import` may still omit one.
func TestLocalFunctionBodyRuleControls(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"local-with-body", `function outer(): i32 { function inner(): i32 { return 1; } return inner(); }`},
		{"local-nested-twice", `function a(): i32 { function b(): i32 { function c(): i32 { return 1; } return c(); } return b(); }`},
		{"plain-top-level", `function f(): i32 { return 1; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.src); err != nil {
				t.Errorf("should still parse: %v", err)
			}
		})
	}
}

// An IMPL method must have a body too — the third context of the same defect
// (#7705). A body-less one passed `-check` clean and then crashed the native
// build (ir.computeFreeEligible walking a typed-nil *ast.Block) and the
// interpreter, with no source position.
//
// A trait's signature-only methods are the shape that makes this look
// plausible, and they stay legal: parseTraitDecl does not share parseFunction,
// so the rule cannot reach them. The controls below pin both directions.
func TestImplMethodMustHaveBody(t *testing.T) {
	const prelude = "struct P { x: i32 }\ntrait T { function get(self: Self): i32; }\n"
	_, err := Parse(prelude + `impl T for P { function get(self: Self): i32; }`)
	if err == nil {
		t.Fatalf("a body-less impl method must be refused, got no error")
	}
	if !strings.Contains(err.Error(), "has no body") {
		t.Errorf("error should name the missing body, got: %v", err)
	}
}

func TestImplMethodBodyRuleControls(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"impl-with-body", "struct P { x: i32 }\ntrait T { function get(self: Self): i32; }\n" +
			`impl T for P { function get(self: Self): i32 { return self.x; } }`},
		{"assoc-fn-impl", "struct P { x: i32 }\ntrait Z { function zero(): Self; }\n" +
			`impl Z for P { function zero(): Self { return P { x: 0 }; } }`},
		// The trait's own signature-only method: body-less and legal.
		{"trait-signature-only", `trait T { function get(self: Self): i32; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.src); err != nil {
				t.Errorf("should still parse: %v", err)
			}
		})
	}
}
