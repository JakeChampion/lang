package ast

import "testing"

// A parsed literal is a magnitude with the sign on the enclosing unary; a
// literal constfold substituted carries its sign in Value. Both readings have
// to agree: `const NEG = -5` arriving as a literal holding -5 was read as the
// magnitude 2^64-5 and refused for every type, so `var x = NEG` widened to i64
// and `var y: i32 = NEG` drew E047.
func TestIntLitOutOfRangeReadsAFoldedSign(t *testing.T) {
	i32 := NumberType{Width: 32, Signed: true}
	i64 := NumberType{Width: 64, Signed: true}
	u8 := NumberType{Width: 8, Signed: false}
	cases := []struct {
		name    string
		lit     NumberLit
		negated bool
		t       NumberType
		want    string
	}{
		{"parsed magnitude", NumberLit{Value: 5}, false, i32, ""},
		{"parsed negated", NumberLit{Value: 5}, true, i32, ""},
		{"folded negative", NumberLit{Value: -5}, false, i32, ""},
		{"folded negative fits i64", NumberLit{Value: -5}, false, i64, ""},
		{"folded negative negated again", NumberLit{Value: -5}, true, i32, ""},
		{"folded negative has no unsigned reading", NumberLit{Value: -5}, false, u8, "literal -5 does not fit in u8: unsigned types have no negative values"},
		{"folded negative negated is positive", NumberLit{Value: -5}, true, u8, ""},
		{"folded i32 min", NumberLit{Value: -1 << 31}, false, i32, ""},
		{"folded below i32 min", NumberLit{Value: -1<<31 - 1}, false, i32, "literal -2147483649 does not fit in i32"},
		{"folded i64 min", NumberLit{Value: -1 << 63}, false, i64, ""},
		{"parsed i32 max plus one", NumberLit{Value: 1 << 31}, false, i32, "literal 2147483648 does not fit in i32"},
		{"parsed i32 min", NumberLit{Value: 1 << 31}, true, i32, ""},
		{"past i64 max is u64", NumberLit{Value: -1, ExceedsI64: true}, false, NumberType{Width: 64}, ""},
		{"past i64 max is not i64", NumberLit{Value: -1, ExceedsI64: true}, false, i64, "literal 18446744073709551615 does not fit in i64"},
		{"past i64 max negated is not u64", NumberLit{Value: -1, ExceedsI64: true}, true, NumberType{Width: 64}, "literal -18446744073709551615 does not fit in u64: unsigned types have no negative values"},
		{"i64 min written as a magnitude", NumberLit{Value: -1 << 63, ExceedsI64: true}, true, i64, ""},
		{"past u64 fits nothing", NumberLit{Raw: "18446744073709551616", ExceedsU64: true}, false, NumberType{Width: 64}, "literal 18446744073709551616 does not fit in u64"},
		{"hex is quoted as written", NumberLit{Value: -1, Raw: "0xFFFFFFFFFFFFFFFF", ExceedsI64: true}, false, i64, "literal 0xFFFFFFFFFFFFFFFF does not fit in i64"},
		{"negative zero is zero", NumberLit{Value: 0}, true, u8, ""},
	}
	for _, c := range cases {
		lit := c.lit
		if got := IntLitOutOfRange(&lit, c.negated, c.t); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
