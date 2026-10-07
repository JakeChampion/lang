package checker

import (
	"strings"
	"testing"
)

// A payloadless variant is a value, not a constructor, so calling it is E038
// in every spelling, as it is in the self-host (#11922). A variant that does
// carry a payload keeps its arity error.
func TestUnitVariantCallIsE038(t *testing.T) {
	const decls = "enum C { R, B(i32) }\nenum G[T] { N, S(T) }\n"
	cases := []struct{ name, body, want string }{
		{"bare", "let c: C = R();", "calling non-function value of type C"},
		{"bare with an argument", "let c: C = R(1);", "calling non-function value of type C"},
		{"qualified", "let c = C.R();", "calling non-function value of type C"},
		{"qualified with an argument", "let c = C.R(1);", "calling non-function value of type C"},
		{"generic", "let g: G[i32] = G.N();", "calling non-function value of type G"},
		{"builtin", "let o: Option[i32] = Option.None();", "calling non-function value of type Option"},
		{"payload variant without its payload", "let c = C.B();", "variant B expects 1 argument(s), got 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSource(t, decls+"function main(): i32 { "+tc.body+" return 0; }")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}
