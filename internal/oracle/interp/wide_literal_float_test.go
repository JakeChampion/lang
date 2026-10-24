package interp

import "testing"

func TestWideIntLiteralInFloatContextKeepsSign(t *testing.T) {
	v := evalProgramValue(t, `function main(): i32 {
	let f: f64 = 9223372036854775808;
	let g: f64 = 18446744073709551615;
	if (f > 0.0 && g > f) { return 1; }
	return 0;
}`)
	if n, ok := v.(Number); !ok || n != 1 {
		t.Fatalf("main returned %v, want 1: a literal past i64 max read as a negative float", v)
	}
}
