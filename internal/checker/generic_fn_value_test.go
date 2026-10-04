package checker

import (
	"strings"
	"testing"
)

const genericFnValueDecls = `trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Sq): i32 { return self.s * self.s; } }
function measure[T: Shape](s: T): i32 { return s.area(); }
function skip[T](s: i32, a: T): T { return a; }
`

// A generic function named as a value takes its type arguments from the
// function type the value is wanted at: a parameter, a let, a field, a return,
// a generic callee's parameter once its other arguments pin it, and an
// enclosing generic's own parameter.
func TestGenericFnValueInstantiatedFromExpectedType(t *testing.T) {
	cases := map[string]string{
		"argument": `function apply(a: i32, f: (i32, i32) => i32): i32 { return f(1, a); }
function main(): i32 { return apply(7, skip); }`,
		"let":    `function main(): i32 { let g: (Sq) => i32 = measure; return g(Sq { s: 2 }); }`,
		"field":  `struct H { f: (Sq) => i32 }` + "\n" + `function main(): i32 { let h: H = H { f: measure }; return h.f(Sq { s: 2 }); }`,
		"return": `function pick(): (Sq) => i32 { return measure; }` + "\n" + `function main(): i32 { let p: (Sq) => i32 = pick(); return p(Sq { s: 2 }); }`,
		"generic callee": `function twice[A](f: (A) => i32, v: A): i32 { return f(v) * 2; }
function main(): i32 { return twice(measure, Sq { s: 1 }); }`,
		"generic caller": `function inner[T](acc: T, f: (i32, T) => T): T { return f(1, acc); }
function outer[T](acc: T): T { return inner(acc, skip); }
function main(): i32 { return outer(7); }`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if err := checkSource(t, genericFnValueDecls+body); err != nil {
				t.Errorf("should typecheck: %v", err)
			}
		})
	}
}

// A bound the value's type arguments meet only once a generic callee's other
// arguments pin them is monomorph's to check, as a call's is; TestGenericFnValue
// covers that at the CLI level.
func TestGenericFnValueErrors(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"bound unmet", `function apply_i(f: (i32) => i32, v: i32): i32 { return f(v); }
function main(): i32 { return apply_i(measure, 3); }`, "type argument T = i32 does not implement trait Shape required by measure"},
		{"arity differs", `function apply2(f: (Sq, Sq) => i32): i32 { return f(Sq { s: 1 }, Sq { s: 2 }); }
function main(): i32 { return apply2(measure); }`, "cannot be used as a value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSource(t, genericFnValueDecls+tc.src)
			if err == nil {
				t.Fatalf("want an error containing %q, got none", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("want an error containing %q, got:\n%v", tc.want, err)
			}
		})
	}
}
