package checker

import (
	"strings"
	"testing"
)

func wantE021Contains(t *testing.T, name, src, substr string) {
	t.Helper()
	err := checkSource(t, src)
	if err == nil {
		t.Fatalf("%s: expected a conformance error, got none", name)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Errorf("%s: expected error containing %q, got: %v", name, substr, err)
	}
}

func TestTraitEnumReturnConforms(t *testing.T) {
	wantOK(t, "borrowed enum-return method", `enum E { A(i32), B }
trait T { function f(self: Self): E; }
impl T for E { function f(self: Self): E { match (self) { A(x) => { return A(x + 1); }, B => { return B; } } } }`)

	wantOK(t, "consuming enum-return method (own self)", `enum List { Cons(i32, List), Nil }
trait Mapper { function inc(own self: Self): List; }
impl Mapper for List { function inc(own self: Self): List { match (self) { Cons(h, t) => { return Cons(h + 1, t.inc()); }, Nil => { return Nil; } } } }`)

	wantOK(t, "enum nested in tuple result", `enum E { A(i32), B }
trait T { function f(self: Self): (E, i32); }
impl T for E { function f(self: Self): (E, i32) { return (B, 0); } }`)
}

func TestTraitOwnershipMustMatch(t *testing.T) {
	wantOK(t, "own == own", `enum L { C(i32, L), N }
trait M { function f(own self: Self): L; }
impl M for L { function f(own self: Self): L { match (self) { C(h, t) => { return C(h, t.f()); }, N => { return N; } } } }`)

	wantE021Contains(t, "trait borrows, impl consumes", `enum L { C(i32, L), N }
trait M { function f(self: Self): L; }
impl M for L { function f(own self: Self): L { match (self) { C(h, t) => { return C(h, t.f()); }, N => { return N; } } } }`, "must not take `own`")

	wantE021Contains(t, "trait consumes, impl borrows", `enum L { C(i32, L), N }
trait M { function f(own self: Self): L; }
impl M for L { function f(self: Self): L { match (self) { C(h, t) => { return C(h, t.f()); }, N => { return N; } } } }`, "must take `own`")
}
