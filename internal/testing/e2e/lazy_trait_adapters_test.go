package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

const lazyTraitAdaptersSrc = `import "std/i64";

pub trait Iter[T] {
  function next(self: Self): Option[(T, Self)];
  function map[U](self: Self, f: (T) => U): MapIt[Self, T, U] {
    return MapIt { it: self, f: f };
  }
  function filter(self: Self, keep: (T) => boolean): FilterIt[Self, T] {
    return FilterIt { it: self, keep: keep };
  }
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
}

pub struct ArrIt[T] { xs: T[], idx: i32 }

impl[T] Iter[T] for ArrIt[T] {
  function next(self: ArrIt[T]): Option[(T, ArrIt[T])] {
    if (self.idx >= self.xs.len()) { return None; }
    return Some((self.xs[self.idx], ArrIt { xs: self.xs, idx: self.idx + 1 }));
  }
}

pub struct MapIt[I, T, U] { it: I, f: (T) => U }

impl[I: Iter[T], T, U] Iter[U] for MapIt[I, T, U] {
  function next(self: MapIt[I, T, U]): Option[(U, MapIt[I, T, U])] {
    match (self.it.next()) {
      Some(t) => { return Some((self.f(t.0), MapIt { it: t.1, f: self.f })); },
      None => { return None; }
    }
  }
}

pub struct FilterIt[I, T] { it: I, keep: (T) => boolean }

impl[I: Iter[T], T] Iter[T] for FilterIt[I, T] {
  function next(self: FilterIt[I, T]): Option[(T, FilterIt[I, T])] {
    let cur: I = self.it;
    while (true) {
      match (cur.next()) {
        Some(t) => {
          if (self.keep(t.0)) { return Some((t.0, FilterIt { it: t.1, keep: self.keep })); }
          cur = t.1;
        },
        None => { return None; }
      }
    }
    return None;
  }
}

function evens_total[I: Iter[i64]](it: I): i64 {
  return it.filter((x: i64): boolean => x % (2 as i64) == (0 as i64))
           .fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}

function main(): i32 {
  let xs: i64[] = [1 as i64, 2 as i64, 3 as i64, 4 as i64, 5 as i64, 6 as i64];
  let total: i64 = ArrIt { xs: xs, idx: 0 }
    .filter((x: i64): boolean => x % (2 as i64) == (0 as i64))
    .map((x: i64): i64 => x * (10 as i64))
    .fold(0 as i64, (a: i64, b: i64): i64 => a + b);
  print(total.to_string());
  let words: string = ArrIt { xs: xs, idx: 4 }
    .map((x: i64): string => "n" + x.to_string())
    .fold("", (acc: string, w: string): string => acc + w + ";");
  print(words);
  print(evens_total(ArrIt { xs: xs, idx: 0 }.map((x: i64): i64 => x + (1 as i64))).to_string());
  let none: i64 = ArrIt { xs: xs, idx: 0 }
    .filter((x: i64): boolean => x > (100 as i64))
    .filter((x: i64): boolean => x > (200 as i64))
    .fold(0 as i64, (a: i64, b: i64): i64 => a + b);
  print(none.to_string());
  return 0;
}
`

const lazyTraitAdaptersWant = "120\nn5;n6;\n12\n0\n"

func TestInterpLazyTraitAdapters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(path, []byte(lazyTraitAdaptersSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 || out != lazyTraitAdaptersWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s\nstderr: %s", code, out, lazyTraitAdaptersWant, errOut)
	}
}

func TestX86_64LazyTraitAdapters(t *testing.T) {
	out, code := compileAndRunX86_64(t, lazyTraitAdaptersSrc)
	if code != 0 || out != lazyTraitAdaptersWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, lazyTraitAdaptersWant)
	}
}

func TestArm64LazyTraitAdapters(t *testing.T) {
	out, code := compileAndRunArm64(t, lazyTraitAdaptersSrc)
	if code != 0 || out != lazyTraitAdaptersWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, lazyTraitAdaptersWant)
	}
}

func TestWasmLazyTraitAdapters(t *testing.T) {
	if out := compileAndRunWasmCapture(t, lazyTraitAdaptersSrc); out != lazyTraitAdaptersWant {
		t.Fatalf("printed\n%s\nwant\n%s", out, lazyTraitAdaptersWant)
	}
}
