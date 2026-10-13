package checker

import (
	"errors"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/diag"
)

// Inside trait B's default body, `self.scale()` is B's `scale` even when
// trait A gives the implementing type one too (#12282): the receiver is
// known there only as B's. A method the program wrote in an impl sees both
// and stays E074.
func TestTraitDefaultOwnMethodOutranksAnotherTraits(t *testing.T) {
	traits := `trait A { function scale(self: Self): i32; }
trait B { function scale(self: Self): i32; function twice(self: Self): i32 { return self.scale() * 2; } }
struct P { v: i32 }
impl A for P { function scale(self: Self): i32 { return self.v; } }
`
	good := traits + `impl B for P { function scale(self: Self): i32 { return self.v + 1; } }
function main(): i32 { let p: P = P { v: 3 }; return p.twice(); }`
	if err := checkSource(t, good); err != nil {
		t.Errorf("a default's own method should resolve: %v", err)
	}
	bad := traits + `impl B for P {
    function scale(self: Self): i32 { return self.v + 1; }
    function twice(self: Self): i32 { return self.scale() * 2; }
}
function main(): i32 { let p: P = P { v: 3 }; return p.twice(); }`
	err := checkSource(t, bad)
	var errs diag.Errors
	if !errors.As(err, &errs) || len(errs) != 1 {
		t.Fatalf("a written impl method sees both traits: want one E074, got %v", err)
	}
	var ce *Error
	if !errors.As(errs[0], &ce) || ce.Code() != "E074" {
		t.Fatalf("want E074, got %v", errs[0])
	}
}

// `Self.f()` inside an impl method or a trait default is the associated
// call on the impl type (#12296); `Self` named as a value anywhere else is
// still E001.
func TestSelfAssociatedCallInImplAndDefault(t *testing.T) {
	src := `trait Mk {
    function zero(): Self;
    function one(self: Self): i32;
    function base(self: Self): i32 { let z: Self = Self.zero(); return z.v; }
}
struct Q { v: i32 }
impl Mk for Q {
    function zero(): Self { return Q { v: 5 }; }
    function one(self: Self): i32 { return Self.zero().v + 1; }
}
function main(): i32 { let q: Q = Q { v: 9 }; return q.one() + q.base(); }`
	if err := checkSource(t, src); err != nil {
		t.Errorf("Self.f() in an impl and a default should resolve: %v", err)
	}
	bare := `struct Q { v: i32 }
function main(): i32 { let s = Self; return 0; }`
	err := checkSource(t, bare)
	if err == nil || !strings.Contains(err.Error(), `cannot find "Self"`) {
		t.Errorf("Self outside an impl: want E001, got %v", err)
	}
}
