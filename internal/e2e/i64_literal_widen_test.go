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
}
