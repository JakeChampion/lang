package interp

import "testing"

// An unannotated local whose init joins an integer literal with a float is an
// f64, the integer arm included: `let x = if (c) { 1 } else { 2.5 }` read back
// as an integer made `x > 0.5` a runtime error. A value block arm, an array
// literal's elements and a generic call's arguments are the same.
func TestUntypedFloatJoinSettlesIntegerArm(t *testing.T) {
	v := evalProgramValue(t, `function pick[T](a: T, b: T): T { return a; }
function main(): i32 {
	let c: boolean = true;
	let x = if (c) { 1 } else { 2.5 };
	let m = match (c) { true => 2, false => 0.25 };
	let n = if (!c) { 2.5 } else { let k = 3; 4 };
	let p = if (c) { ({ 1 }) } else { 2.5 };
	let q = if (c) { ({ let t = 0; 2 }) } else { 2.5 };
	let a = [1, 2.5];
	let g = if (c) { pick(3, 4) } else { 2.5 };
	if (x > 0.5 && m > 1.5 && n > 3.0 && p > 0.5 && q > 1.5 && a[0] > 0.5 && g > 2.5) { return 1; }
	return 0;
}`)
	if n, ok := v.(Number); !ok || n != 1 {
		t.Fatalf("main returned %v, want 1", v)
	}
}
