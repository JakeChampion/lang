package e2e

import "testing"

func TestNegConstSliceBound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping neg-const slice-bound e2e in -short mode")
	}
	assertBackendsAgreeWithInterp(t, "neg_const_bound",
		`function f(s: string): i32 { let i: i32 = s.len() + (0 - 2); let sub: str = slice_unchecked(s, 0, i + 1); return sub.len(); }
function main(): i32 { return f("hello"); }`)
	assertBackendsAgreeWithInterp(t, "neg_const_chain",
		`function f(s: string): i32 { let i: i32 = s.len() - 1 - 1; let sub: str = slice_unchecked(s, 0, i + 1); return sub.len(); }
function main(): i32 { return f("hello"); }`)
	assertBackendsAgreeWithInterp(t, "clean_bounds_regress",
		`function main(): i32 { let s: string = "hello world"; let a: str = slice_unchecked(s, 0, 5); let b: str = slice_unchecked(s, 6, 11); let c: str = slice_unchecked(s, 2, 10); return a.len() + b.len() + c.len(); }`)
	assertBackendsAgreeWithInterp(t, "neg_const_bound_is_none",
		`function f(s: string): i32 { let lo: i32 = s.len() + (0 - 7); match (s[lo:3]) { Some(v) => { return v.len(); }, None => { return 7; } } }
function main(): i32 { return f("hello"); }`)
}
