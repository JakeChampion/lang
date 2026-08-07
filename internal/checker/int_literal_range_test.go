package checker

import (
	"strings"
	"testing"
)

// The most negative number of a width had no literal spelling. A NumberLit
// carries only the magnitude — the sign is a separate unary node — so the
// range check judged `-2147483648` as the out-of-range POSITIVE 2147483648
// and refused it. `std/math`'s own `i32_min` is written `0 - 2147483647 - 1`
// for exactly this reason, and `i64` never showed the fault because its
// range check returns early.
func TestIntLiteralRangeCountsTheSign(t *testing.T) {
	cases := []struct {
		src      string
		accepted bool
		// When rejected, the message must quote the value as WRITTEN. It used
		// to print the magnitude, so `-2147483649` was reported as a problem
		// with `2147483649` — a number not in the source.
		wantInMsg string
	}{
		{"var x: i32 = -2147483648;", true, ""},
		{"var x: i32 = 2147483647;", true, ""},
		{"var x: i32 = -1;", true, ""},
		{"var x: i32 = 0;", true, ""},
		// Double negation is a positive value again.
		{"var x: i32 = --5;", true, ""},
		{"var x: i32 = -2147483649;", false, "-2147483649"},
		{"var x: i32 = 2147483648;", false, "2147483648"},
		{"var x: i32 = -4000000000;", false, "-4000000000"},
		// Negating the smallest value overflows in the other direction, and
		// the two minuses cancel back to a positive out-of-range magnitude.
		{"var x: i32 = -(-2147483648);", false, "2147483648"},
	}
	for _, c := range cases {
		err := checkSource(t, "function main(): i32 { "+c.src+" return 0; }")
		if c.accepted {
			if err != nil {
				t.Errorf("%s: rejected, want accepted: %v", c.src, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: accepted, want rejected", c.src)
			continue
		}
		if !strings.Contains(err.Error(), "does not fit") {
			t.Errorf("%s: wrong error: %v", c.src, err)
			continue
		}
		if !strings.Contains(err.Error(), c.wantInMsg) {
			t.Errorf("%s: message does not quote %q as written: %v", c.src, c.wantInMsg, err)
		}
	}
}

// The sign is tracked through every position a literal can settle in, not
// only a var initialiser — an argument, a return, an array element and a
// struct field all reach the same check by different routes.
func TestIntLiteralRangeInEveryPosition(t *testing.T) {
	sources := []string{
		`struct S { v: i32 }
function take(v: i32): i32 { return v; }
function main(): i32 { return take(-2147483648); }`,
		`function smallest(): i32 { return -2147483648; }
function main(): i32 { return smallest() + 1; }`,
		`function main(): i32 { var a: i32[] = [-2147483648, 0]; return a[1]; }`,
		`struct S { v: i32 }
function main(): i32 { var s: S = S { v: -2147483648 }; return s.v + 1; }`,
	}
	for i, src := range sources {
		if err := checkSource(t, src); err != nil {
			t.Errorf("position %d rejected the smallest i32: %v", i, err)
		}
	}
}

// Unsigned slots are untouched. A negative literal there wraps today, which
// is a separate question from whether the smallest signed value is spellable.
func TestIntLiteralRangeLeavesUnsignedAlone(t *testing.T) {
	if err := checkSource(t, `function main(): i32 { var x: u32 = -5; return 0; }`); err != nil {
		t.Errorf("u32 = -5 changed behaviour: %v", err)
	}
}
