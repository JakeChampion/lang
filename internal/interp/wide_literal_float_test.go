package interp

import "testing"

// An integer literal past i64 max settled into float context carries its
// magnitude in Value as a wrapped bit pattern; converted as signed it came out
// negative, so `var f: f64 = 9223372036854775808` was -2^63.
func TestWideIntLiteralInFloatContextKeepsSign(t *testing.T) {
	v := evalProgramValue(t, `function main(): i32 {
	var f: f64 = 9223372036854775808;
	var g: f64 = 18446744073709551615;
	if (f > 0.0 && g > f) { return 1; }
	return 0;
}`)
	if n, ok := v.(Number); !ok || n != 1 {
		t.Fatalf("main returned %v, want 1: a literal past i64 max read as a negative float", v)
	}
}
