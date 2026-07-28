package checker

import (
	"strings"
	"testing"
)

// The checked operators `+?` / `-?` / `*?` (#5542) evaluate to `Option[T]`:
// `Some(result)` when the exact result fits the operand type, `None` on
// overflow. They are integer-only — no string / float form and no composite
// overload — and `usize` is rejected because its overflow bound is
// target-width-dependent and so isn't expressible in the target-agnostic IR.
func TestCheckedOperatorTyping(t *testing.T) {
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
	// The result is Option[T]; matching / `?` unwrapping type-checks.
	mustOK(`function main(): i32 { var a: i32 = 1; var b: i32 = 2; match (a +? b) { Some(v) => { return v; }, None => { return 0; } } }`)
	mustOK(`function main(): i32 { var a: i32 = 1; var b: i32 = 2; match (a -? b) { Some(v) => { return v; }, None => { return 0; } } }`)
	mustOK(`function f(): Option[i32] { var a: i32 = 1; var b: i32 = 2; var c: i32 = (a *? b)?; return Some(c); }`)
	mustOK(`function main(): i32 { var a: u8 = 1; var b: u8 = 2; match (a +? b) { Some(v) => { return v as i32; }, None => { return 0; } } }`)
	mustOK(`function main(): i32 { var a: i64 = 1; var b: i64 = 2; match (a *? b) { Some(v) => { return v as i32; }, None => { return 0; } } }`)
	mustOK(`function main(): i32 { var a: u64 = 1; var b: u64 = 2; match (a -? b) { Some(v) => { return v as i32; }, None => { return 0; } } }`)
	// A pair of bare literals defaults to i32, like every other integer op.
	mustOK(`function main(): i32 { match (40 +? 2) { Some(v) => { return v; }, None => { return 0; } } }`)
	// Checked divide / remainder yield Option[T] too.
	mustOK(`function main(): i32 { var a: i32 = 84; var b: i32 = 2; match (a /? b) { Some(v) => { return v; }, None => { return 0; } } }`)
	mustOK(`function main(): i32 { var a: u32 = 85; var b: u32 = 43; match (a %? b) { Some(v) => { return v as i32; }, None => { return 0; } } }`)
	// Checked shifts yield Option[T].
	mustOK(`function main(): i32 { var a: i32 = 1; var b: i32 = 4; match (a <<? b) { Some(v) => { return v; }, None => { return 0; } } }`)
	mustOK(`function main(): i32 { var a: u32 = 256; var b: u32 = 2; match (a >>? b) { Some(v) => { return v as i32; }, None => { return 0; } } }`)

	// Assigning the Option[T] to the wrong element type is a type error.
	mustErr(`function main(): i32 { var a: i32 = 1; var b: i32 = 2; var c: Option[i64] = a +? b; return 0; }`,
		`Option`)
	// No string form.
	mustErr(`function main(): i32 { var s: string = "a"; var t: string = "b"; var u: Option[string] = s +? t; return 0; }`,
		`requires an integer type`)
	// No float form.
	mustErr(`function main(): i32 { var a: f64 = 1.0; var b: f64 = 2.0; var c: Option[f64] = a *? b; return 0; }`,
		`requires an integer type`)
	// Mismatched widths still need an explicit `as`.
	mustErr(`function main(): i32 { var a: i32 = 1; var b: u32 = 2; var c: Option[i32] = a +? b; return 0; }`,
		`share an integer type`)
	// usize is rejected: the overflow bound is target-width-dependent.
	mustErr(`function main(): i32 { var a: usize = 1; var b: usize = 2; match (a +? b) { Some(v) => { return v as i32; }, None => { return 0; } } }`,
		"not supported on `usize`")
	// /? and %? are integer-only and usize-rejecting too.
	mustErr(`function main(): i32 { var a: f64 = 1.0; var b: f64 = 2.0; var c: Option[f64] = a /? b; return 0; }`,
		`requires an integer type`)
	mustErr(`function main(): i32 { var a: usize = 1; var b: usize = 2; match (a %? b) { Some(v) => { return v as i32; }, None => { return 0; } } }`,
		"not supported on `usize`")
}
