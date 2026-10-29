package checker

import "testing"

// A float destination settles only a type argument that the call returns, so
// it leaves X = f32x4 of a call that returns f64 (#13821).
func TestFloatDestinationKeepsOtherTypeArgs(t *testing.T) {
	src := `trait Total { function total(self: Self): f64; }
impl Total for f32x4 { function total(self: f32x4): f64 { return 4.0; } }
function twice[X: Total](x: X): f64 { return x.total() * 2.0; }
function id[T](x: T): T { return x; }
function main(): i32 {
	let v: f32x4 = __f32x4_splat(1.0);
	let r: f64 = twice(v);
	let a: f32 = id(1.5);
	let b: f64 = if (true) { id(3) } else { 2.5 };
	return 0;
}`
	if err := checkSource(t, src); err != nil {
		t.Fatalf("want accepted, got %v", err)
	}
}
