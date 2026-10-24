package interp

import "testing"

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
	let b = if (c) { 1 + 1 } else { 2.5 };
	let t = if (c) { (1, 2) } else { (3.5, 4) };
	let o = if (c) { Some(1) } else { Some(2.5) };
	let ov: f64 = 0.0;
	match (o) { Some(v) => { ov = v; }, None => {} }
	if (x > 0.5 && m > 1.5 && n > 3.0 && p > 0.5 && q > 1.5 && a[0] > 0.5 && g > 2.5 && b > 1.5 && t.0 > 0.5 && t.1 == 2 && ov > 0.5) { return 1; }
	return 0;
}`)
	if n, ok := v.(Number); !ok || n != 1 {
		t.Fatalf("main returned %v, want 1", v)
	}
}
