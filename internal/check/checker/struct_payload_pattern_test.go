package checker

import (
	"strings"
	"testing"
)

// A payload slot may hold a STRUCT pattern. `A(P { x })` and `A(Ok(n))` are
// the same spelling, so only the slot's TYPE separates them: with a struct
// there, the position projects fields instead of testing a tag.
func TestStructPatternAtPayloadSlot(t *testing.T) {
	src := `struct Pt { x: i32, y: i32, z: i32 }
		enum H { S(Pt), N(i32) }
		function f(h: H): i32 {
			match (h) {
				S(Pt { x, .. }) => { return x; },
				N(n) => { return n; }
			}
		}
		function main(): i32 { return f(N(1)); }`
	if err := checkSource(t, src); err != nil {
		t.Fatalf("struct pattern at a payload slot should type-check: %v", err)
	}
}

// A struct has one shape, so the slot never fails on itself — the arm covers
// its variant alone, with no wildcard and no sibling.
func TestStructPatternAtPayloadSlotIsIrrefutable(t *testing.T) {
	src := `struct Pt { x: i32, y: i32 }
		enum H { S(Pt), N(i32) }
		function f(h: H): i32 {
			match (h) {
				S(Pt { x, y }) => { return x + y; },
				N(n) => { return n; }
			}
		}
		function main(): i32 { return f(N(1)); }`
	if err := checkSource(t, src); err != nil {
		t.Fatalf("a struct slot covers its variant on its own: %v", err)
	}
}

// A field carrying a literal DOES make the slot refutable, so that arm no
// longer covers `S` by itself and the match needs another one.
func TestStructPatternFieldLiteralIsRefutable(t *testing.T) {
	src := `struct Pt { x: i32, y: i32 }
		enum H { S(Pt), N(i32) }
		function f(h: H): i32 {
			match (h) {
				S(Pt { x: 1, y }) => { return y; },
				N(n) => { return n; }
			}
		}
		function main(): i32 { return f(N(1)); }`
	err := checkSource(t, src)
	if err == nil {
		t.Fatal("a field literal makes the slot refutable, so S is not covered")
	}
	if !strings.Contains(err.Error(), "not exhaustive") {
		t.Errorf("want the exhaustiveness diagnostic, got %v", err)
	}
}

// The slot's type decides: a struct pattern where the slot is an ENUM is
// still the variant form, and an unknown variant there is still E014.
func TestVariantPatternAtPayloadSlotUnaffected(t *testing.T) {
	src := `enum In { Ok2(i32), Er2(i32) }
		enum H { S(In), N(i32) }
		function f(h: H): i32 {
			match (h) {
				S(Nope(a)) => { return a; },
				N(n) => { return n; }
			}
		}
		function main(): i32 { return f(N(1)); }`
	err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected the unknown-variant diagnostic")
	}
	if !strings.Contains(err.Error(), "Nope") {
		t.Errorf("error should name the unknown variant; got %v", err)
	}
}

// A struct pattern naming the wrong struct is rejected rather than silently
// projecting the slot's own fields.
func TestStructPatternAtPayloadSlotNameMismatch(t *testing.T) {
	src := `struct Pt { x: i32 }
		struct Other { x: i32 }
		enum H { S(Pt), N(i32) }
		function f(h: H): i32 {
			match (h) {
				S(Other { x }) => { return x; },
				N(n) => { return n; }
			}
		}
		function main(): i32 { return f(N(1)); }`
	err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected a struct-name mismatch diagnostic")
	}
	if !strings.Contains(err.Error(), "Other") {
		t.Errorf("error should name the mismatched struct; got %v", err)
	}
}
