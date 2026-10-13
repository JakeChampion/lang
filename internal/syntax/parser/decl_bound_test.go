package parser

import (
	"strings"
	"testing"
)

// A bound on a struct's, enum's or union alias's type parameter is P001
// naming the impl it belongs on (#12282, #12297): the alias used to drop the
// bound without a word, the other two reported the `:` as an unexpected
// token.
func TestDeclTypeParamBoundIsP001(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"struct", "struct W[T: B] { v: T }", "struct W: a bound on type parameter T belongs on an impl"},
		{"enum", "enum E[T: B] { A(T), N }", "enum E: a bound on type parameter T belongs on an impl"},
		{"alias", "struct L[T] { v: T }\nstruct Q { n: i32 }\ntype V[T: B] = L[T] | Q;", "type V: a bound on type parameter T belongs on an impl"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(c.src)
			if err == nil {
				t.Fatalf("accepted a bound on a declaration's type parameter:\n%s", c.src)
			}
			if code := firstCode(err); code != "P001" {
				t.Errorf("want P001, got %q: %v", code, err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("want %q, got %v", c.want, err)
			}
		})
	}
	for _, src := range []string{
		"struct W[T] { v: T }",
		"enum E[T, U] { A(T), B(U) }",
		"struct L[T] { v: T }\nstruct Q { n: i32 }\ntype V[T] = L[T] | Q;",
	} {
		if _, err := Parse(src); err != nil {
			t.Errorf("refused a declaration without a bound: %v\n%s", err, src)
		}
	}
}
