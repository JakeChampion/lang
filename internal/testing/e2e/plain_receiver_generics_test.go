package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

const plainReceiverGenericsSrc = `import "std/i32";
import "std/i64";

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
  function zip[U, J: It[U]](self: Self, other: J): Zipped[Self, T, J, U] {
    return Zipped[Self, T, J, U] { a: self, b: other };
  }
}

pub struct Upto { at: i32, end: i32 }
impl It[i32] for Upto {
  function next(self: Upto): Option[(i32, Upto)] {
    if (self.at >= self.end) { return None; }
    return Some((self.at, Upto { at: self.at + 1, end: self.end }));
  }
}

pub struct Arr { xs: i64[], idx: i32 }
impl It[i64] for Arr {
  function next(self: Arr): Option[(i64, Arr)] {
    if (self.idx >= self.xs.len()) { return None; }
    return Some((self.xs[self.idx], Arr { xs: self.xs, idx: self.idx + 1 }));
  }
}

// T and U are phantom: no field holds them.
pub struct Zipped[I, T, J, U] { a: I, b: J }
impl[I: It[T], T, J: It[U], U] It[(T, U)] for Zipped[I, T, J, U] {
  function next(self: Zipped[I, T, J, U]): Option[((T, U), Zipped[I, T, J, U])] {
    match (self.a.next()) {
      Some(x) => {
        match (self.b.next()) {
          Some(y) => { return Some(((x.0, y.0), Zipped[I, T, J, U] { a: x.1, b: y.1 })); },
          None => { return None; }
        }
      },
      None => { return None; }
    }
  }
}

pub struct Box[T] { v: T, n: i32 }
function (b: Box[T]) apply[A](a: A, f: (A, T) => A): A { return f(a, b.v); }
function (b: Box[T]) twice(): i32 { return b.apply(1, (n: i32, v: T): i32 => n * 2); }
function (b: Box[T]) step(): Option[(T, Box[T])] { return Some((b.v, Box { v: b.v, n: b.n + 1 })); }

function label(b: Box[string]): string { return b.v + b.n.to_string(); }

pub struct C { n: i32 }
function (c: C) wrap[U](u: U): Box[U] { return Box { v: u, n: c.n }; }
function (c: C) first_of[U, J: It[U]](j: J): Option[U] {
  match (j.next()) { Some(t) => { return Some(t.0); }, None => { return None; } }
}

function main(): i32 {
  let c: C = C { n: 4 };
  print(c.wrap(7).twice().to_string());
  let bs: Box[string] = c.wrap("b");
  print(label(bs));
  print(label(c.wrap("x")));
  match (c.wrap("s").step()) {
    Some(t) => { print(t.0 + t.1.n.to_string()); },
    None => { print("none"); }
  }
  let xs: i64[] = [10 as i64, 20 as i64, 30 as i64];
  print(Upto { at: 0, end: 9 }.zip(Arr { xs: xs, idx: 0 }).count().to_string());
  let last: i64 = Upto { at: 3, end: 9 }.zip(Arr { xs: xs, idx: 1 })
    .fold(0 as i64, (acc: i64, p: (i32, i64)): i64 => acc + (p.0 as i64) * p.1);
  print(last.to_string());
  match (c.first_of(Arr { xs: xs, idx: 2 })) {
    Some(v) => { print(v.to_string()); },
    None => { print("none"); }
  }
  match (c.first_of(Upto { at: 5, end: 5 })) {
    Some(v) => { print(v.to_string()); },
    None => { print("none"); }
  }
  return 0;
}
`

const plainReceiverGenericsWant = "2\nb4\nx4\ns5\n3\n180\n30\nnone\n"

func TestInterpPlainReceiverGenerics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(path, []byte(plainReceiverGenericsSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 || out != plainReceiverGenericsWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s\nstderr: %s", code, out, plainReceiverGenericsWant, errOut)
	}
}

func TestX86_64PlainReceiverGenerics(t *testing.T) {
	out, code := compileAndRunX86_64(t, plainReceiverGenericsSrc)
	if code != 0 || out != plainReceiverGenericsWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, plainReceiverGenericsWant)
	}
}

func TestArm64PlainReceiverGenerics(t *testing.T) {
	out, code := compileAndRunArm64(t, plainReceiverGenericsSrc)
	if code != 0 || out != plainReceiverGenericsWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, plainReceiverGenericsWant)
	}
}

func TestWasmPlainReceiverGenerics(t *testing.T) {
	if out := compileAndRunWasmCapture(t, plainReceiverGenericsSrc); out != plainReceiverGenericsWant {
		t.Fatalf("printed\n%s\nwant\n%s", out, plainReceiverGenericsWant)
	}
}
