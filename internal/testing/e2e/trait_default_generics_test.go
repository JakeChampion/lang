package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

const defaultCallsOwnParamDefaultSrc = `import "std/i32";
pub trait It[T] {
  function next(self: Self): Option[(T, Self)];
  function fold[A](self: Self, init: A, f: (A, T) => A): A {
    let acc: A = init;
    let cur: Self = self;
    let go: boolean = true;
    while (go) {
      match (cur.next()) {
        Some(t) => { acc = f(acc, t.0); cur = t.1; },
        None => { go = false; }
      }
    }
    return acc;
  }
  function count(self: Self): i32 {
    return self.fold(0, (n: i32, v: T): i32 => n + 1);
  }
}
pub struct ArrIt[T] { xs: T[], idx: i32 }
impl[T] It[T] for ArrIt[T] {
  function next(self: ArrIt[T]): Option[(T, ArrIt[T])] {
    if (self.idx >= self.xs.len()) { return None; }
    return Some((self.xs[self.idx], ArrIt { xs: self.xs, idx: self.idx + 1 }));
  }
}
pub struct R { lo: i32, hi: i32 }
impl It[i32] for R {
  function next(self: R): Option[(i32, R)] {
    if (self.lo >= self.hi) { return None; }
    return Some((self.lo, R { lo: self.lo + 1, hi: self.hi }));
  }
}
function main(): i32 {
  let a: ArrIt[i64] = ArrIt { xs: [1 as i64, 2 as i64], idx: 0 };
  print(a.count().to_string());
  print(R { lo: 0, hi: 5 }.count().to_string());
  return 0;
}
`

const defaultCallsOwnParamDefaultWant = "2\n5\n"

func TestInterpDefaultCallsOwnParamDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(path, []byte(defaultCallsOwnParamDefaultSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 || out != defaultCallsOwnParamDefaultWant {
		t.Fatalf("exit %d, printed %q, want %q\nstderr: %s", code, out, defaultCallsOwnParamDefaultWant, errOut)
	}
}

func TestX86_64DefaultCallsOwnParamDefault(t *testing.T) {
	out, code := compileAndRunX86_64(t, defaultCallsOwnParamDefaultSrc)
	if code != 0 || out != defaultCallsOwnParamDefaultWant {
		t.Fatalf("exit %d, printed %q, want %q", code, out, defaultCallsOwnParamDefaultWant)
	}
}

func TestArm64DefaultCallsOwnParamDefault(t *testing.T) {
	out, code := compileAndRunArm64(t, defaultCallsOwnParamDefaultSrc)
	if code != 0 || out != defaultCallsOwnParamDefaultWant {
		t.Fatalf("exit %d, printed %q, want %q", code, out, defaultCallsOwnParamDefaultWant)
	}
}

func TestWasmDefaultCallsOwnParamDefault(t *testing.T) {
	if out := compileAndRunWasmCapture(t, defaultCallsOwnParamDefaultSrc); out != defaultCallsOwnParamDefaultWant {
		t.Fatalf("printed %q, want %q", out, defaultCallsOwnParamDefaultWant)
	}
}

const tupleStructInstanceSrc = `import "std/i32";
pub struct Box[T] { v: T, n: i32 }
function (b: Box[T]) bump(): Box[T] { return Box { v: b.v, n: b.n + 1 }; }
function main(): i32 {
  let b: Box[(i32, i64)] = Box { v: (1, 2 as i64), n: 0 };
  let c: Box[(i32, i64)] = b.bump().bump();
  print(c.n.to_string());
  print(c.v.0.to_string());
  return 0;
}
`

const tupleStructInstanceWant = "2\n1\n"

func TestInterpTupleStructInstance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(path, []byte(tupleStructInstanceSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 || out != tupleStructInstanceWant {
		t.Fatalf("exit %d, printed %q, want %q\nstderr: %s", code, out, tupleStructInstanceWant, errOut)
	}
}

func TestX86_64TupleStructInstance(t *testing.T) {
	out, code := compileAndRunX86_64(t, tupleStructInstanceSrc)
	if code != 0 || out != tupleStructInstanceWant {
		t.Fatalf("exit %d, printed %q, want %q", code, out, tupleStructInstanceWant)
	}
}

func TestArm64TupleStructInstance(t *testing.T) {
	out, code := compileAndRunArm64(t, tupleStructInstanceSrc)
	if code != 0 || out != tupleStructInstanceWant {
		t.Fatalf("exit %d, printed %q, want %q", code, out, tupleStructInstanceWant)
	}
}

func TestWasmTupleStructInstance(t *testing.T) {
	if out := compileAndRunWasmCapture(t, tupleStructInstanceSrc); out != tupleStructInstanceWant {
		t.Fatalf("printed %q, want %q", out, tupleStructInstanceWant)
	}
}
