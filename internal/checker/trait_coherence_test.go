package checker

import (
	"strings"
	"testing"
)

// Trait coherence: enum-returning trait methods, and `own`-aware conformance.

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

// An enum-returning trait method must not be rejected with a spurious E021
// ("expected (E)=>E, got (E)=>E"). The parser models the bare enum name
// in the trait signature as a StructType while the impl method carries an
// EnumType. normalizeEnumKinds reconciles the kinds. Pins both the borrowed and
// the consuming (`own self`) forms — the latter is the headline FBIP map.
func TestTraitEnumReturnConforms(t *testing.T) {
	wantOK(t, "borrowed enum-return method", `enum E { A(i32), B }
trait T { function f(self: Self): E; }
impl T for E { function f(self: Self): E { match (self) { A(x) => { return A(x + 1); }, B => { return B; } } } }`)

	wantOK(t, "consuming enum-return method (own self)", `enum List { Cons(i32, List), Nil }
trait Mapper { function inc(own self: Self): List; }
impl Mapper for List { function inc(own self: Self): List { match (self) { Cons(h, t) => { return Cons(h + 1, t.inc()); }, Nil => { return Nil; } } } }`)

	// An enum named in a NESTED position (a tuple result) also reconciles.
	wantOK(t, "enum nested in tuple result", `enum E { A(i32), B }
trait T { function f(self: Self): (E, i32); }
impl T for E { function f(self: Self): (E, i32) { return (B, 0); } }`)
}

// `own` is part of the trait contract: a generic call `x.m()` through a
// `T: Trait` bound transfers / borrows the receiver based on the TRAIT's
// declared ownership, so the impl must agree or the call would double-free
// (impl consumes where the trait borrows) or move-after-use (vice versa).
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
