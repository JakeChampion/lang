package checker

import (
	"strings"
	"testing"
)

// The saturating operators `+|` / `-|` / `*|` (#5542) are integer-only: there
// is no string-concat form (unlike `+`), no float form, and no composite
// operator overload. `usize` is rejected outright because its clamp bounds are
// target-width-dependent and so aren't expressible in the target-agnostic IR.
func TestSaturatingOperatorTyping(t *testing.T) {
	mustOK := func(src string) {
		t.Helper()
		if err := checkSource(t, src); err != nil {
			t.Errorf("unexpected error for %q: %v", src, err)
		}
	}
	mustErr := func(src, want string) {
		t.Helper()
		err := checkSource(t, src)
		if err == nil {
			t.Errorf("expected an error for %q, got none", src)
			return
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("for %q: want error containing %q, got: %v", src, want, err)
		}
	}
	mustOK(`function main(): i32 { var a: i32 = 1; var b: i32 = 2; return a +| b; }`)
	mustOK(`function main(): i32 { var a: i32 = 1; var b: i32 = 2; return a -| b; }`)
	mustOK(`function main(): i32 { var a: i32 = 1; var b: i32 = 2; return a *| b; }`)
	mustOK(`function main(): i32 { var a: u8 = 1; var b: u8 = 2; return (a +| b) as i32; }`)
	mustOK(`function main(): i32 { var a: i64 = 1; var b: i64 = 2; return (a *| b) as i32; }`)
	mustOK(`function main(): i32 { var a: u64 = 1; var b: u64 = 2; return (a -| b) as i32; }`)

	// No string form — `+|` is not overloaded the way `+` is.
	mustErr(`function main(): i32 { var s: string = "a"; var t: string = "b"; var u: string = s +| t; return 0; }`,
		`requires an integer type`)
	// No float form.
	mustErr(`function main(): i32 { var a: f64 = 1.0; var b: f64 = 2.0; var c: f64 = a *| b; return 0; }`,
		`requires an integer type`)
	// Mismatched widths still need an explicit `as`.
	mustErr(`function main(): i32 { var a: i32 = 1; var b: u32 = 2; return a +| b; }`,
		`share an integer type`)
	// usize is rejected: the clamp bounds are target-width-dependent.
	mustErr(`function main(): i32 { var a: usize = 1; var b: usize = 2; return (a +| b) as i32; }`,
		"not supported on `usize`")
}
