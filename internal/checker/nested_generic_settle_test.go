package checker

import (
	"strings"
	"testing"
)

// A generic call nested in another generic call's argument takes the width of
// the position reading the outer call, as the non-nested call does (#10508).
func TestNestedGenericCallSettlesToItsReader(t *testing.T) {
	const head = "function id[T](a: T): T { return a; }\nfunction take(x: i64): i32 { return x as i32; }\n"
	good := []string{
		`function main(): i32 { let z: i64 = id(id(5000000000)); return (z / 1000000000) as i32; }`,
		`function main(): i32 { let z: u64 = id(id(1)); return 0; }`,
		`function main(): i32 { let z: i64 = id(id(id(7))); return z as i32; }`,
		`function main(): i32 { return take(id(id(1))); }`,
		`function main(): i32 { if (id(id(1)) == 4611686018427387904) { return 1; } return 0; }`,
		`function main(): i32 { let a: i64 = 3; if (id(id(1)) == a) { return 1; } return 0; }`,
	}
	for _, src := range good {
		if err := checkSource(t, head+src); err != nil {
			t.Errorf("%s: %v", src, err)
		}
	}
	err := checkSource(t, head+`function main(): i32 { let z: u8 = id(id(300)); return 0; }`)
	if err == nil || !strings.Contains(err.Error(), "literal 300 does not fit in u8") {
		t.Errorf("an out-of-range literal through two generic calls: %v", err)
	}
}
