package checker

import "testing"

func TestCheckedAllocationLengthSignature(t *testing.T) {
	wantOK(t, "wide input", `function f(n: i64): i32 { return __alloc_len(n); }`)
	wantOK(t, "literal input", `function f(): i32 { return __alloc_len(1); }`)
	for _, src := range []string{
		`function f(): i32 { return __alloc_len(); }`,
		`function f(): i32 { return __alloc_len(1i64, 2i64); }`,
		`function f(): i32 { return __alloc_len(1.0); }`,
		`function f(): i32 { return __alloc_len(true); }`,
		`function f(): i32 { return __alloc_len("1"); }`,
	} {
		if err := checkSource(t, src); err == nil {
			t.Errorf("accepted invalid length call: %s", src)
		}
	}
}
