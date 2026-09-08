package interp

import "testing"

func TestUnaryNegationUsesCheckedWidth(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		want         Number
	}{
		{"i32 minimum", `function main(): i32 { var x = -2147483648i32; return -x; }`, -2147483648},
		{"u8 wrap", `function main(): u8 { var x = 1u8; return -x; }`, 255},
		{"u32 wrap", `function main(): u32 { var x = 1u32; return -x; }`, 4294967295},
		{"u64 bits", `function main(): u64 { var x = 1u64; return -x; }`, -1},
		{"i64 minimum", `function main(): i64 { var x = -9223372036854775808i64; return -x; }`, -9223372036854775808},
		{"i64 destination", `function main(): i64 { return -2147483649; }`, -2147483649},
		{"i64 nested destination", `function main(): i64 { return -(-2147483649); }`, 2147483649},
		{"i64 argument", `function main(): i64 { return take(-2147483649); } function take(x: i64): i64 { return x; }`, -2147483649},
		{"i64 tuple context", `function main(): i64 { var pair: (i64, i32) = (-2147483649, 0); return pair.0; }`, -2147483649},
		{"float context", `function main(): i32 { var x: f64 = -2147483649; if (x < -2147483648.0) { return 1; } return 0; }`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := evalProgramValue(t, tc.source)
			if n, ok := got.(Number); !ok || n != tc.want {
				t.Fatalf("got %v, want %v\n%s", got, tc.want, tc.source)
			}
		})
	}
}
