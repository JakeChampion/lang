package e2e

import "testing"

// A `?` reads its source box at the source payload's width, whatever the
// destination reads the result at, and a literal payload settles to that
// destination (#10614). These run on the self-host compiler; the native
// checker's half is TestTryOpTypeIsTheSourcePayload.
func TestTryPayloadWidthIsTheSources(t *testing.T) {
	cases := []struct{ name, src string }{
		{"i32_into_i64", `function f(o: Option[i32]): Option[i64] { return Some((o)? as i64); }
function main(): i32 {
    match (f(Some(3))) {
        Some(v) => { return v as i32; },
        None => { return 98; }
    }
}`},
		{"checked_i32_beside_i64", `function narrow_chk(s: i64, a: i32, b: i32): Option[i64] { return Some(s + ((a +? b)?)); }
function main(): i32 {
    match (narrow_chk(0 - 100i64, 30, 10)) {
        Some(v) => { return (v + 100i64) as i32; },
        None => { return 98; }
    }
}`},
		{"i64_into_i32_and_literal_f32", `function g(o: Option[i64]): Option[i32] { return Some((o)? as i32); }
function h(): Option[f32] { let v: f32 = Some(3.5)?; return Some(v); }
function main(): i32 {
    let a: i32 = match (g(Some(7i64))) { Some(v) => v, None => 0 };
    let b: f32 = match (h()) { Some(v) => v, None => 0.0 };
    return a + (b * 2.0) as i32;
}`},
	}
	for _, c := range cases {
		assertBackendsAgreeWithInterp(t, c.name, c.src)
	}
}
