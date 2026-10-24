package e2e

import (
	"bytes"
	"os/exec"
	"testing"
)

const unannotatedBigLiteralProgram = `
function main(): i32 {
  let x = 2147483648;        // one past i32 max, no annotation → i64
  if (x < 0) { return 1; }   // i64 2147483648 is positive → false
  return 0;
}
`

const bigLiteralArithmeticProgram = `
function main(): i32 {
  let x = 5000000000;
  return (x / 1000000000) as i32;
}
`

const compoundBigLiteralProgram = `
function main(): i32 {
  let w: i64 = 3 - 4611686018427387904;
  let t = 3 - 4611686018427387904;
  let u = 4611686018427387904 - 3;
  let v = if (u > 0) { 3 - 4611686018427387904 } else { 0 };
  let f = (3 - 4611686018427387904) as f64;
  if (t != w) { return 1; }
  if (u != 0 - w) { return 2; }
  if (v != w) { return 3; }
  if (f > -4600000000000000000.0) { return 4; }
  return ((u / 1000000000000000000) as i32) + 40;
}
`

const wideLiteralSiblingsProgram = `
function id[T](v: T): T { return v; }
function pair[A, B](a: A, b: B): (A, B) { return (a, b); }
function main(): i32 {
  let t = id(4611686018427387904);
  let c = 0;
  if (t > 0) { c = c + 1; }
  if (4611686018427387904 > 1) { c = c + 2; }
  let b = 1 < 4611686018427387904;
  if (b) { c = c + 4; }
  if (4611686018427387904 != 0) { c = c + 8; }
  let p = pair(4611686018427387904, "hello");
  if (p.0 == 4611686018427387904 && p.1 == "hello") { c = c + 16; }
  if (p.0 / 1000000000000000000 == 4) { c = c + 32; }
  return c;
}
`

const compositeBigLiteralProgram = `
function main(): i32 {
  let w: i64 = 4611686018427387904;
  let c = 0;
  let t = (1, 4611686018427387904);
  if (t.1 == w) { c = c + 1; }
  let xs = [4611686018427387904, 1];
  if (xs[0] == w) { c = c + 2; }
  let n = (1, (2, 4611686018427387904));
  if (n.1.1 == w) { c = c + 4; }
  let m = (1, [4611686018427387904]);
  if (m.1[0] == w) { c = c + 8; }
  let d = (1, 4611686018427387904 / 2);
  if (d.1 == w / 2) { c = c + 16; }
  if (t.0 == 1 && xs[1] == 1) { c = c + 32; }
  return c;
}
`

const matchScrutineeBigLiteralProgram = `
function main(): i32 {
  let c = 0;
  match (3 - 4611686018427387904) { 3 => { c = c + 1; }, _ => { c = c + 2; } }
  let m = match (3 - 4611686018427387904) { 3 => 100, _ => 4 };
  c = c + m;
  match (7) { 4611686018427387904 => { c = c + 200; }, 7 => { c = c + 8; }, _ => { } }
  let r = match (4611686018427387904) { 4611686018427387904 => 16, _ => 300 };
  c = c + r;
  if (4611686018427387904 != 0) { c = c + 32; }
  return c;
}
`

const genericScrutineeBigLiteralProgram = `
function pick[T](a: T, b: T): Option[T] { return Some(b); }
function main(): i32 {
  let c = 0;
  match (pick(1, 4611686018427387904)) { Some(v) => { if (v / 1000000000000000000 == 4) { c = c + 1; } }, None => { } }
  let m = match (pick(2, 4611686018427387904)) { Some(v) => v / 1000000000000000000, None => 0 };
  c = c + (m as i32) * 2;
  return c;
}
`

const genericCalleeBigLiteralProgram = `
function make[T](seed: T): (i32) => T { return (k: i32): T => seed; }
function main(): i32 {
  let c = 0;
  let a = make(5000000000)(2);
  if (a == 5000000000) { c = c + 1; }
  let b: i64 = make(5000000000)(2);
  if (b == 5000000000) { c = c + 2; }
  return c;
}
`

const annotatedGenericBigLiteralProgram = `
function pair[A, B](a: A, b: B): (A, B) { return (a, b); }
function first[A, B](a: A, b: B): A { return a; }
function some[T](x: T): Option[T] { return Some(x); }
function main(): i32 {
  let c = 0;
  let o: Option[i64] = some(4611686018427387904);
  match (o) { Some(v) => { if (v / 1000000000000000000 == 4) { c = c + 32; } }, None => { } }
  let p: (i64, string) = pair(1234567890123, "hello");
  if (p.0 / 1000000000000 == 1 && p.1 == "hello") { c = c + 1; }
  let q: i64 = first(1234567890123, "x");
  if (q / 1000000000000 == 1) { c = c + 2; }
  let r: (string, i64) = pair("x", 4611686018427387904);
  if (r.1 / 1000000000000000000 == 4) { c = c + 4; }
  let s: (i64, i64) = pair(4611686018427387904, 5);
  if (s.0 / 1000000000000000000 == 4 && s.1 == 5) { c = c + 8; }
  let t: (i32, i64) = pair(5, 4611686018427387904);
  if (t.0 == 5 && t.1 / 1000000000000000000 == 4) { c = c + 16; }
  return c;
}
`

const arrayDestinationGenericProgram = `
function wrap[T](x: T): T[] { return [x]; }
function two[T](a: T, b: T): T[] { return [a, b]; }
function main(): i32 {
  let c = 0;
  let xs: i64[] = wrap(1234567890123);
  if (xs[0] / 1000000000000 == 1) { c = c + 1; }
  let ys: i64[] = wrap(5);
  if (ys.len() == 1 && ys[0] == 5) { c = c + 2; }
  let zs: i64[] = two(4611686018427387904, 7);
  if (zs[0] / 1000000000000000000 == 4 && zs[1] == 7) { c = c + 4; }
  let ws: i32[] = wrap(9);
  if (ws[0] == 9) { c = c + 8; }
  return c;
}
`

const genericPositionsBigLiteralProgram = `
function both[T](a: T, b: T): (T, T) { return (a, b); }
function pick[T](a: T, b: T): Option[T] { return Some(b); }
function id[T](a: T): T { return a; }
function main(): i32 {
  let c = 0;
  let q = both(1, 4611686018427387904);
  if (q.1 == 4611686018427387904) { c = c + 1; }
  match (pick(1, 4611686018427387904)) { Some(v) => { if (v == 4611686018427387904) { c = c + 2; } }, None => { } }
  if (both(1, 4611686018427387904).1 == 4611686018427387904) { c = c + 4; }
  let x: (u64, u64) = both(1, 4611686018427387904);
  if (x.1 / 1000000000000000000 == 4 && x.0 == 1) { c = c + 8; }
  let z: i64 = both(1, 4611686018427387904).1;
  if (z / 1000000000000000000 == 4) { c = c + 16; }
  if (id(1) + 4294967296 == 4294967297) { c = c + 32; }
  return c;
}
`

const typedFieldBindsAheadOfLiteralProgram = `
struct Same[T] { a: T, b: T }
@noinline function pair[T](a: T, b: T): T { return a; }
function main(): i32 {
  let y: i64 = 8589934592;
  let q = Same { a: 3, b: y };
  let r: i64 = pair(1, y);
  let c = 0;
  if (q.b - y == 0) { c = c + 1; }
  if (q.a + q.b == 8589934595) { c = c + 2; }
  if (r == 1) { c = c + 4; }
  let xs = [Same { a: 1, b: 2 }, Same { a: 3, b: y }];
  if (xs[0].a * 4294967296 + xs[1].b == 12884901888) { c = c + 8; }
  let ws = [Same { a: 3, b: y }, Same { a: 1, b: 2 }];
  if (ws[1].b * 4294967296 == 8589934592) { c = c + 16; }
  let o = Same { a: 1, b: 2 };
  let o2 = o;
  let z: i64 = o2.a * 4294967296;
  if (z == 4294967296 && o.b == 2) { c = c + 32; }
  let p = Same { a: 3, b: 4 };
  let g = (): i64 => p.a * 4294967296;
  if (g() == 12884901888) { c = c + 64; }
  return c;
}
`

const genericStructLocalOneWidthProgram = `
struct Same[T] { a: T, b: T }
struct Stack[T] { items: T[] }
@noinline function take1(x: Same[i64]): i64 { return x.a + x.b + 8589934592; }
@noinline function take(xs: Same[i64][]): i64 { return xs[0].a * 4294967296; }
@noinline function take_stack(x: Stack[i64]): i64 { return x.items[1] * 4294967296; }
function main(): i32 {
  let q = Same { a: 1, b: 2 };
  let c = 0;
  if (take1(q) == 8589934595) { c = c + 1; }
  let p = Same { a: 3, b: 4 };
  if (take([p]) == 12884901888) { c = c + 2; }
  let o = Same { a: 5, b: 6 };
  let r: i64 = o.a;
  r = r * 4294967296;
  if (r == 21474836480 && o.b + 1 == 7) { c = c + 4; }
  let d = Same { a: 7, b: 8 };
  if (d.a + d.b == 15) { c = c + 8; }
  let st = Stack { items: [1, 2] };
  if (take_stack(st) == 8589934592) { c = c + 16; }
  return c;
}
`

const genericStructLocalBigLiteralProgram = `
struct Same[T] { a: T, b: T }
struct Stack[T] { items: T[] }
function main(): i32 {
  let q = Same { a: 1, b: 4611686018427387904 };
  let r: i64 = q.b;
  let c = 0;
  if (r == 4611686018427387904) { c = c + 1; }
  if (q.a + 1 == 2) { c = c + 2; }
  if (q.b - q.a == 4611686018427387903) { c = c + 4; }
  let xs = [Same { a: 1, b: 4611686018427387904 }];
  if (xs[0].b == r) { c = c + 8; }
  let st = Stack { items: [1, 4611686018427387904] };
  if (st.items[1] == r) { c = c + 16; }
  return c;
}
`

const arrayArgumentStructLiteralProgram = `
struct Same[T] { a: T, b: T }
function take(xs: Same[i64][]): i32 {
  let s: i64 = xs[0].a + xs[0].b + xs[1].b;
  return (s - 8589934592) as i32;
}
function main(): i32 {
  let y: i64 = 4294967296;
  return take([Same { a: 3, b: y }, Same { a: 1, b: 4294967296 }]);
}
`

const shiftLiteralReadsCountWidthProgram = `
function wide(v: i64): i64 { return v; }
function main(): i32 {
  let k: i64 = 40;
  let n: i32 = 3;
  let c = 0;
  if ((1 << k) == 1099511627776) { c = c + 1; }
  if (wide(0x200000 >> n as i64) == 262144) { c = c + 2; }
  if (wide(1 - (1 << k)) == -1099511627775) { c = c + 4; }
  return c;
}
`

func TestInterpUnannotatedBigLiteralWidens(t *testing.T) {
	bin := buildLangBinForInterp(t)
	run := func(src string, want int, what string) {
		cmd := exec.Command(bin, "-interp", "-")
		cmd.Stdin = bytes.NewReader([]byte(src))
		var out, errb bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errb
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("interp %s: exit = %d, want %d\nstderr: %s", what, code, want, errb.String())
		}
	}
	run(unannotatedBigLiteralProgram, 0, "big-literal widen (stays positive)")
	run(compoundBigLiteralProgram, 44, "big-literal compound")
	run(wideLiteralSiblingsProgram, 63, "big-literal generic call and comparisons")
	run(compositeBigLiteralProgram, 63, "big-literal tuple and array elements")
	run(matchScrutineeBigLiteralProgram, 62, "big-literal match scrutinee")
	run(genericScrutineeBigLiteralProgram, 9, "big-literal generic call as scrutinee")
	run(genericCalleeBigLiteralProgram, 3, "big-literal generic call as callee")
	run(annotatedGenericBigLiteralProgram, 63, "big-literal annotated generic call")
	run(arrayDestinationGenericProgram, 15, "annotated array destination generic call")
	run(genericPositionsBigLiteralProgram, 63, "literal-bound generic call in every position")
	run(arrayArgumentStructLiteralProgram, 3, "struct literals in an array argument")
	run(genericStructLocalBigLiteralProgram, 31, "generic struct local with a wide literal field")
	run(genericStructLocalOneWidthProgram, 31, "generic struct local takes its first use's width")
	run(typedFieldBindsAheadOfLiteralProgram, 127, "typed value binds ahead of an earlier literal")
	run(shiftLiteralReadsCountWidthProgram, 7, "shifted literal reads at the count's width")
}

func TestX86_64UnannotatedBigLiteralWidens(t *testing.T) {
	if _, code := compileAndRunX86_64(t, unannotatedBigLiteralProgram); code != 0 {
		t.Errorf("x86-64 big-literal widen: exit = %d, want 0", code)
	}
	if _, code := compileAndRunX86_64(t, bigLiteralArithmeticProgram); code != 5 {
		t.Errorf("x86-64 big-literal arithmetic: exit = %d, want 5", code)
	}
	if _, code := compileAndRunX86_64(t, compoundBigLiteralProgram); code != 44 {
		t.Errorf("x86-64 big-literal compound: exit = %d, want 44", code)
	}
	if _, code := compileAndRunX86_64(t, wideLiteralSiblingsProgram); code != 63 {
		t.Errorf("x86-64 big-literal generic call and comparisons: exit = %d, want 63", code)
	}
	if _, code := compileAndRunX86_64(t, compositeBigLiteralProgram); code != 63 {
		t.Errorf("x86-64 big-literal tuple and array elements: exit = %d, want 63", code)
	}
	if _, code := compileAndRunX86_64(t, matchScrutineeBigLiteralProgram); code != 62 {
		t.Errorf("x86-64 big-literal match scrutinee: exit = %d, want 62", code)
	}
	if _, code := compileAndRunX86_64(t, genericScrutineeBigLiteralProgram); code != 9 {
		t.Errorf("x86-64 big-literal generic call as scrutinee: exit = %d, want 9", code)
	}
	if _, code := compileAndRunX86_64(t, genericCalleeBigLiteralProgram); code != 3 {
		t.Errorf("x86-64 big-literal generic call as callee: exit = %d, want 3", code)
	}
	if _, code := compileAndRunX86_64(t, arrayDestinationGenericProgram); code != 15 {
		t.Errorf("x86-64 annotated array destination generic call: exit = %d, want 15", code)
	}
	if _, code := compileAndRunX86_64(t, annotatedGenericBigLiteralProgram); code != 63 {
		t.Errorf("x86-64 big-literal annotated generic call: exit = %d, want 63", code)
	}
	if _, code := compileAndRunX86_64(t, genericPositionsBigLiteralProgram); code != 63 {
		t.Errorf("x86-64 literal-bound generic call in every position: exit = %d, want 63", code)
	}
	if _, code := compileAndRunX86_64(t, arrayArgumentStructLiteralProgram); code != 3 {
		t.Errorf("x86-64 struct literals in an array argument: exit = %d, want 3", code)
	}
	if _, code := compileAndRunX86_64(t, genericStructLocalBigLiteralProgram); code != 31 {
		t.Errorf("x86-64 generic struct local with a wide literal field: exit = %d, want 31", code)
	}
	if _, code := compileAndRunX86_64(t, genericStructLocalOneWidthProgram); code != 31 {
		t.Errorf("x86-64 generic struct local takes its first use's width: exit = %d, want 31", code)
	}
	if _, code := compileAndRunX86_64(t, typedFieldBindsAheadOfLiteralProgram); code != 127 {
		t.Errorf("x86-64 typed value binds ahead of an earlier literal: exit = %d, want 127", code)
	}
	if _, code := compileAndRunX86_64(t, shiftLiteralReadsCountWidthProgram); code != 7 {
		t.Errorf("x86-64 shifted literal reads at the count's width: exit = %d, want 7", code)
	}
}

func TestArm64UnannotatedBigLiteralWidens(t *testing.T) {
	if _, code := compileAndRunArm64(t, unannotatedBigLiteralProgram); code != 0 {
		t.Errorf("arm64 big-literal widen: exit = %d, want 0", code)
	}
	if _, code := compileAndRunArm64(t, bigLiteralArithmeticProgram); code != 5 {
		t.Errorf("arm64 big-literal arithmetic: exit = %d, want 5", code)
	}
	if _, code := compileAndRunArm64(t, compoundBigLiteralProgram); code != 44 {
		t.Errorf("arm64 big-literal compound: exit = %d, want 44", code)
	}
	if _, code := compileAndRunArm64(t, wideLiteralSiblingsProgram); code != 63 {
		t.Errorf("arm64 big-literal generic call and comparisons: exit = %d, want 63", code)
	}
	if _, code := compileAndRunArm64(t, compositeBigLiteralProgram); code != 63 {
		t.Errorf("arm64 big-literal tuple and array elements: exit = %d, want 63", code)
	}
	if _, code := compileAndRunArm64(t, matchScrutineeBigLiteralProgram); code != 62 {
		t.Errorf("arm64 big-literal match scrutinee: exit = %d, want 62", code)
	}
	if _, code := compileAndRunArm64(t, genericScrutineeBigLiteralProgram); code != 9 {
		t.Errorf("arm64 big-literal generic call as scrutinee: exit = %d, want 9", code)
	}
	if _, code := compileAndRunArm64(t, genericCalleeBigLiteralProgram); code != 3 {
		t.Errorf("arm64 big-literal generic call as callee: exit = %d, want 3", code)
	}
	if _, code := compileAndRunArm64(t, arrayDestinationGenericProgram); code != 15 {
		t.Errorf("arm64 annotated array destination generic call: exit = %d, want 15", code)
	}
	if _, code := compileAndRunArm64(t, annotatedGenericBigLiteralProgram); code != 63 {
		t.Errorf("arm64 big-literal annotated generic call: exit = %d, want 63", code)
	}
	if _, code := compileAndRunArm64(t, genericPositionsBigLiteralProgram); code != 63 {
		t.Errorf("arm64 literal-bound generic call in every position: exit = %d, want 63", code)
	}
	if _, code := compileAndRunArm64(t, arrayArgumentStructLiteralProgram); code != 3 {
		t.Errorf("arm64 struct literals in an array argument: exit = %d, want 3", code)
	}
	if _, code := compileAndRunArm64(t, genericStructLocalBigLiteralProgram); code != 31 {
		t.Errorf("arm64 generic struct local with a wide literal field: exit = %d, want 31", code)
	}
	if _, code := compileAndRunArm64(t, genericStructLocalOneWidthProgram); code != 31 {
		t.Errorf("arm64 generic struct local takes its first use's width: exit = %d, want 31", code)
	}
	if _, code := compileAndRunArm64(t, typedFieldBindsAheadOfLiteralProgram); code != 127 {
		t.Errorf("arm64 typed value binds ahead of an earlier literal: exit = %d, want 127", code)
	}
	if _, code := compileAndRunArm64(t, shiftLiteralReadsCountWidthProgram); code != 7 {
		t.Errorf("arm64 shifted literal reads at the count's width: exit = %d, want 7", code)
	}
}

func TestWASMUnannotatedBigLiteralWidens(t *testing.T) {
	if code := runWasm(t, unannotatedBigLiteralProgram); code != 0 {
		t.Errorf("wasm big-literal widen: exit = %d, want 0", code)
	}
	if code := runWasm(t, bigLiteralArithmeticProgram); code != 5 {
		t.Errorf("wasm big-literal arithmetic: exit = %d, want 5", code)
	}
	if code := runWasm(t, compoundBigLiteralProgram); code != 44 {
		t.Errorf("wasm big-literal compound: exit = %d, want 44", code)
	}
	if code := runWasm(t, wideLiteralSiblingsProgram); code != 63 {
		t.Errorf("wasm big-literal generic call and comparisons: exit = %d, want 63", code)
	}
	if code := runWasm(t, compositeBigLiteralProgram); code != 63 {
		t.Errorf("wasm big-literal tuple and array elements: exit = %d, want 63", code)
	}
	if code := runWasm(t, matchScrutineeBigLiteralProgram); code != 62 {
		t.Errorf("wasm big-literal match scrutinee: exit = %d, want 62", code)
	}
	if code := runWasm(t, genericScrutineeBigLiteralProgram); code != 9 {
		t.Errorf("wasm big-literal generic call as scrutinee: exit = %d, want 9", code)
	}
	if code := runWasm(t, genericCalleeBigLiteralProgram); code != 3 {
		t.Errorf("wasm big-literal generic call as callee: exit = %d, want 3", code)
	}
	if code := runWasm(t, arrayDestinationGenericProgram); code != 15 {
		t.Errorf("wasm annotated array destination generic call: exit = %d, want 15", code)
	}
	if code := runWasm(t, annotatedGenericBigLiteralProgram); code != 63 {
		t.Errorf("wasm big-literal annotated generic call: exit = %d, want 63", code)
	}
	if code := runWasm(t, genericPositionsBigLiteralProgram); code != 63 {
		t.Errorf("wasm literal-bound generic call in every position: exit = %d, want 63", code)
	}
	if code := runWasm(t, arrayArgumentStructLiteralProgram); code != 3 {
		t.Errorf("wasm struct literals in an array argument: exit = %d, want 3", code)
	}
	if code := runWasm(t, genericStructLocalBigLiteralProgram); code != 31 {
		t.Errorf("wasm generic struct local with a wide literal field: exit = %d, want 31", code)
	}
	if code := runWasm(t, genericStructLocalOneWidthProgram); code != 31 {
		t.Errorf("wasm generic struct local takes its first use's width: exit = %d, want 31", code)
	}
	if code := runWasm(t, typedFieldBindsAheadOfLiteralProgram); code != 127 {
		t.Errorf("wasm typed value binds ahead of an earlier literal: exit = %d, want 127", code)
	}
	if code := runWasm(t, shiftLiteralReadsCountWidthProgram); code != 7 {
		t.Errorf("wasm shifted literal reads at the count's width: exit = %d, want 7", code)
	}
}
