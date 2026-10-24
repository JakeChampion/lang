package parser

import (
	"strings"
	"testing"
)

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
		{"trait-signature-only", `trait T { function get(self: Self): i32; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.src); err != nil {
				t.Errorf("should still parse: %v", err)
			}
		})
	}
}
