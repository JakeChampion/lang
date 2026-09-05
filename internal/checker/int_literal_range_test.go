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

// The unsigned side was left open when the signed range check landed ("a
// negative literal there wraps today, which is a separate question"). It did
// not wrap consistently: the sign lives on the enclosing unary and the check
// tested the magnitude alone, so `var a: u8 = -1` was accepted and the
// natives stored 0xFFFFFFFF into a u8 slot while the interpreter read 255
// (#8448). A negative literal has no unsigned reading, so it is rejected.
func TestIntLiteralRangeRejectsNegativeUnsigned(t *testing.T) {
	rejected := []string{
		`var x: u8 = -1;`,
		`var x: u32 = -5;`,
		`var x: u64 = -1;`,
		`var x: usize = -1;`,
		`var x: u32 = -2147483649;`,
	}
	for _, src := range rejected {
		err := checkSource(t, "function main(): i32 { "+src+" return 0; }")
		if err == nil {
			t.Errorf("%s: accepted; a negative literal has no unsigned reading", src)
			continue
		}
		if !strings.Contains(err.Error(), "does not fit") {
			t.Errorf("%s: wrong error: %v", src, err)
		}
	}
	accepted := []string{
		`var x: u8 = 0;`,
		`var x: u8 = 255;`,
		`var x: u32 = 4294967295;`,
		`var x: u64 = 18446744073709551615;`,
		// Zero is spelled with a sign in generated code often enough to
		// matter, and negating it is still zero.
		`var x: u32 = -0;`,
	}
	for _, src := range accepted {
		if err := checkSource(t, "function main(): i32 { "+src+" return 0; }"); err != nil {
			t.Errorf("%s: rejected, want accepted: %v", src, err)
		}
	}
}

// The 64-bit widths returned early from the range check, so a literal at
// 2^63 wrapped to i64 MIN with no diagnostic — the parser deferred to the
// checker and the checker deferred to nobody (#8449). Past i64 max the
// literal's Value holds a wrapped bit pattern, so the message has to quote
// the magnitude the source actually wrote.
func TestIntLiteral64BitRange(t *testing.T) {
	cases := []struct {
		src       string
		accepted  bool
		wantInMsg string
	}{
		{`var x: i64 = 9223372036854775807;`, true, ""},
		{`var x: i64 = -9223372036854775808;`, true, ""},
		{`var x: u64 = 18446744073709551615;`, true, ""},
		{`var x: i64 = 9223372036854775808;`, false, "9223372036854775808"},
		{`var x: i64 = 18446744073709551615;`, false, "18446744073709551615"},
		{`var x: i32 = 9223372036854775808;`, false, "9223372036854775808"},
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
		if !strings.Contains(err.Error(), c.wantInMsg) {
			t.Errorf("%s: message does not quote %q as written: %v", c.src, c.wantInMsg, err)
		}
	}
}

// A hex literal with the top bit set was rejected outright while the same
// value in decimal parsed (#8417): the hex path had no unsigned retry. Both
// spellings reach the same range rules now.
func TestHexLiteralTopBitSet(t *testing.T) {
	accepted := []string{
		`var x: u64 = 0xFFFFFFFFFFFFFFFF;`,
		`var x: u64 = 0x8000000000000000;`,
		`var x: i64 = 0x7FFFFFFFFFFFFFFF;`,
		`var x: u32 = 0xFFFFFFFF;`,
	}
	for _, src := range accepted {
		if err := checkSource(t, "function main(): i32 { "+src+" return 0; }"); err != nil {
			t.Errorf("%s: rejected, want accepted: %v", src, err)
		}
	}
	// The same value that overflows i64 is still refused for a signed slot.
	if err := checkSource(t, `function main(): i32 { var x: i64 = 0xFFFFFFFFFFFFFFFF; return 0; }`); err == nil {
		t.Error("0xFFFFFFFFFFFFFFFF accepted for i64; it exceeds the signed range")
	}
}
