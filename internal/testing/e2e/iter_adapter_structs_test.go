package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

const iterAdapterStructsSrc = `import "std/i64";

pub trait Iter[T] {
  function next(self: Self): Option[(T, Self)];
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

function total[I: Iter[i64]](it: I): i64 {
  let acc: i64 = 0 as i64;
  let cur: I = it;
  let go: boolean = true;
  while (go) {
    match (cur.next()) {
      Some(t) => { acc = acc + t.0; cur = t.1; },
      None => { go = false; }
    }
  }
  return acc;
}

function joined[I: Iter[string]](it: I): string {
  let out: string = "";
  let cur: I = it;
  let go: boolean = true;
  while (go) {
    match (cur.next()) {
      Some(t) => { out = out + t.0 + ";"; cur = t.1; },
      None => { go = false; }
    }
  }
  return out;
}

function main(): i32 {
  let xs: i64[] = [1 as i64, 2 as i64, 3 as i64, 4 as i64, 5 as i64, 6 as i64];
  let evens: FilterIt[ArrIt[i64], i64] = FilterIt { it: ArrIt { xs: xs, idx: 0 }, keep: (x: i64): boolean => x % (2 as i64) == (0 as i64) };
  let tens: MapIt[FilterIt[ArrIt[i64], i64], i64, i64] = MapIt { it: evens, f: (x: i64): i64 => x * (10 as i64) };
  print(total(tens));
  let words: MapIt[ArrIt[i64], i64, string] = MapIt { it: ArrIt { xs: xs, idx: 3 }, f: (x: i64): string => "n" + x.to_string() };
  print(joined(words));
  let none: FilterIt[ArrIt[i64], i64] = FilterIt { it: ArrIt { xs: xs, idx: 0 }, keep: (x: i64): boolean => x > (100 as i64) };
  print(total(none));
  return 0;
}
`

const iterAdapterStructsWant = "120\nn4;n5;n6;\n0\n"

func TestInterpIterAdapterStructs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(path, []byte(iterAdapterStructsSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 || out != iterAdapterStructsWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s\nstderr: %s", code, out, iterAdapterStructsWant, errOut)
	}
}

func TestX86_64IterAdapterStructs(t *testing.T) {
	out, code := compileAndRunX86_64(t, iterAdapterStructsSrc)
	if code != 0 || out != iterAdapterStructsWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, iterAdapterStructsWant)
	}
}

func TestArm64IterAdapterStructs(t *testing.T) {
	out, code := compileAndRunArm64(t, iterAdapterStructsSrc)
	if code != 0 || out != iterAdapterStructsWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, iterAdapterStructsWant)
	}
}

func TestWasmIterAdapterStructs(t *testing.T) {
	if out := compileAndRunWasmCapture(t, iterAdapterStructsSrc); out != iterAdapterStructsWant {
		t.Fatalf("printed\n%s\nwant\n%s", out, iterAdapterStructsWant)
	}
}
