package e2e

import (
	"bytes"
	"os/exec"
	"testing"
)

// unannotatedBigLiteralProgram pins the #3676 semantics: an UNANNOTATED integer
// literal that doesn't fit i32 (`2147483648`, one past i32 max) defaults to i64
// rather than being silently truncated. i32 is the default int, so `var x = 5`
// stays i32 — but a written-out constant past i32 range has no valid i32 reading,
// so it widens to i64 (option 2 of the issue). Before the fix, native x86-64
// truncated the literal to INT_MIN (so `x < 0` was true → exit 1) while the AST
// interpreter and the self-host IR kept it wide (exit 0); native now widens too,
// so all paths agree on exit 0. Arithmetic on an i32 value still wraps at 32
// bits (#3581) — only the bare literal's own type widens.
const unannotatedBigLiteralProgram = `
function main(): i32 {
  var x = 2147483648;        // one past i32 max, no annotation → i64
  if (x < 0) { return 1; }   // i64 2147483648 is positive → false
  return 0;
}
`

// bigLiteralArithmeticProgram shows the widened literal carries its full value
// into a later i64 op: 5000000000 / 1000000000 == 5 (would be garbage if
// truncated to i32).
const bigLiteralArithmeticProgram = `
function main(): i32 {
  var x = 5000000000;
  return (x / 1000000000) as i32;
}
`

// compoundBigLiteralProgram pins #8668: the widening reads through an
// unannotated binding's ARITHMETIC, not only a bare literal. `3 - 2^62` was
// left polymorphic, nothing settled it, and the literal lowered at the i32
// default as 0 — so `t` was 3, `u` was -3, and no engine complained. Every
// form here must agree with the annotated `: i64` spelling: the compound in
// either operand order, an if-expression arm, and a cast operand.
// 2^62 / 10^18 = 4, so a correct run exits 44; the truncated one exited 1.
const compoundBigLiteralProgram = `
function main(): i32 {
  var w: i64 = 3 - 4611686018427387904;
  var t = 3 - 4611686018427387904;
  var u = 4611686018427387904 - 3;
  var v = if (u > 0) { 3 - 4611686018427387904 } else { 0 };
  var f = (3 - 4611686018427387904) as f64;
  if (t != w) { return 1; }
  if (u != 0 - w) { return 2; }
  if (v != w) { return 3; }
  if (f > -4600000000000000000.0) { return 4; }
  return ((u / 1000000000000000000) as i32) + 40;
}
`

// wideLiteralSiblingsProgram pins the shapes the compound rule reaches
// through their own walks: a generic call whose T is pinned by nothing but
// the literal (scalar, and carried inside a tuple result), and comparisons —
// whose boolean result means nothing outside them ever settles the operands,
// so they take the default at the comparison itself. Each shape adds a
// distinct bit; a correct run exits 63, and one that truncates every literal
// to the i32 default exits 0.
const wideLiteralSiblingsProgram = `
function id[T](v: T): T { return v; }
function pair[A, B](a: A, b: B): (A, B) { return (a, b); }
function main(): i32 {
  var t = id(4611686018427387904);
  var c = 0;
  if (t > 0) { c = c + 1; }
  if (4611686018427387904 > 1) { c = c + 2; }
  var b = 1 < 4611686018427387904;
  if (b) { c = c + 4; }
  if (4611686018427387904 != 0) { c = c + 8; }
  var p = pair(4611686018427387904, "hello");
  if (p.0 == 4611686018427387904 && p.1 == "hello") { c = c + 16; }
  if (p.0 / 1000000000000000000 == 4) { c = c + 32; }
  return c;
}
`

// compositeBigLiteralProgram pins #8722: the widening reaches the ELEMENTS of
// an unannotated tuple / array init, which nothing settled — so a wide element
// typed i32 and lowered at that default, reading back as 0 for 2^62 on every
// native backend while `-interp` computed it wide (`var t = (1, 2^62); var a:
// i64 = t.1;` printed 0 against interp's 4611686018427387904, with no
// diagnostic anywhere). Each element shape adds a distinct bit: a tuple
// element, an array element, a tuple nested in a tuple, an array nested in a
// tuple, an element written as arithmetic, and the neighbouring small elements
// that must keep their own values. A correct run exits 63; before the fix the
// program did not even compile, since comparing an i32-typed element against
// the i64 `w` is E041.
const compositeBigLiteralProgram = `
function main(): i32 {
  var w: i64 = 4611686018427387904;
  var c = 0;
  var t = (1, 4611686018427387904);
  if (t.1 == w) { c = c + 1; }
  var xs = [4611686018427387904, 1];
  if (xs[0] == w) { c = c + 2; }
  var n = (1, (2, 4611686018427387904));
  if (n.1.1 == w) { c = c + 4; }
  var m = (1, [4611686018427387904]);
  if (m.1[0] == w) { c = c + 8; }
  var d = (1, 4611686018427387904 / 2);
  if (d.1 == w / 2) { c = c + 16; }
  if (t.0 == 1 && xs[1] == 1) { c = c + 32; }
  return c;
}
`

// matchScrutineeBigLiteralProgram pins the match half of #8722: a
// still-polymorphic scrutinee settled at the i32 default before its literal
// patterns did, so `3 - 2^62` compared as 3 and took the `3` arm on every
// native engine and in the interpreter, where a wide reading of the scrutinee
// (the annotated `: i64` spelling) rejects it. The scrutinee now settles at
// the width its own literals and the patterns select, in both the statement
// and the expression form. A correct run exits 62; the truncated one exited
// 157 (both first arms taken).
const matchScrutineeBigLiteralProgram = `
function main(): i32 {
  var c = 0;
  match (3 - 4611686018427387904) { 3 => { c = c + 1; }, _ => { c = c + 2; } }
  var m = match (3 - 4611686018427387904) { 3 => 100, _ => 4 };
  c = c + m;
  match (7) { 4611686018427387904 => { c = c + 200; }, 7 => { c = c + 8; }, _ => { } }
  var r = match (4611686018427387904) { 4611686018427387904 => 16, _ => 300 };
  c = c + r;
  if (4611686018427387904 != 0) { c = c + 32; }
  return c;
}
`

// annotatedGenericBigLiteralProgram pins the annotated half of #8722: a
// destination type reaches a generic call's type parameters through the
// callee's RETURN type, so `(i64, string)` binds A of `pair` and `i64` binds A
// of `first` whatever B is bound to, and `Option[i64]` binds T of `some`.
// Before, a tuple or enum destination settled nothing (the parameter
// defaulted to i32 and the literal truncated on the natives while the
// interpreter kept it wide) and a scalar one restamped only a single type
// parameter, so `first(1234567890123, "x")` was refused as E038. A correct
// run exits 63.
const annotatedGenericBigLiteralProgram = `
function pair[A, B](a: A, b: B): (A, B) { return (a, b); }
function first[A, B](a: A, b: B): A { return a; }
function some[T](x: T): Option[T] { return Some(x); }
function main(): i32 {
  var c = 0;
  var o: Option[i64] = some(4611686018427387904);
  match (o) { Some(v) => { if (v / 1000000000000000000 == 4) { c = c + 32; } }, None => { } }
  var p: (i64, string) = pair(1234567890123, "hello");
  if (p.0 / 1000000000000 == 1 && p.1 == "hello") { c = c + 1; }
  var q: i64 = first(1234567890123, "x");
  if (q / 1000000000000 == 1) { c = c + 2; }
  var r: (string, i64) = pair("x", 4611686018427387904);
  if (r.1 / 1000000000000000000 == 4) { c = c + 4; }
  var s: (i64, i64) = pair(4611686018427387904, 5);
  if (s.0 / 1000000000000000000 == 4 && s.1 == 5) { c = c + 8; }
  var t: (i32, i64) = pair(5, 4611686018427387904);
  if (t.0 == 5 && t.1 / 1000000000000000000 == 4) { c = c + 16; }
  return c;
}
`

// arrayDestinationGenericProgram pins #9003: an annotated ARRAY destination
// binds a generic call's type parameter through its `T[]` result the way a
// tuple or scalar destination does. Before, the still-polymorphic element
// compared unequal to the concrete one and the binding was refused as E003
// (`cannot assign i32[] to variable of type i64[]`). A correct run exits 15.
const arrayDestinationGenericProgram = `
function wrap[T](x: T): T[] { return [x]; }
function two[T](a: T, b: T): T[] { return [a, b]; }
function main(): i32 {
  var c = 0;
  var xs: i64[] = wrap(1234567890123);
  if (xs[0] / 1000000000000 == 1) { c = c + 1; }
  var ys: i64[] = wrap(5);
  if (ys.len() == 1 && ys[0] == 5) { c = c + 2; }
  var zs: i64[] = two(4611686018427387904, 7);
  if (zs[0] / 1000000000000000000 == 4 && zs[1] == 7) { c = c + 4; }
  var ws: i32[] = wrap(9);
  if (ws[0] == 9) { c = c + 8; }
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
	run(annotatedGenericBigLiteralProgram, 63, "big-literal annotated generic call")
	run(arrayDestinationGenericProgram, 15, "annotated array destination generic call")
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
	if _, code := compileAndRunX86_64(t, arrayDestinationGenericProgram); code != 15 {
		t.Errorf("x86-64 annotated array destination generic call: exit = %d, want 15", code)
	}
	if _, code := compileAndRunX86_64(t, annotatedGenericBigLiteralProgram); code != 63 {
		t.Errorf("x86-64 big-literal annotated generic call: exit = %d, want 63", code)
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
	if _, code := compileAndRunArm64(t, arrayDestinationGenericProgram); code != 15 {
		t.Errorf("arm64 annotated array destination generic call: exit = %d, want 15", code)
	}
	if _, code := compileAndRunArm64(t, annotatedGenericBigLiteralProgram); code != 63 {
		t.Errorf("arm64 big-literal annotated generic call: exit = %d, want 63", code)
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
	if code := runWasm(t, arrayDestinationGenericProgram); code != 15 {
		t.Errorf("wasm annotated array destination generic call: exit = %d, want 15", code)
	}
	if code := runWasm(t, annotatedGenericBigLiteralProgram); code != 63 {
		t.Errorf("wasm big-literal annotated generic call: exit = %d, want 63", code)
	}
}
